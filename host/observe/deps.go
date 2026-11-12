package observe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cathome5/mau/fs"
)

// 依赖级别常量——与 C# DependencyProbe 一致。
const (
	// DepLevelRequired 运行必需
	DepLevelRequired = "required"
	// DepLevelOptional 可选功能
	DepLevelOptional = "optional"
	// DepLevelDev 开发辅助
	DepLevelDev = "dev"
)

// 依赖状态常量。
const (
	// DepStateOK 可用
	DepStateOK = "ok"
	// DepStateUnconfigured 未配置（本就没配——正常态，不报红）
	DepStateUnconfigured = "unconfigured"
	// DepStateMissing 缺失（配了但路径 / 命令找不到）
	DepStateMissing = "missing"
	// DepStateUnusable 不可用（存在但跑不起来）
	DepStateUnusable = "unusable"
)

// DepCommandTimeout 是命令探测超时（对齐 C# CommandTimeoutMs）。
const DepCommandTimeout = 3 * time.Second

// RootPath 是受控根条目的观测面视图。
type RootPath struct {
	// ID 根标识
	ID string
	// Path 绝对路径
	Path string
	// Writable 可写标志
	Writable bool
}

// DepEnv 是依赖探测的输入采集面——由启动链注入，探测包不依赖配置包。
type DepEnv struct {
	// ConfigGet 配置读面（空串 = 未配置；nil = 全部未配置）
	ConfigGet func(key string) string
	// Roots 受控根表
	Roots []RootPath
	// RepoRoot 仓库根（mau 受控根；空 = 未定位）
	RepoRoot string
	// Log 观测出口（nil = 静默）
	Log func(module string, level int, message, category string)
}

// DepResult 是一条依赖探测结果（快照 deps 段条目）。
type DepResult struct {
	// Level 级别（required / optional / dev）
	Level string `json:"level"`
	// Name 显示名
	Name string `json:"name"`
	// State 状态（ok / unconfigured / missing / unusable）
	State string `json:"state"`
	// Detail 细节（版本 / 路径 / 缺失清单）
	Detail string `json:"detail"`
	// ProbedAt 探测时刻（Unix 毫秒）
	ProbedAt int64 `json:"probedAt"`
}

// depJob 是一条待执行的 IO 探测任务。
type depJob struct {
	level    string
	name     string
	kind     string // paths / command / filever
	command  string
	args     []string
	paths    []string
	probedAt int64
}

// DepProbe 是外部依赖探测——配置类即时采集，IO 类后台单飞。
//
// 与 C# DependencyProbe 的差异（有意）：不做 TTL 自动过期——P3-a 无工具面消费方，
// 依赖面变化靠手动 /api/v1/deps/refresh 重探（差异可观测，不外推）。
type DepProbe struct {
	mu      sync.Mutex
	env     DepEnv
	results map[string]*DepResult
	running bool

	// ioWait 用于测试等待后台批次完成（nil = 不等）
	ioWait chan struct{}

	// 注入点——测试可替换（nil 用真实实现）
	statFile   func(path string) bool
	runCommand func(exe string, args []string, timeout time.Duration) (string, bool)
	fileVer    func(path string) (string, bool)
}

// NewDepProbe 创建探测器。
func NewDepProbe(env DepEnv) *DepProbe {
	return &DepProbe{
		env:     env,
		results: make(map[string]*DepResult),
		statFile: func(path string) bool {
			if path == "" {
				return false
			}
			_, err := os.Stat(path)
			return err == nil
		},
		runCommand: runCommandProbe,
		fileVer:    probeFileVersion,
	}
}

// SetEnv 更新输入面（配置 / 根表变化后重新采集）。
func (p *DepProbe) SetEnv(env DepEnv) {
	p.mu.Lock()
	p.env = env
	p.mu.Unlock()
}

