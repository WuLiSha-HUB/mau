package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cathome5/host/admin"
	"cathome5/host/config"
	"cathome5/host/group"
	cathomehttp "cathome5/host/http"
	"cathome5/host/session"
)

// 本文件是「一个端口一个宿主实例」的装配面——对齐 C# CatHome4.Http.HttpHost.Start 的两条调用路径：
//   - StartCatHost（CatHostService.Cats.cs:2056）：每猫独立对话端口——chat.html + 本猫视图面 / 指令面 /
//     对话页条件面（context / keyinfo / fullctx / effort / api-role / api-models / api-model）；
//   - StartGroupHost（CatHostService.GroupHost.cs:123）：每组独立群聊端口——group.html + 本组四条运行时路由。
//
// 机制共用：端口分配（猫：端口族沿段方向扫；组：9000-9099 向上）→ server.New + Bind 子集 →
// Start + Pump → Stop。端口即身份：实例闭包绑定所属猫 / 组，不接受跨身份参数（C# 闭包绑定同形）。
//
// 形态差异（记录在案）：每猫端口的 `Chat ` 直投本猫会话，不挂「上工/开工 <项目>」自动挂载面——
// 该面当前只在主端口（默认猫）接线（见 commandRouter.HandleFor）。

// groupPortFrom / groupPortTo 组端口段（对齐 C# GroupPortFrom / GroupPortTo——与端口族段不相交）。
const (
	groupPortFrom = 9000
	groupPortTo   = 9099
)

// hostInstance 是一个运行中的附属宿主实例（每猫 / 每组一端口）。
type hostInstance struct {
	// port 实际监听端口
	port int
	// page 静态页文件名（chat.html / group.html——`/` 的页面）
	page string
	// server HTTP 服务实例（路由 / SSE / 静态面）
	server *cathomehttp.Server
	// stopOnce 停止一次性开关（重复 stop 幂等）
	stopOnce sync.Once
}

// hostInstanceDeps 是附属实例的装配依赖——main 构造一次，全部实例共用。
type hostInstanceDeps struct {
	// DataRoot 数据根
	DataRoot string
	// ConfigDir 配置目录
	ConfigDir string
	// HTMLRoot 静态页根
	HTMLRoot string
	// Version 前端版本（__V__ cache-busting）
	Version string
	// Area 区标识（dev / deploy——观测用）
	Area string
	// FrontendTestPort 前端测试服务端口（区段值）
	FrontendTestPort int
	// Band 端口段（猫端口族分配用）
	Band config.PortBand
	// Admin 管理面依赖（对话页静态路由 / 组行构建 / 思考强度读写的共享数据源）
	Admin *admin.Deps
	// Snapshot 全局快照构建器（每猫端口注入——对齐 C# HttpHostOptions.SnapshotBuilder）
	Snapshot func(includeLogs bool) string
	// DispatchCat 按猫投递指令（每猫端口的 command 面；nil = 不接线）
	DispatchCat func(catID string, sess *session.Session, text string) bool
	// Logf 日志出口（nil = 静默）
	Logf func(level int, message, category string)
}

// log 出声（nil 安全）。
func (d *hostInstanceDeps) log(level int, message, category string) {
	if d != nil && d.Logf != nil {
		d.Logf(level, message, category)
	}
}

// options 构造实例装配参数——page 即 `/` 的静态页（chat.html / group.html）。
func (d *hostInstanceDeps) options(port int, page string) cathomehttp.Options {
	return cathomehttp.Options{
		Port:             port,
		DataRoot:         d.DataRoot,
		ConfigDir:        d.ConfigDir,
		HTMLRoot:         d.HTMLRoot,
		Version:          d.Version,
		ChatPage:         page,
		Area:             d.Area,
		FrontendTestPort: d.FrontendTestPort,
	}
}

// startHostInstance 拉起一个实例并等待监听就绪（≤2 s）。
//
// Start 的返回值只在退出 / 失败时出现——就绪靠回环拨号确认（Start 成功但尚未 accept 的窗口极短，
// 拨号失败重试即可；端口被占则 Start 立即返回错误，不空等）。
func startHostInstance(opts cathomehttp.Options, bind func(*cathomehttp.Server)) (*hostInstance, error) {
	srv := cathomehttp.New(opts)
	if bind != nil {
		bind(srv)
	}
	inst := &hostInstance{port: opts.Port, page: opts.ChatPage, server: srv}
	go srv.Pump()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	address := "127.0.0.1:" + strconv.Itoa(opts.Port)
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return nil, err
			}
			return inst, nil
		default:
		}
		conn, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return inst, nil
		}
		if time.Now().After(deadline) {
			inst.stop()
			return nil, fmt.Errorf("监听未就绪（%s）：%v", address, dialErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stop 优雅停止实例（幂等）。
func (h *hostInstance) stop() {
	if h == nil || h.server == nil {
		return
	}
	h.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.server.Shutdown(ctx)
	})
}

