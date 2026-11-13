package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cathome5/host/config"
	"cathome5/host/session"
)

// 本文件是多猫面（d-1）的存储面与注册表——对齐 C# CatHostService.Cats.cs / CatRuntime.cs：
//   - cat.cfg 只承载用户配置（sessions/<id>/cat.cfg）；
//   - 运行态（running / port）独立落盘 Data/runtime/cats.json（缺失记录 = 静默态）；
//   - 启动扫描 LoadCatsOnBoot：sessions/*/cat.cfg 全部加载进注册表，running 猫补建会话；
//   - cat.new 建猫（ticks id + 默认命名 + cat-default.cfg 模板继承 + 会话装配）；
//   - cat.delete 删目录（sessions/<id> 整棵）。
//
// 形态差异（Go 单端口设计，见 L3 A 域）：多猫暂无独立对话端口——port 恒 0（未监听），
// 会话层由 cat.chat / 组派单 / 订阅推送消费；每猫 HttpHost 与组轻宿主归后续轮。

// dotNetTicksEpochOffset .NET ticks 与 Unix 纳秒的纪元偏移（0001-01-01 → 1970-01-01 = 62135596800 秒）。
const dotNetTicksEpochOffset = 621355968000000000

// lastSessionIDTicks 进程内最后发放的 ticks（单调保护——同一百纳秒内连续建猫不撞 key）。
var lastSessionIDTicks int64

// newSessionID 生成猫 key——对齐 C# SessionStore.NewSessionId（DateTime.Now.Ticks.ToString()，17–18 位数字）。
//
// 进程内单调：C# 未防护毫秒级连发撞车；猫 key 是会话目录名与寻址主键，唯一性是存储面硬约束。
func newSessionID() string {
	ticks := time.Now().UnixNano()/100 + dotNetTicksEpochOffset
	for {
		prev := atomic.LoadInt64(&lastSessionIDTicks)
		if ticks <= prev {
			ticks = prev + 1
		}
		if atomic.CompareAndSwapInt64(&lastSessionIDTicks, prev, ticks) {
			return strconv.FormatInt(ticks, 10)
		}
	}
}

// catRuntimeEntry 运行态条目——running + port（C# CatRuntimeEntry 等价）。
type catRuntimeEntry struct {
	// Running 是否运行态——启动扫描拉起依据
	Running bool `json:"running"`
	// Port 监听端口（0 = 未监听）
	Port int `json:"port"`
}

// catRuntimeDoc 运行态文件形态 { "cats": { "<id>": {...} } }。
type catRuntimeDoc struct {
	// Cats 猫 id → 运行态条目
	Cats map[string]catRuntimeEntry `json:"cats"`
}

// catRuntimePath 运行态文件路径——Data/runtime/cats.json。
func catRuntimePath(dataRoot string) string {
	return filepath.Join(dataRoot, "runtime", "cats.json")
}

// loadCatRuntime 读取运行态全表——防御式（缺失 / 损坏返回空表；不阻断启动）。
func loadCatRuntime(dataRoot string, logf func(level int, message, category string)) map[string]catRuntimeEntry {
	mapOut := map[string]catRuntimeEntry{}
	raw, err := os.ReadFile(catRuntimePath(dataRoot))
	if err != nil {
		if !os.IsNotExist(err) && logf != nil {
			logf(2, "猫运行态读取失败（按空表处理）："+err.Error(), "CHAT")
		}
		return mapOut
	}
	var doc catRuntimeDoc
	if err := json.Unmarshal(config.StripBOM(raw), &doc); err != nil {
		if logf != nil {
			logf(2, "猫运行态解析失败（按空表处理）："+err.Error(), "CHAT")
		}
		return mapOut
	}
	if doc.Cats != nil {
		mapOut = doc.Cats
	}
	return mapOut
}

