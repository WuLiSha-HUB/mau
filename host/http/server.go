// Package http 复现宿主的主端口 HTTP 外观层：路由装配、静态面、SSE 与只读 API。
//
// 对齐 C# 版 CatHome4.Http.HttpHost：路由注册是构造期装配（不存在的构建器
// 就不注册路由，不做运行时按猫过滤）；静态面全部禁缓存（fonts 长缓存）；
// SSE 帧四行、容量 512、DropOldest。
package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"cathome5/host/config"
)

// Options 是服务器的装配参数。
type Options struct {
	// Port 监听端口（0 = 由系统分配，仅测试用）
	Port int
	// DataRoot 数据根（sessions / config 的父目录）
	DataRoot string
	// ConfigDir 配置目录（Data/config）
	ConfigDir string
	// HTMLRoot 静态页根（index.html / chat.html / css / js / pet / fonts）
	HTMLRoot string
	// Version 前端 cache-busting 版本（替换 __V__）
	Version string
	// ChatPage 该端口服务的页面文件名（空 = index.html；对话页实例 = chat.html）
	ChatPage string
	// Area 区标识（dev / deploy——观测用）
	Area string
	// FrontendTestPort 前端测试服务端口（区段值：dev 8069 / deploy 8099；
	// 0 = 未配置，/api/v1/frontend-test 回 ok:false）
	FrontendTestPort int
}

// Server 是主端口 HTTP 服务器。
type Server struct {
	opts Options
	mux  *http.ServeMux
	hs   *http.Server

	// 配置面（构造期绑定；nil = 未绑定，读面返回空）
	schema *config.Schema
	store  *config.Store

	// 快照面——a3 注入真实构建器；未注入时返回空 JSON 骨架
	snapshotFn  func(includeLogs bool) string
	viewBuilder func() string
	// viewFrameFn 构建视图增量帧（每 tick 取；nil = 不推 view 帧）
	viewFrameFn func() string
	// logsArrayFn 构建快照 logs 段的数组 JSON（?logs=N 替换用）
	logsArrayFn func(n int) string
	// logsQueryFn 构建 /api/v1/logs 的完整响应 JSON
	logsQueryFn func(n int, cat, typePrefix string) string
	// stateCache 是猫卡 state 段缓存（tick 写、连接首帧读——atomic string）
	stateCache atomic.Value
	// snapshotCache 是最近一次快照 JSON（atomic string）
	snapshotCache atomic.Value

	// 条件注册的构建器——nil 即不注册（构造期装配，与 C# 同语义）
	catsBuilder      func() string
	apiRoleBuilder   func() string
	apiModelsBuilder func() string
	depsRefresh      func()
	llmProbe         func() string
	// flowReloader 语料热重载面（nil = 不挂载 /api/v1/flow/reload——管理面板实例不注入）
	flowReloader func(name, mode string) string
	// 对话页条件面——构造期条件注册（nil 即不挂载；C# HttpHostOptions 同语义）
	contextBuilder        func(max int) string
	keyInfoBuilder        func(max int) string
	fullContextBuilder    func(max int) string
	effortBuilder         func() string
	effortSetter          func(value string) string
	apiRoleToggler        func() string
	apiModelSetter        func(model string) string
	injectProjectsBuilder func() string
	injectProjectSetter   func(cat, project string, expert *bool) string
	injectAutoGetter      func(cat string) string
	injectAutoSetter      func(cat string, on *bool) string
	// adminRegister 是管理端路由注册面（nil = 不挂载管理段；C# _routeRegistrar 同语义）
	adminRegister func(mux *http.ServeMux)

	// SSE 连接表
	clientsLock sync.Mutex
	clients     []*sseClient

	// pump 停止信号
	stopOnce sync.Once
	stopCh   chan struct{}

	// 路由装配一次性开关（条件注册依赖 Bind* 先完成）
	routesOnce sync.Once

	// listenAddr 是实际监听地址（Start 成功后填写；atomic 供测试并发读取）
	listenAddr atomic.Value

	// tickHook 是每轮快照节拍的前置钩子（帧号推进等；nil = 无）
	tickHook func()
	// patchFn 构建增量 patch（nil = 不推 patch 帧——退化为只缓存全量快照）
	patchFn func() string
	// logIncrementFn 取日志增量（from 游标 → 数组 JSON + 新游标；nil = 不推 log 帧）
	logIncrementFn func(from int) (string, int)
	// logCursor 日志增量游标（Pump 单 goroutine 独占）
	logCursor int
	// commandFn 是会话指令投递面（返回 false = 未受理）
	commandFn func(text string) bool
	// frameFn 当前全局帧号（回执携带；nil = 0）
	frameFn func() int64
	// cmdSeq 指令序号计数器（原子）
	cmdSeq int64
	// frontendTestHTTP 是前端测试代理客户端（Timeout 120s——对齐 C# FrontendTestHttp；
	// Timeout 仅构造时可设，故随 Server 构造一次）
	frontendTestHTTP *http.Client
}

