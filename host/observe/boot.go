package observe

import (
	"strings"
	"sync"
	"time"
)

// 降级类别常量——与 C# StartupDegradation 同名同义。
const (
	// KindConfig 配置错误
	KindConfig = "config"
	// KindEnv 外部环境（路径 / 权限 / 端口 / 磁盘）
	KindEnv = "env"
	// KindData 数据文件损坏
	KindData = "data"
	// KindExternal 外部服务（QQ / LLM / 浏览器内核）
	KindExternal = "external"
	// KindInternal 内部一致性缺陷（ERROR 级——不得当作环境问题忽略）
	KindInternal = "internal"
)

// 降级形态常量。
const (
	// StateUnavailable 能力不提供
	StateUnavailable = "unavailable"
	// StateReadonly 读面可用写面拒绝
	StateReadonly = "readonly"
	// StateEmpty 按空数据继续
	StateEmpty = "empty"
	// StateFallback 换端口 / 换落点
	StateFallback = "fallback"
	// StateRebuilt 缺目录 / 缺文件补建默认
	StateRebuilt = "rebuilt"
)

// Degradation 是一条启动降级条目（C# StartupDegradeEntry 九字段对齐）。
type Degradation struct {
	// Code 机器码（大写下划线，如 ROOT_UNREACHABLE / CFG_CORRUPT）
	Code string `json:"code"`
	// Kind 类别（config / env / data / external / internal）
	Kind string `json:"kind"`
	// Ability 受影响能力
	Ability string `json:"ability"`
	// State 形态（unavailable / readonly / empty / fallback / rebuilt）
	State string `json:"state"`
	// Detail 人读原因（含具体对象）
	Detail string `json:"detail"`
	// Fix 修复指引
	Fix string `json:"fix"`
	// Stage 启动段名（S0..S7）
	Stage string `json:"stage"`
	// At 登记时刻（yyyy-MM-dd HH:mm:ss）
	At string `json:"at"`
	// Count 同一 Code + Ability 的累计次数
	Count int `json:"count"`
}

// BootSegment 是快照 boot 段。
type BootSegment struct {
	// OK 是否零降级
	OK bool `json:"ok"`
	// Count 降级条目数
	Count int `json:"count"`
	// Summary 摘要行（空 = 无降级）
	Summary string `json:"summary"`
	// Sink 早期诊断 sink 落点（空 = 仅内存）
	Sink string `json:"sink"`
	// ReportPath 启动报告路径（空 = 未落盘）
	ReportPath string `json:"reportPath"`
	// Degraded 降级条目（按登记顺序）
	Degraded []Degradation `json:"degraded"`
}

// BootRegistry 是启动降级登记表——启动链唯一的降级记账点。
//
// 写者：启动段与降级点（Report）；读者：快照 boot 段 / 日志出口 / 启动提示。
// 去重口径：Code + Ability 相同者累加 Count，不重复列条（详情保留首条）。
type BootRegistry struct {
	mu      sync.Mutex
	entries []Degradation
	sink    string
	report  string
}

// NewBootRegistry 创建空登记表。
func NewBootRegistry() *BootRegistry {
	return &BootRegistry{}
}

// Report 登记一条降级——同 Code + Ability 已存在时只累加次数并刷新时刻。
func (r *BootRegistry) Report(code, kind, ability, state, detail, fix, stage string) Degradation {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().Format("2006-01-02 15:04:05")
	for i := range r.entries {
		if r.entries[i].Code == code && r.entries[i].Ability == ability {
			r.entries[i].Count++
			r.entries[i].At = now
			return r.entries[i]
		}
	}
	entry := Degradation{
		Code:    code,
		Kind:    kind,
		Ability: ability,
		State:   state,
		Detail:  detail,
		Fix:     fix,
		Stage:   stage,
		At:      now,
		Count:   1,
	}
	r.entries = append(r.entries, entry)
	return entry
}

// Snapshot 取当前登记表副本（按登记顺序）。
func (r *BootRegistry) Snapshot() []Degradation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Degradation, len(r.entries))
	copy(out, r.entries)
	return out
}

// Count 当前降级条目数。
func (r *BootRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// HasAny 是否存在降级条目。
func (r *BootRegistry) HasAny() bool {
	return r.Count() > 0
}

// SetSink 设置早期诊断 sink 落点（展示用）。
func (r *BootRegistry) SetSink(sink string) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
}

// SetReportPath 设置启动报告路径（展示用）。
func (r *BootRegistry) SetReportPath(path string) {
	r.mu.Lock()
	r.report = path
	r.mu.Unlock()
}

// BuildSummaryLine 摘要行：`启动降级 N 项：CODE1, CODE2×2`（无降级为空串）。
func (r *BootRegistry) BuildSummaryLine() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("启动降级 ")
	b.WriteString(itoa(len(r.entries)))
	b.WriteString(" 项：")
	for i := range r.entries {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(r.entries[i].Code)
		if r.entries[i].Count > 1 {
			b.WriteString("×")
			b.WriteString(itoa(r.entries[i].Count))
		}
	}
	return b.String()
}

// Segment 构建快照 boot 段。
func (r *BootRegistry) Segment() BootSegment {
	entries := r.Snapshot()
	r.mu.Lock()
	sink, report := r.sink, r.report
	r.mu.Unlock()
	return BootSegment{
		OK:         len(entries) == 0,
		Count:      len(entries),
		Summary:    r.BuildSummaryLine(),
		Sink:       sink,
		ReportPath: report,
		Degraded:   entries,
	}
}