// saveCatRuntime 写入运行态全表——原子写（临时文件 + 改名）。
func saveCatRuntime(dataRoot string, entries map[string]catRuntimeEntry, logf func(level int, message, category string)) {
	body, err := json.Marshal(catRuntimeDoc{Cats: entries})
	if err != nil {
		if logf != nil {
			logf(2, "猫运行态序列化失败："+err.Error(), "CHAT")
		}
		return
	}
	path := catRuntimePath(dataRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		if logf != nil {
			logf(2, "猫运行态写入失败："+err.Error(), "CHAT")
		}
		return
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, body, 0o644); err != nil {
		if logf != nil {
			logf(2, "猫运行态写入失败："+err.Error(), "CHAT")
		}
		return
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		if logf != nil {
			logf(2, "猫运行态写入失败："+err.Error(), "CHAT")
		}
	}
}

// catInstance 是单猫注册条目——配置 + 会话句柄 + 运行态。
//
// 对齐 C# CatEntry：id / displayName / type / session / running / port；
// session 为 nil = 静默态（未建会话——cat.start 补建）。
type catInstance struct {
	// id 猫 key（寻址主键 / 会话目录名）
	id string
	// displayName 显示名
	displayName string
	// typ 猫 Type 标签（空 = 默认组）
	typ string
	// cfg 最近一次读取的 cat.cfg（装配 / 列表用；写面后刷新）
	cfg *config.CatCfg
	// session 会话实体（nil = 静默）
	session *session.Session
	// wiring 会话装配结果（nil = 静默）
	wiring *sessionWiring
	// running 运行态（真值 = 运行态文件 / cat.start / cat.stop）
	running bool
	// port 监听端口（Go 单端口形态恒 0——未监听独立端口）
	port int
}

// catRegistry 是多猫注册表——majordomo 单列 + 多猫列表（不含 majordomo，对齐 C# _cats）。
type catRegistry struct {
	// mu 保护 major / cats / runtime 全表（指令线程与主循环并发访问）
	mu sync.Mutex
	// dataRoot 数据根
	dataRoot string
	// major 默认猫（majordomo——恒存在，不进 cats 列表）
	major *catInstance
	// cats 多猫列表（目录序；不含 majordomo）
	cats []*catInstance
	// runtime 运行态表（runtime/cats.json 的内存副本）
	runtime map[string]catRuntimeEntry
	// assemble 会话装配函数（main 注入；nil = 不可装配——cat.new / cat.start 明示）
	assemble func(catID string) *sessionWiring
	// validateRoots 受控根白名单校验（新猫继承 defaultEnabledRoots 时过滤；nil = 原样）
	validateRoots func(ids []string) (kept, removed []string, poolUnavailable bool)
	// logf 出声回调（nil = 静默）
	logf func(level int, message, category string)
}

// newCatRegistry 构造多猫注册表（运行态按需懒加载）。
func newCatRegistry(dataRoot string) *catRegistry {
	return &catRegistry{dataRoot: dataRoot, runtime: map[string]catRuntimeEntry{}}
}

// log 出声（nil 安全）。
func (r *catRegistry) log(level int, message, category string) {
	if r.logf != nil {
		r.logf(level, message, category)
	}
}

// setMajor 注册默认猫（majordomo）——启动装配完成后调用（running 恒 true；port = 主端口）。
func (r *catRegistry) setMajor(w *sessionWiring, displayName string, port int) {
	if w == nil {
		return
	}
	r.mu.Lock()
	r.major = &catInstance{id: w.CatID, displayName: displayName, session: w.Session, wiring: w, running: true, port: port}
	r.mu.Unlock()
}

// pumpAll 全部会话推进（主循环每帧调用——每猫 Session.Pump 一条）。
func (r *catRegistry) pumpAll() {
	for _, sess := range r.sessions() {
		sess.Pump()
	}
}

// pumpDelays 延迟队列泵——共享单实例，到点条目按 catKey 分发到归属会话（主循环每帧调用）。
//
// 停机态（宿主重启中）不投递——条目保留，由重启后的新宿主加载后投递；
// 归属会话不存在——条目丢弃并出声（不静默）。对齐 C# `ChatBridge.DeliverDelay`。
func (r *catRegistry) pumpDelays(delays *session.DelayQueue) {
	if delays == nil {
		return
	}
	delays.Pump(func(catKey, content, source string) bool {
		if session.HostRestarting() {
			return false
		}
		sess := r.sessionByKey(catKey)
		if sess == nil {
			session.Logf("CatHome5", 2, "延迟条目归属会话不存在——已丢弃: cat="+catKey, "DELAY")
			return true
		}
		sess.PostSystemMessage(source, content, "")
		return true
	})
}