// Start 立即采集配置类条目并异步跑 IO 类条目。
func (p *DepProbe) Start() {
	direct, jobs := p.collect()
	p.store(direct)
	if len(jobs) > 0 {
		go p.runJobs(jobs)
	}
}

// Refresh 重置全部条目并重探（配置类同步、IO 类异步）。
func (p *DepProbe) Refresh() {
	direct, jobs := p.collect()
	p.mu.Lock()
	p.results = make(map[string]*DepResult, len(direct)+len(jobs))
	p.mu.Unlock()
	p.store(direct)
	if len(jobs) > 0 {
		go p.runJobs(jobs)
	}
}

// Snapshot 返回按级别（required → optional → dev）+ 名称排序的结果副本。
func (p *DepProbe) Snapshot() []DepResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]DepResult, 0, len(p.results))
	for _, r := range p.results {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := levelOrder(out[i].Level), levelOrder(out[j].Level)
		if li != lj {
			return li < lj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// levelOrder 级别排序权重。
func levelOrder(level string) int {
	switch level {
	case DepLevelRequired:
		return 0
	case DepLevelOptional:
		return 1
	default:
		return 2
	}
}

// store 写入结果表（同名覆盖）。
func (p *DepProbe) store(results []*DepResult) {
	if len(results) == 0 {
		return
	}
	p.mu.Lock()
	for _, r := range results {
		p.results[r.Name] = r
	}
	p.mu.Unlock()
}

// runJobs 后台单飞执行 IO 探测批次。
func (p *DepProbe) runJobs(jobs []depJob) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	wait := p.ioWait
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		if wait != nil {
			close(wait)
		}
	}()
	for _, job := range jobs {
		result := p.runJob(job)
		p.store([]*DepResult{result})
	}
}

// runJob 按探测类型分派。
func (p *DepProbe) runJob(job depJob) *DepResult {
	switch job.kind {
	case "paths":
		return p.runPathsProbe(job)
	case "command":
		return p.runCommandJob(job)
	case "filever":
		return p.runFileVersionJob(job)
	default:
		return makeDepResult(job.level, job.name, DepStateUnusable, "未知探测类型："+job.kind, job.probedAt)
	}
}

// runPathsProbe 路径集探测——全部存在即可用；缺失项逐个列出。
func (p *DepProbe) runPathsProbe(job depJob) *DepResult {
	var missing []string
	for _, path := range job.paths {
		if !p.statFile(path) {
			if path == "" {
				missing = append(missing, "(空)")
			} else {
				missing = append(missing, path)
			}
		}
	}
	if len(missing) == 0 {
		return makeDepResult(job.level, job.name, DepStateOK, "路径就绪（"+itoa(len(job.paths))+" 项）", job.probedAt)
	}
	return makeDepResult(job.level, job.name, DepStateMissing, "缺失："+strings.Join(missing, "; "), job.probedAt)
}

// runCommandJob 命令探测——绝对路径不存在即缺失；否则跑一次读首行版本。
func (p *DepProbe) runCommandJob(job depJob) *DepResult {
	absolute := strings.ContainsAny(job.command, "\\/")
	if absolute && !p.statFile(job.command) {
		return makeDepResult(job.level, job.name, DepStateMissing, "文件不存在："+job.command, job.probedAt)
	}
	output, ran := p.runCommand(job.command, job.args, DepCommandTimeout)
	if !ran {
		return makeDepResult(job.level, job.name, DepStateUnusable, "执行失败："+job.command, job.probedAt)
	}
	version := firstLine(output)
	switch {
	case version == "":
		version = "已就绪（无版本输出）"
	case !isTextClean(version):
		if p.env.Log != nil {
			p.env.Log("CatHome4", LevelWarn, "依赖探测版本输出不可解析（编码异常）："+job.command, "DEPS")
		}
		version = "已就绪（版本输出不可解析）"
	}
	return makeDepResult(job.level, job.name, DepStateOK, version, job.probedAt)
}