// startCatHost 装配并拉起一只猫的对话端口——对齐 C# StartCatHost 的 HttpHostOptions 子集。
//
// 注入面：视图（首帧全量 + 每帧增量）/ 对话页条件路由 / 指令面 / 快照 / 对话页静态路由。
// 不注入：管理端 RouteRegistrar（C# 每猫 host 只挂 RegisterChatPageRoutes）、cats 列表、
// patch / state / log 增量（管理面板面，对话页不消费）。
func startCatHost(inst *catInstance, deps *hostInstanceDeps, port int) (*hostInstance, error) {
	if inst == nil || inst.session == nil {
		return nil, errors.New("会话未装配")
	}
	sess := inst.session
	wiring := inst.wiring
	apiConfigID := ""
	if inst.cfg != nil {
		apiConfigID = inst.cfg.APIConfigID
	}
	return startHostInstance(deps.options(port, "chat.html"), func(srv *cathomehttp.Server) {
		// 快照——全局观测面（对齐 C# 每实例 SnapshotBuilder 注入）
		if deps.Snapshot != nil {
			srv.BindSnapshot(deps.Snapshot)
		}
		// 视图面——chat 页唯一数据通道：连接首帧全量 + 每帧增量（无变化零字节）
		srv.BindView(sess.BuildViewFullJSON)
		srv.BindViewFrame(sess.TakeViewFrameJSON)
		sess.AttachView()
		sess.AttachConnInfo(srv.ClientCount)
		// 对话页条件面（C# HttpHostOptions 同构：cat host 注入了这七组回调）
		srv.BindContext(sess.BuildContextView)
		srv.BindKeyInfo(sess.BuildKeyInfoView)
		srv.BindFullContext(sess.BuildFullContextView)
		if deps.Admin != nil {
			srv.BindEffort(
				func() string { return deps.Admin.BuildEffortJSON(inst.id) },
				func(value string) string { return deps.Admin.SetEffortJSON(inst.id, value) },
			)
		}
		srv.BindAPIRole(func() string {
			store := (*config.LlmAPIStore)(nil)
			if wiring != nil {
				store = wiring.LLMStore
			}
			return admin.BuildAPIRoleJSON(sess, store, apiConfigID)
		})
		srv.BindAPIRoleToggle(func() string {
			role := sess.ToggleAPIRole()
			return `{"ok":true,"role":` + session.JSONString(role) + `}`
		})
		if wiring != nil && wiring.LLMStore != nil {
			store := wiring.LLMStore
			srv.BindAPIModels(func() string {
				return admin.BuildAPIModelsJSON(store, apiConfigID, sess.APIRoleBackup(), sess.ModelOverride())
			})
			srv.BindAPIModel(func(model string) string {
				value := sess.SetModelOverride(model)
				if wiring.Client != nil {
					wiring.Client.SetModelOverride(value)
				}
				return `{"ok":true,"model":` + session.JSONString(value) + `}`
			})
		}
		// 指令面——Chat / session.new 落本猫会话；cat.* / catcfg.* / delay.* 与管理面同一执行体
		if deps.DispatchCat != nil {
			srv.BindCommand(func(text string) bool { return deps.DispatchCat(inst.id, sess, text) })
		}
		if deps.Admin != nil {
			// 对话页静态路由（chat-images / cache-image / audio）——C# RegisterChatPageRoutes 同形
			srv.BindAdmin(func(mux *http.ServeMux) { admin.RegisterChatPageRoutes(mux, deps.Admin) })
		}
	})
}

// startGroupHost 装配并拉起一个组的群聊端口——对齐 C# StartGroupHost（ServeChatPage=false + group.html +
// 本组四条运行时路由；不注入快照 / 视图 / 指令面）。
func startGroupHost(name string, deps *hostInstanceDeps, port int) (*hostInstance, error) {
	if deps.Admin == nil {
		return nil, errors.New("管理面依赖未接线")
	}
	bound := name
	return startHostInstance(deps.options(port, "group.html"), func(srv *cathomehttp.Server) {
		srv.BindAdmin(func(mux *http.ServeMux) { admin.RegisterGroupHostRoutes(mux, deps.Admin, bound) })
	})
}