// entries 全猫条目——[majordomo] + 多猫（只读快照）。
func (r *catRegistry) entries() []*catInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*catInstance, 0, len(r.cats)+1)
	if r.major != nil {
		out = append(out, r.major)
	}
	out = append(out, r.cats...)
	return out
}

// findCat 猫寻址——对齐 C# FindCat：id 精确 / 显示名精确 / id 前缀唯一（歧义 null）；不含 majordomo。
func (r *catRegistry) findCat(key string) *catInstance {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.findCatLocked(key)
}

// findCatLocked 猫寻址（调用方持锁）。
func (r *catRegistry) findCatLocked(key string) *catInstance {
	if key == "" {
		return nil
	}
	var prefix *catInstance
	for _, inst := range r.cats {
		if inst.id == key || inst.displayName == key {
			return inst
		}
		if strings.HasPrefix(inst.id, key) {
			if prefix != nil {
				return nil // 前缀歧义——要求更精确的键
			}
			prefix = inst
		}
	}
	return prefix
}

// findAny 猫寻址（含默认猫）——majordomo / 默认猫显示名 / 多猫注册表。
func (r *catRegistry) findAny(key string) *catInstance {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.major != nil {
		if key == "majordomo" || key == r.major.id || key == r.major.displayName {
			return r.major
		}
	}
	return r.findCatLocked(key)
}

// findDeliveryCat 投递寻址——对齐 C# FindDeliveryCat：多猫优先，默认会话按 id / 显示名命中（薄壳）。
func (r *catRegistry) findDeliveryCat(key string) *catInstance {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if inst := r.findCatLocked(key); inst != nil {
		return inst
	}
	if r.major == nil {
		return nil
	}
	if key == "majordomo" || key == r.major.id || key == r.major.displayName {
		return r.major
	}
	return nil
}

// sessionByKey 会话查询（含默认猫）——nil = 无该会话（未找到 / 静默）。
func (r *catRegistry) sessionByKey(key string) *session.Session {
	inst := r.findAny(key)
	if inst == nil {
		return nil
	}
	return inst.session
}

// sessions 全部已建会话（运行态判断 / 重启闸门 Idle 判据用）。
func (r *catRegistry) sessions() []*session.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*session.Session, 0, len(r.cats)+1)
	if r.major != nil && r.major.session != nil {
		out = append(out, r.major.session)
	}
	for _, inst := range r.cats {
		if inst.session != nil {
			out = append(out, inst.session)
		}
	}
	return out
}

// nextDefaultName 默认猫名生成——「默认小猫」占用时依次「默认小猫(1)」「默认小猫(2)」…
// （注册表内已用名比对，大小写不敏感；对齐 C# NextDefaultCatName）。
func (r *catRegistry) nextDefaultName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nextDefaultNameLocked()
}