// runFileVersionJob PE 版本探测——文件存在 + 版本读取（缺失版本回落「文件就绪」）。
func (p *DepProbe) runFileVersionJob(job depJob) *DepResult {
	if len(job.paths) == 0 {
		return makeDepResult(job.level, job.name, DepStateUnusable, "无目标路径", job.probedAt)
	}
	path := job.paths[0]
	if !p.statFile(path) {
		return makeDepResult(job.level, job.name, DepStateMissing, "文件不存在："+path, job.probedAt)
	}
	version, ok := p.fileVer(path)
	if !ok {
		return makeDepResult(job.level, job.name, DepStateUnusable, "版本读取失败", job.probedAt)
	}
	if version == "" {
		version = "文件就绪（无版本信息）"
	}
	return makeDepResult(job.level, job.name, DepStateOK, version, job.probedAt)
}

// makeDepResult 构造结果条目。
func makeDepResult(level, name, state, detail string, probedAt int64) *DepResult {
	return &DepResult{Level: level, Name: name, State: state, Detail: detail, ProbedAt: probedAt}
}

// collect 采集探测输入——配置类即时定案，IO 类产出任务。
func (p *DepProbe) collect() ([]*DepResult, []depJob) {
	p.mu.Lock()
	env := p.env
	p.mu.Unlock()
	now := time.Now().UnixMilli()
	cfg := func(key string) string {
		if env.ConfigGet == nil {
			return ""
		}
		return env.ConfigGet(key)
	}
	var direct []*DepResult
	var jobs []depJob

	// [段1] 受控根表——逐根目录存在性（运行必需）
	if len(env.Roots) == 0 {
		direct = append(direct, makeDepResult(DepLevelRequired, "受控根表", DepStateUnusable, "受控根表为空", now))
	} else {
		paths := make([]string, 0, len(env.Roots))
		for _, r := range env.Roots {
			paths = append(paths, r.Path)
		}
		jobs = append(jobs, depJob{level: DepLevelRequired, name: "受控根表", kind: "paths", paths: paths, probedAt: now})
	}

	// [段2] 解释器与内核——路径配置 + 真跑一次读版本（运行必需）
	direct, jobs = addPathCommandProbe(direct, jobs, cfg, "ps.pwsh_path", DepLevelRequired, "PowerShell 7", []string{"--version"}, now)
	direct, jobs = addPathCommandProbe(direct, jobs, cfg, "py.python_path", DepLevelRequired, "Python", []string{"--version"}, now)
	direct, jobs = addPathCommandProbe(direct, jobs, cfg, "node.node_path", DepLevelRequired, "Node.js", []string{"--version"}, now)
	direct, jobs = addPathFileVersionProbe(direct, jobs, cfg, "browser.kernel_path", DepLevelRequired, "浏览器内核", now)

	// [段3] Kokoro 服务——配置完整性（不探活：拉起常驻进程副作用不可接受）
	direct, jobs = collectKokoro(direct, jobs, cfg, env.Roots, now)

	// [段3b] node 服务——配置完整性
	direct, jobs = collectStdioService(direct, jobs, cfg, env.Roots, now, "ts", "ts", "Node 服务（ts）")

	// [段4] 可选功能——配置完整性（未配置属正常态）
	direct = append(direct, configPresence(cfg, "search.api_config_id", DepLevelOptional, "搜索 API", now))
	direct = append(direct, configPresence(cfg, "vision.api_config_id", DepLevelOptional, "视觉 API", now))

	// [段5] 开发辅助——外部命令版本 + 自举链文件
	jobs = append(jobs, depJob{level: DepLevelDev, name: "dotnet SDK", kind: "command", command: "dotnet", args: []string{"--version"}, probedAt: now})
	jobs = append(jobs, depJob{level: DepLevelDev, name: "Git", kind: "command", command: "git", args: []string{"--version"}, probedAt: now})
	direct, jobs = collectRepoArtifacts(direct, jobs, env.RepoRoot, now)

	return direct, jobs
}