// tryBindPort 试绑即释放——端口可用性探测（对齐 C# TcpListener 试绑口径）。
func tryBindPort(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// catHostManager 是每猫独立对话端口的实例表——端口分配 + 起停。
//
// 对齐 C# CatHostService 的 _cats[i].Host / Port 与 AllocatePort：端口族沿段方向扫（dev 8078→8070 /
// deploy 8081→8180），段内全占即失败；启动扫描沿用上次端口优先（被占则重新分配）。
type catHostManager struct {
	// mu 保护 hosts / ports（起停串行——端口分配与实例表是同一把锁）
	mu sync.Mutex
	// deps 装配依赖
	deps *hostInstanceDeps
	// hosts 猫 key → 实例（仅运行中的猫在表内）
	hosts map[string]*hostInstance
	// ports 猫 key → 监听端口（与 hosts 同生同灭）
	ports map[string]int
}

// newCatHostManager 构造猫宿主实例表。
func newCatHostManager(deps *hostInstanceDeps) *catHostManager {
	return &catHostManager{deps: deps, hosts: map[string]*hostInstance{}, ports: map[string]int{}}
}

// port 猫宿主端口（0 = 未运行）。
func (m *catHostManager) port(catKey string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ports[catKey]
}

// running 猫宿主运行态。
func (m *catHostManager) running(catKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.hosts[catKey]
	return ok
}

// start 起猫宿主——preferred>0 先试该端口（启动扫描沿用上次端口），失败回落端口族分配。
//
// 端口绑定竞态（试绑后释放到 Start 的窗口）按 C# 同口径换端口重试一次。
func (m *catHostManager) start(inst *catInstance, preferred int) (int, error) {
	if inst == nil {
		return 0, errors.New("猫条目为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.hosts[inst.id]; ok {
		return existing.port, nil
	}
	port := 0
	if preferred >= 1024 && !m.portTakenLocked(preferred) && tryBindPort(preferred) {
		port = preferred
	}
	if port == 0 {
		port = m.allocateLocked()
	}
	if port <= 0 {
		return 0, fmt.Errorf("端口分配失败（端口段 %d-%d 全占用）", m.deps.Band.FamilyFrom, m.deps.Band.FamilyTo)
	}
	host, err := startCatHost(inst, m.deps, port)
	if err != nil {
		// 试绑后释放的竞态窗口——换端口重试一次（对齐 C# HandleCatStart）
		retry := m.allocateLocked()
		if retry <= 0 {
			return 0, fmt.Errorf("端口绑定失败: %v", err)
		}
		host, err = startCatHost(inst, m.deps, retry)
		if err != nil {
			return 0, fmt.Errorf("端口绑定失败: %v", err)
		}
		port = retry
	}
	m.hosts[inst.id] = host
	m.ports[inst.id] = port
	return port, nil
}

// stop 停猫宿主（未运行无操作）。
func (m *catHostManager) stop(catKey string) {
	m.mu.Lock()
	host, ok := m.hosts[catKey]
	delete(m.hosts, catKey)
	delete(m.ports, catKey)
	m.mu.Unlock()
	if ok && host != nil {
		host.stop()
	}
}

// stopAll 停全部猫宿主——宿主退出收尾。
func (m *catHostManager) stopAll() {
	m.mu.Lock()
	hosts := m.hosts
	m.hosts = map[string]*hostInstance{}
	m.ports = map[string]int{}
	m.mu.Unlock()
	for _, host := range hosts {
		host.stop()
	}
}

// allocateLocked 端口族分配（调用方持锁）——从 FamilyFrom 沿段方向扫，跳过已在表内的端口；全占 -1。
func (m *catHostManager) allocateLocked() int {
	band := m.deps.Band
	step := band.FamilyStep()
	for port := band.FamilyFrom; ; port += step {
		if step > 0 && port > band.FamilyTo {
			return -1
		}
		if step < 0 && port < band.FamilyTo {
			return -1
		}
		if m.portTakenLocked(port) {
			continue
		}
		if tryBindPort(port) {
			return port
		}
	}
}

// portTakenLocked 端口是否已被本表占用（调用方持锁）。
func (m *catHostManager) portTakenLocked(port int) bool {
	for _, used := range m.ports {
		if used == port {
			return true
		}
	}
	return false
}

// groupHostManager 是组轻宿主的实例表——每组一端口（9000-9099 向上寻路），起停随组启停。
//
// 对齐 C# CatHostService.GroupHosts / GroupPorts / AllocateGroupPortLocked / StartGroupHost /
// StopGroupHost / StartEnabledGroupHosts：关组释放的端口可被新组复用。
type groupHostManager struct {
	// mu 保护 hosts / ports（起停与端口分配串行）
	mu sync.Mutex
	// deps 装配依赖
	deps *hostInstanceDeps
	// store 组数据面（组存在性与启用态判据）
	store *group.Store
	// hosts 组名（小写）→ 实例
	hosts map[string]*hostInstance
	// ports 组名（小写）→ 端口
	ports map[string]int
}

// newGroupHostManager 构造组宿主实例表。
func newGroupHostManager(deps *hostInstanceDeps, store *group.Store) *groupHostManager {
	return &groupHostManager{deps: deps, store: store, hosts: map[string]*hostInstance{}, ports: map[string]int{}}
}

// normalizeGroupKey 组名归一（Store 内部同口径：小写 + 去空白）。
func normalizeGroupKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// port 组宿主端口（0 = 未运行）。
func (m *groupHostManager) port(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ports[normalizeGroupKey(name)]
}

// running 组宿主运行态。
func (m *groupHostManager) running(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.hosts[normalizeGroupKey(name)]
	return ok
}

// start 起组宿主——组不存在 / 端口段全占 / 拉起失败一律出声并返回 false（已运行幂等 true）。
//
// 对齐 C# StartGroupHost：组名取 Store 里的规范名（不信任调用方大小写）。
func (m *groupHostManager) start(name string) bool {
	if m == nil || m.store == nil {
		return false
	}
	summary := m.store.GetGroup(name)
	if summary == nil {
		m.deps.log(2, "组宿主启动失败：组不存在 "+name, "GROUP")
		return false
	}
	bound := summary.Name
	key := normalizeGroupKey(bound)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hosts[key]; ok {
		return true
	}
	port := m.allocateLocked()
	if port < 0 {
		m.deps.log(2, fmt.Sprintf("组宿主启动失败：端口段 %d-%d 全占用（组 %s）", groupPortFrom, groupPortTo, bound), "GROUP")
		return false
	}
	host, err := startGroupHost(bound, m.deps, port)
	if err != nil {
		m.deps.log(2, "组宿主启动失败（组 "+bound+"）："+err.Error(), "GROUP")
		return false
	}
	m.hosts[key] = host
	m.ports[key] = port
	m.deps.log(1, "组宿主已启动："+bound+" | 端口 "+strconv.Itoa(port), "GROUP")
	return true
}

// stop 停组宿主——停实例释放端口；未运行无操作。
func (m *groupHostManager) stop(name string) {
	if m == nil {
		return
	}
	key := normalizeGroupKey(name)
	m.mu.Lock()
	host, ok := m.hosts[key]
	delete(m.hosts, key)
	delete(m.ports, key)
	m.mu.Unlock()
	if !ok || host == nil {
		return
	}
	host.stop()
	m.deps.log(1, "组宿主已停止："+name+" | 端口 "+strconv.Itoa(host.port)+" 已释放", "GROUP")
}

// stopAll 停全部组宿主——宿主退出收尾。
func (m *groupHostManager) stopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	hosts := m.hosts
	m.hosts = map[string]*hostInstance{}
	m.ports = map[string]int{}
	m.mu.Unlock()
	for _, host := range hosts {
		host.stop()
	}
}

// startEnabled 启动期批量拉起——enabled 的组各起一个宿主（对齐 C# StartEnabledGroupHosts）。
func (m *groupHostManager) startEnabled() {
	if m == nil || m.store == nil {
		return
	}
	started := 0
	for _, summary := range m.store.ListGroups() {
		if !summary.Enabled {
			continue
		}
		if m.start(summary.Name) {
			started++
		}
	}
	if started > 0 {
		m.deps.log(1, "组宿主批量拉起："+strconv.Itoa(started)+" 组", "GROUP")
	}
}

// allocateLocked 组端口分配（调用方持锁）——9000 起向上试绑，跳过表内已占者；全占 -1。
func (m *groupHostManager) allocateLocked() int {
	for port := groupPortFrom; port <= groupPortTo; port++ {
		taken := false
		for _, used := range m.ports {
			if used == port {
				taken = true
				break
			}
		}
		if taken {
			continue
		}
		if tryBindPort(port) {
			return port
		}
	}
	return -1
}