// New 构造服务器；路由在首次请求或 Start 时装配（条件注册依赖 Bind* 先完成）。
func New(opts Options) *Server {
	s := &Server{opts: opts, mux: http.NewServeMux(), stopCh: make(chan struct{})}
	s.hs = &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(opts.Port), Handler: s}
	s.snapshotCache.Store("")
	s.frontendTestHTTP = &http.Client{Timeout: frontendTestTimeout}
	return s
}

// ServeHTTP 实现 http.Handler——首次请求前装配路由。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.ensureRoutes()
	s.mux.ServeHTTP(w, r)
}

// ensureRoutes 装配路由（幂等）。
func (s *Server) ensureRoutes() {
	s.routesOnce.Do(s.routes)
}

// BindConfig 绑定配置面（schema + store）；nil 允许——读面返回空列表。
func (s *Server) BindConfig(schema *config.Schema, store *config.Store) {
	s.schema = schema
	s.store = store
}

// BindSnapshot 绑定快照构建器（a3 注入）。
func (s *Server) BindSnapshot(fn func(includeLogs bool) string) {
	s.snapshotFn = fn
	if fn != nil {
		s.snapshotCache.Store(fn(false))
	}
}

// BindCats 绑定猫清单构建器；nil = 不注册 /api/v1/cats。
func (s *Server) BindCats(fn func() string) { s.catsBuilder = fn }

// BindAPIRole 绑定端点角色读面；nil = 不注册。
func (s *Server) BindAPIRole(fn func() string) { s.apiRoleBuilder = fn }

// BindAPIModels 绑定模型清单读面；nil = 不注册。
func (s *Server) BindAPIModels(fn func() string) { s.apiModelsBuilder = fn }

// BindDepsRefresh 绑定依赖探针刷新；nil = 不注册。
func (s *Server) BindDepsRefresh(fn func()) { s.depsRefresh = fn }

// BindLLMProbe 绑定端点探活；nil = 不注册。
func (s *Server) BindLLMProbe(fn func() string) { s.llmProbe = fn }

// BindFlowReloader 绑定语料热重载面（name + mode → 回执 JSON）；nil = 不注册 /api/v1/flow/reload。
func (s *Server) BindFlowReloader(fn func(name, mode string) string) { s.flowReloader = fn }

// BindContext 绑定前文条目视图（?max= 上限）；nil = 不注册 /api/v1/context。
func (s *Server) BindContext(fn func(max int) string) { s.contextBuilder = fn }

// BindKeyInfo 绑定关键信息视图；nil = 不注册 /api/v1/keyinfo。
func (s *Server) BindKeyInfo(fn func(max int) string) { s.keyInfoBuilder = fn }

// BindFullContext 绑定完整前文视图；nil = 不注册 /api/v1/fullctx。
func (s *Server) BindFullContext(fn func(max int) string) { s.fullContextBuilder = fn }