// addPathCommandProbe 追加「路径配置 + 真跑一次读版本」探测（空值即未配置，不回落探测路径）。
func addPathCommandProbe(direct []*DepResult, jobs []depJob, cfg func(string) string, key, level, name string, args []string, now int64) ([]*DepResult, []depJob) {
	path := cfg(key)
	if path == "" {
		return append(direct, makeDepResult(level, name, DepStateUnconfigured, "未配置（"+key+" 空）", now)), jobs
	}
	return direct, append(jobs, depJob{level: level, name: name, kind: "command", command: path, args: args, probedAt: now})
}

// addPathFileVersionProbe 追加「路径配置 + PE 文件版本」探测（版本行不可靠的外部程序用）。
func addPathFileVersionProbe(direct []*DepResult, jobs []depJob, cfg func(string) string, key, level, name string, now int64) ([]*DepResult, []depJob) {
	path := cfg(key)
	if path == "" {
		return append(direct, makeDepResult(level, name, DepStateUnconfigured, "未配置（"+key+" 空）", now)), jobs
	}
	return direct, append(jobs, depJob{level: level, name: name, kind: "filever", paths: []string{path}, probedAt: now})
}

// configPresence 「配置项非空即已配置」探测——可选功能面。
func configPresence(cfg func(string) string, key, level, name string, now int64) *DepResult {
	if cfg(key) == "" {
		return makeDepResult(level, name, DepStateUnconfigured, "未配置（"+key+" 空）", now)
	}
	return makeDepResult(level, name, DepStateOK, "已配置", now)
}

// collectKokoro Kokoro 服务探测——解释器（服务级覆盖优先）+ 入口脚本双双存在即就绪。
func collectKokoro(direct []*DepResult, jobs []depJob, cfg func(string) string, roots []RootPath, now int64) ([]*DepResult, []depJob) {
	script := cfg("py.service.kokoro.script")
	python := cfg("py.service.kokoro.python")
	if python == "" {
		python = cfg("py.python_path")
	}
	if script == "" {
		return append(direct, makeDepResult(DepLevelRequired, "Kokoro TTS", DepStateUnconfigured, "未配置（py.service.kokoro.script 空）", now)), jobs
	}
	full, ok := expandRootPath(script, roots)
	if !ok {
		return append(direct, makeDepResult(DepLevelRequired, "Kokoro TTS", DepStateUnusable, "脚本寻址非法：ROOT_NOT_FOUND", now)), jobs
	}
	if python == "" {
		return append(direct, makeDepResult(DepLevelRequired, "Kokoro TTS", DepStateUnconfigured, "未配置（解释器空——服务与 py.python_path 均未设）", now)), jobs
	}
	return direct, append(jobs, depJob{level: DepLevelRequired, name: "Kokoro TTS", kind: "paths", paths: []string{python, full}, probedAt: now})
}

// collectStdioService stdio 服务实例探测——入口脚本 + 解释器路径存在即可用。
func collectStdioService(direct []*DepResult, jobs []depJob, cfg func(string) string, roots []RootPath, now int64, serviceName, overrideKey, displayName string) ([]*DepResult, []depJob) {
	prefix := "node.service." + serviceName + "."
	script := cfg(prefix + "script")
	exe := cfg(prefix + overrideKey)
	if exe == "" {
		exe = cfg("node.node_path")
	}
	if script == "" {
		return append(direct, makeDepResult(DepLevelRequired, displayName, DepStateUnconfigured, "未配置（"+prefix+"script 空）", now)), jobs
	}
	full, ok := expandRootPath(script, roots)
	if !ok {
		return append(direct, makeDepResult(DepLevelRequired, displayName, DepStateUnusable, "脚本寻址非法：ROOT_NOT_FOUND", now)), jobs
	}
	if exe == "" {
		return append(direct, makeDepResult(DepLevelRequired, displayName, DepStateUnconfigured, "未配置（解释器空——服务覆盖与 node.node_path 均未设）", now)), jobs
	}
	return direct, append(jobs, depJob{level: DepLevelRequired, name: displayName, kind: "paths", paths: []string{exe, full}, probedAt: now})
}