// nextDefaultNameLocked 默认猫名生成（调用方持锁）。
func (r *catRegistry) nextDefaultNameLocked() string {
	const baseName = "默认小猫"
	used := make(map[string]bool, len(r.cats))
	for _, inst := range r.cats {
		if inst.displayName != "" {
			used[strings.ToLower(inst.displayName)] = true
		}
	}
	if !used[strings.ToLower(baseName)] {
		return baseName
	}
	for n := 1; ; n++ {
		candidate := baseName + "(" + strconv.Itoa(n) + ")"
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

// setRuntime 更新单猫运行态——读改写（保留其它猫记录）+ 落盘。
func (r *catRegistry) setRuntime(id string, running bool, port int) {
	if id == "" {
		return
	}
	r.mu.Lock()
	r.runtime[id] = catRuntimeEntry{Running: running, Port: port}
	snapshot := make(map[string]catRuntimeEntry, len(r.runtime))
	for key, value := range r.runtime {
		snapshot[key] = value
	}
	r.mu.Unlock()
	saveCatRuntime(r.dataRoot, snapshot, r.logf)
}

// removeRuntime 移除单猫运行态——cat.delete 调用。
func (r *catRegistry) removeRuntime(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	delete(r.runtime, id)
	snapshot := make(map[string]catRuntimeEntry, len(r.runtime))
	for key, value := range r.runtime {
		snapshot[key] = value
	}
	r.mu.Unlock()
	saveCatRuntime(r.dataRoot, snapshot, r.logf)
}

// createCat 建猫（cat.new 存储面 + 会话装配）——对齐 C# HandleCatNew：
// id = ticks / 空名自动命名 / cat-default.cfg 模板继承 / 会话立即构造（静默态：不监听端口）。
//
// 返回 (实例, 失败文本)——失败时回执文本以 "cat.new | " 开头（对齐 C# 文案）。
func (r *catRegistry) createCat(name string) (*catInstance, string) {
	r.mu.Lock()
	id := newSessionID()
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = r.nextDefaultNameLocked()
	}
	r.mu.Unlock()

	cfg := &config.CatCfg{ID: id, DisplayName: displayName}
	// 模板继承——新猫初值取 cat-default.cfg（模板缺失 = 空四字段，与 C# 同）
	if tpl, err := config.LoadCatDefault(filepath.Join(r.dataRoot, "config", "cat-default.cfg")); err == nil && tpl != nil {
		cfg.Persona = tpl.DefaultPersona
		cfg.ToolNames = tpl.DefaultToolNames
		cfg.InjectList = append([]string{}, tpl.DefaultInjectList...)
		cfg.Packs = append([]string{}, tpl.DefaultPacks...)
		cfg.UserInjectIdleOnly = tpl.DefaultUserInjectIdleOnly
		if len(tpl.DefaultEnabledRoots) > 0 {
			if r.validateRoots != nil {
				kept, _, _ := r.validateRoots(tpl.DefaultEnabledRoots)
				cfg.EnabledRoots = kept
			} else {
				cfg.EnabledRoots = append([]string{}, tpl.DefaultEnabledRoots...)
			}
		}
	}
	cfgPath := filepath.Join(r.dataRoot, "sessions", id, "cat.cfg")
	if err := config.SaveCatCfg(cfgPath, cfg); err != nil {
		return nil, "cat.new | 写入 cat.cfg 失败: " + err.Error()
	}
	// 会话装配（静默态：running=false；会话就绪后 cat.chat 即可投递）
	var wiring *sessionWiring
	if r.assemble != nil {
		wiring = r.assemble(id)
	}
	if wiring == nil || wiring.Session == nil {
		// 装配失败不留半个猫——清理刚写的 cat.cfg 与目录（对齐 C#：构造失败不落档）
		_ = os.RemoveAll(filepath.Join(r.dataRoot, "sessions", id))
		return nil, "cat.new | 会话构造失败"
	}
	inst := &catInstance{id: id, displayName: displayName, cfg: cfg, session: wiring.Session, wiring: wiring}
	r.mu.Lock()
	r.cats = append(r.cats, inst)
	r.mu.Unlock()
	r.log(1, "已创建猫「"+displayName+"」（id "+id+"），静默待启动", "CHAT")
	return inst, ""
}

// deleteCatFiles 删除猫文件——sessions/<id>/ 整棵目录（异常不阻断删除流程）。
func (r *catRegistry) deleteCatFiles(id string) {
	if id == "" {
		return
	}
	dir := filepath.Join(r.dataRoot, "sessions", id)
	if err := os.RemoveAll(dir); err != nil {
		r.log(2, "销毁猫文件清理异常："+err.Error(), "CHAT")
	}
}

// loadOnBoot 启动扫描——对齐 C# LoadCatsOnBoot：
// sessions/*/cat.cfg 全部加载进注册表（跳过 majordomo）；running 猫补建会话，静默猫仅可见。
func (r *catRegistry) loadOnBoot() {
	sessionsDir := filepath.Join(r.dataRoot, "sessions")
	dirs, err := os.ReadDir(sessionsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			r.log(2, "启动扫描：sessions 目录枚举失败（跳过加载）："+err.Error(), "CHAT")
		}
		return
	}
	r.mu.Lock()
	r.runtime = loadCatRuntime(r.dataRoot, r.logf)
	r.mu.Unlock()
	runtimeDirty := false
	for _, entry := range dirs {
		if !entry.IsDir() || strings.EqualFold(entry.Name(), "majordomo") {
			continue
		}
		cfgPath := filepath.Join(sessionsDir, entry.Name(), "cat.cfg")
		cfg, cfgErr := config.LoadCatCfg(cfgPath)
		if cfgErr != nil || cfg == nil || cfg.ID == "" {
			continue
		}
		displayName := cfg.DisplayName
		if displayName == "" {
			displayName = cfg.ID
		}
		inst := &catInstance{id: cfg.ID, displayName: displayName, typ: cfg.Type, cfg: cfg}
		// 运行态取值——文件优先；无记录则从旧 cat.cfg 迁移一次（旧版把 running/port 混在 cat.cfg）
		r.mu.Lock()
		rt, has := r.runtime[cfg.ID]
		if !has {
			if cfg.Running || cfg.Port != 0 {
				rt = catRuntimeEntry{Running: cfg.Running, Port: cfg.Port}
				r.runtime[cfg.ID] = rt
				has = true
				runtimeDirty = true
				r.log(1, "启动扫描：猫「"+displayName+"」运行态迁移（running="+strconv.FormatBool(rt.Running)+" port="+strconv.Itoa(rt.Port)+"）", "CHAT")
			}
		}
		r.cats = append(r.cats, inst)
		r.mu.Unlock()
		if !has || !rt.Running {
			r.log(1, "启动扫描：猫「"+displayName+"」为静默态（id "+cfg.ID+"，不建会话）", "CHAT")
			continue
		}
		// 运行态——补建会话（装配失败按静默态保留条目，出声不阻断）
		if r.assemble == nil {
			r.log(2, "启动扫描：猫 "+cfg.ID+" 会话补建失败（装配函数未接线）", "CHAT")
			continue
		}
		wiring := r.assemble(cfg.ID)
		if wiring == nil || wiring.Session == nil {
			r.log(2, "启动扫描：猫 "+cfg.ID+" 会话补建失败（按静默态保留条目）", "CHAT")
			continue
		}
		r.mu.Lock()
		inst.session = wiring.Session
		inst.wiring = wiring
		inst.running = true
		inst.port = rt.Port // Go 单端口形态：独立对话端口未实现，port 保 0
		r.mu.Unlock()
		r.log(1, "启动扫描：已拉起猫「"+displayName+"」（会话层——无独立端口）", "CHAT")
	}
	if runtimeDirty {
		r.mu.Lock()
		snapshot := make(map[string]catRuntimeEntry, len(r.runtime))
		for key, value := range r.runtime {
			snapshot[key] = value
		}
		r.mu.Unlock()
		saveCatRuntime(r.dataRoot, snapshot, r.logf)
	}
}