// BindEffort 绑定思考强度读写面；nil = 不注册 /api/v1/effort。
func (s *Server) BindEffort(get func() string, set func(value string) string) {
	s.effortBuilder = get
	s.effortSetter = set
}

// BindAPIRoleToggle 绑定端点角色手动对调；nil = 不注册 /api/v1/api-role/toggle。
func (s *Server) BindAPIRoleToggle(fn func() string) { s.apiRoleToggler = fn }

// BindAPIModel 绑定会话级临时模型写入；nil = 不注册 /api/v1/api-model。
func (s *Server) BindAPIModel(fn func(model string) string) { s.apiModelSetter = fn }

// BindInject 绑定项目挂载三件（projects / project / auto）；nil = 不注册。
func (s *Server) BindInject(projects func() string, project func(cat, project string, expert *bool) string, autoGet func(cat string) string, autoSet func(cat string, on *bool) string) {
	s.injectProjectsBuilder = projects
	s.injectProjectSetter = project
	s.injectAutoGetter = autoGet
	s.injectAutoSetter = autoSet
}

// BindView 绑定视图全量构建器（首帧补发用）；nil = 不推 view 帧。
func (s *Server) BindView(fn func() string) { s.viewBuilder = fn }

// BindViewFrame 绑定视图增量帧构建器（每 tick 取帧；无变化零字节）；nil = 不推。
func (s *Server) BindViewFrame(fn func() string) { s.viewFrameFn = fn }

// BindLogs 绑定日志面：快照 logs 段数组与 /api/v1/logs 响应。
func (s *Server) BindLogs(arrayFn func(n int) string, queryFn func(n int, cat, typePrefix string) string) {
	s.logsArrayFn = arrayFn
	s.logsQueryFn = queryFn
}

// BindTick 绑定快照节拍前置钩子（帧号推进等）；nil = 无钩子。
func (s *Server) BindTick(fn func()) { s.tickHook = fn }

// BindPatch 绑定增量 patch 构建器；nil = 不推 patch 帧。
func (s *Server) BindPatch(fn func() string) { s.patchFn = fn }

// BindLogIncrement 绑定日志增量取数（from 游标 → 数组 JSON + 新游标）；nil = 不推 log 帧。
func (s *Server) BindLogIncrement(fn func(from int) (string, int)) { s.logIncrementFn = fn }

// BindCommand 绑定会话指令投递面；nil = 指令入口返回未实现。
func (s *Server) BindCommand(fn func(text string) bool) { s.commandFn = fn }

// BindFrame 绑定全局帧号读取（指令回执携带）；nil = 0。
func (s *Server) BindFrame(fn func() int64) { s.frameFn = fn }

// BindAdmin 绑定管理端路由注册面；nil = 不挂载管理段（C# _routeRegistrar 同语义）。
func (s *Server) BindAdmin(fn func(mux *http.ServeMux)) { s.adminRegister = fn }

// currentSnapshot 返回当前快照缓存。
func (s *Server) currentSnapshot() string {
	v, _ := s.snapshotCache.Load().(string)
	return v
}

// Start 启动监听并进入请求循环（阻塞）。
//
// 路由在监听前装配——保证首个请求不会撞上未装配的 mux（C# BuildApp 同序）。
func (s *Server) Start() error {
	s.ensureRoutes()
	ln, err := net.Listen("tcp", s.hs.Addr)
	if err != nil {
		return err
	}
	s.listenAddr.Store(ln.Addr().String())
	err = s.hs.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown 优雅关闭。
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return s.hs.Shutdown(ctx)
}

// Addr 返回实际监听地址（Start 之前为配置端口）。
func (s *Server) Addr() string {
	if v, ok := s.listenAddr.Load().(string); ok && v != "" {
		return v
	}
	return s.hs.Addr
}

// waitStop 供 pump 循环判定退出（关闭返回 true）。
func (s *Server) waitStop(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-s.stopCh:
		return true
	case <-timer.C:
		return false
	}
}