// collectRepoArtifacts 自举链产物探测——按仓库根定位 SetUp.exe / Mau.exe。
func collectRepoArtifacts(direct []*DepResult, jobs []depJob, repoRoot string, now int64) ([]*DepResult, []depJob) {
	if repoRoot == "" {
		return append(direct, makeDepResult(DepLevelDev, "自举链产物", DepStateUnconfigured, "未定位仓库根（无 `mau` 受控根）", now)), jobs
	}
	jobs = append(jobs,
		depJob{level: DepLevelDev, name: "SetUp.exe", kind: "filever", paths: []string{filepath.Join(repoRoot, "SetUp.exe")}, probedAt: now},
		depJob{level: DepLevelDev, name: "Mau.exe", kind: "filever", paths: []string{filepath.Join(repoRoot, "Mau-public", "Mau.exe")}, probedAt: now},
		depJob{level: DepLevelDev, name: "前端测试依赖", kind: "paths", paths: []string{filepath.Join(repoRoot, "CatHome4", "html", "tests", "node_modules")}, probedAt: now},
	)
	return direct, jobs
}

// expandRootPath 展开受控根寻址（id:相对路径）或原样返回绝对路径。
func expandRootPath(raw string, roots []RootPath) (string, bool) {
	colon := strings.IndexByte(raw, ':')
	// 盘符形态（C:\ / C:/）——不是根寻址
	if colon == 1 && len(raw) > 2 && (raw[2] == '\\' || raw[2] == '/') {
		return raw, true
	}
	if colon > 0 && colon < len(raw)-1 && !strings.ContainsAny(raw[:colon], "\\/") {
		id := raw[:colon]
		rest := raw[colon+1:]
		for _, r := range roots {
			if strings.EqualFold(r.ID, id) {
				return filepath.Join(r.Path, filepath.FromSlash(rest)), true
			}
		}
		return "", false
	}
	return raw, true
}

// runCommandProbe 起进程跑一次命令——只看「能否启动 / 是否超时」，不看退出码（与 C# 同口径）。
func runCommandProbe(exe string, args []string, timeout time.Duration) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err == nil {
		return out.String(), true
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// 进程跑起来了但退出码非零——输出仍是有效信息
		return out.String(), true
	}
	return "", false
}

// probeFileVersion 读 PE 文件版本（FileVersion——对齐 C# FileVersionInfo.FileVersion）。
//
// 以目标文件所在目录构造只读临时根表，让 fs 服务的根寻址能接受任意绝对路径
// （探测目标是配置里写死的外部程序，不保证落在受控根内）。
func probeFileVersion(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(abs)
	svc := &fs.Service{Roots: []fs.Root{{ID: "probe", Path: dir, Writable: false}}}
	facts, err := svc.VersionFacts(abs)
	if err != nil {
		// C# FileVersionInfo 对「PE 无版本资源 / 版本事实读不出」返回空串而不抛——
		// 探测面按同一宽容语义处理；只有「不是 PE」才判不可用。
		var toolErr *fs.ToolError
		if errors.As(err, &toolErr) && toolErr.Name == "InvalidDataException" && !strings.HasPrefix(toolErr.Msg, "Not a PE file") {
			return "", true
		}
		return "", false
	}
	return facts.FileVersion, true
}

// firstLine 取首行（去空白）。
func firstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return strings.TrimSpace(trimmed)
}

// isTextClean 判定文本是否可解析——含替换字符（U+FFFD）即视为解码失败。
func isTextClean(text string) bool {
	return !strings.ContainsRune(text, '\uFFFD')
}