// startCat 启动猫会话（cat.start）——对齐 C# HandleCatStart 的会话层语义：
// 未找到 / 已运行 / 会话缺失补建 / 运行态落盘。端口面（独立对话端口）随 Go 单端口形态暂缺。
func (r *catRegistry) startCat(key string) string {
	inst := r.findCat(key)
	if inst == nil {
		return "cat.start | 未找到猫: " + key
	}
	r.mu.Lock()
	running := inst.running
	displayName := inst.displayName
	r.mu.Unlock()
	if running {
		portText := ""
		r.mu.Lock()
		if inst.port > 0 {
			portText = " :" + strconv.Itoa(inst.port)
		}
		r.mu.Unlock()
		return "cat.start | 已在运行: " + displayName + portText
	}
	// 会话缺失（未建 / 已停）→ 补建
	r.mu.Lock()
	hasSession := inst.session != nil
	r.mu.Unlock()
	if !hasSession {
		if r.assemble == nil {
			return "cat.start | 会话重建失败: " + displayName
		}
		wiring := r.assemble(inst.id)
		if wiring == nil || wiring.Session == nil {
			return "cat.start | 会话重建失败: " + displayName
		}
		r.mu.Lock()
		inst.session = wiring.Session
		inst.wiring = wiring
		r.mu.Unlock()
		r.log(1, "猫「"+displayName+"」会话已按磁盘重建（前文 / 视图 / 元数据）", "CHAT")
	}
	r.mu.Lock()
	inst.running = true
	r.mu.Unlock()
	r.setRuntime(inst.id, true, 0)
	r.log(1, "已启动猫「"+displayName+"」（会话层——无独立端口）", "CHAT")
	return "cat.start | " + displayName + " | 已启动（会话层——独立对话端口归后续轮）"
}

// stopCat 停止猫（cat.stop）——对齐 C# HandleCatStop：majordomo 禁停 / 未找到 / 已静默 / 忙拒绝 /
// 释放会话内存 + 运行态落盘。
func (r *catRegistry) stopCat(key string) string {
	if key == "majordomo" {
		return "cat.stop | majordomo 为特殊会话——强制自启，不可停止"
	}
	inst := r.findCat(key)
	if inst == nil {
		return "cat.stop | 未找到猫: " + key
	}
	r.mu.Lock()
	running := inst.running
	displayName := inst.displayName
	sess := inst.session
	r.mu.Unlock()
	if !running {
		return "cat.stop | 已是静默态: " + displayName
	}
	if sess != nil && !sess.IsIdle() {
		return "cat.stop | 忙——本轮未结束，请稍后再试: " + displayName
	}
	// 会话内存释放（磁盘三面已物化：前文 jsonl / 视图 view.jsonl / 元数据 session.json）；
	// 视图待推标记复位——无消费者时不得阻塞全局重启闸门（C# A297 同义）
	if sess != nil && sess.ViewBus != nil {
		sess.ViewBus.ResetPending()
	}
	r.mu.Lock()
	inst.session = nil
	inst.wiring = nil
	inst.running = false
	inst.port = 0
	r.mu.Unlock()
	r.setRuntime(inst.id, false, 0)
	r.log(1, "已停止猫「"+displayName+"」（会话内存已释放）", "CHAT")
	return "cat.stop | " + displayName + " | 已停止（会话内存已释放）"
}

// deleteCat 销毁猫（cat.delete）——对齐 C# HandleCatDelete：majordomo 禁删 / 未找到 /
// 停止（不阻断）→ 移除注册表 → 运行态摘除 → 删目录。
func (r *catRegistry) deleteCat(key string) string {
	if key == "majordomo" {
		return "cat.delete | majordomo 为特殊会话——不可删除"
	}
	inst := r.findCat(key)
	if inst == nil {
		return "cat.delete | 未找到猫: " + key
	}
	r.mu.Lock()
	displayName := inst.displayName
	sess := inst.session
	r.mu.Unlock()
	if sess != nil && sess.ViewBus != nil {
		sess.ViewBus.ResetPending()
	}
	r.mu.Lock()
	for i, item := range r.cats {
		if item == inst {
			r.cats = append(r.cats[:i], r.cats[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	r.removeRuntime(inst.id)
	r.deleteCatFiles(inst.id)
	r.log(1, "已销毁猫「"+displayName+"」（id "+inst.id+"）", "CHAT")
	return "cat.delete | " + displayName + " | 已销毁"
}

// refreshCfg 刷新注册条目的 cfg 缓存（cat.cfg.set / catcfg.apply 写盘后调用；显示名同步）。
func (r *catRegistry) refreshCfg(id string, cfg *config.CatCfg) {
	if id == "" || cfg == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	apply := func(inst *catInstance) {
		inst.cfg = cfg
		if cfg.DisplayName != "" {
			inst.displayName = cfg.DisplayName
		}
		inst.typ = cfg.Type
	}
	if r.major != nil && r.major.id == id {
		apply(r.major)
		return
	}
	for _, inst := range r.cats {
		if inst.id == id {
			apply(inst)
			return
		}
	}
}
