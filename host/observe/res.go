package observe

import (
	"runtime"
	"time"
)

// Res 是快照 res 段——资源占用采集（对齐 C# ResProbe.Capture 的五段结构）。
type Res struct {
	// T 采集时刻（Unix 毫秒）
	T int64 `json:"t"`
	// Proc 本体进程内存
	Proc ProcRes `json:"proc"`
	// Py py 服务行（P3-a 无服务 → 空数组）
	Py []ServiceRes `json:"py"`
	// Node node 服务行（P3-a 无服务 → 空数组）
	Node []ServiceRes `json:"node"`
	// GPU 显存占用
	GPU GPURes `json:"gpu"`
	// Loop 主循环时序（P3-a 无主循环 → 零值）
	Loop LoopRes `json:"loop"`
}

// ProcRes 是本体进程内存（C# 的 ws/private/heap/committed/g0/g1/g2/loh）。
type ProcRes struct {
	// WS 工作集字节（不可读 0）
	WS int64 `json:"ws"`
	// Private 私有字节（不可读 0）
	Private int64 `json:"private"`
	// Heap 托管堆（Go：HeapAlloc）
	Heap int64 `json:"heap"`
	// Committed 已提交（Go：Sys）
	Committed int64 `json:"committed"`
	// G0 第 0 代（Go 无分代 → 0）
	G0 int64 `json:"g0"`
	// G1 第 1 代（Go 无分代 → 0）
	G1 int64 `json:"g1"`
	// G2 第 2 代（Go 无分代 → 0）
	G2 int64 `json:"g2"`
	// LOH 大对象堆（Go 无 LOH → 0）
	LOH int64 `json:"loh"`
}

// ServiceRes 是一条 py / node 服务资源行。
type ServiceRes struct {
	// Name 服务名
	Name string `json:"name"`
	// State 状态
	State string `json:"state"`
	// Alive 是否存活
	Alive bool `json:"alive"`
	// PID 进程 ID
	PID int64 `json:"pid"`
	// WS 工作集字节
	WS int64 `json:"ws"`
}

// GPURes 是 GPU 显存占用（WDDM 下只报全局）。
type GPURes struct {
	// Available 是否可用
	Available bool `json:"available"`
	// UsedMib 已用 MiB
	UsedMib int `json:"usedMib"`
	// TotalMib 总量 MiB
	TotalMib int `json:"totalMib"`
	// Pct 占用百分比（一位小数）
	Pct float64 `json:"pct"`
}

// LoopRes 是主循环时序（C# LoopTiming 的投影形态）。
type LoopRes struct {
	// AvgMs 窗口平均帧耗时
	AvgMs float64 `json:"avgMs"`
	// PeakMs 窗口峰值帧耗时
	PeakMs float64 `json:"peakMs"`
	// Samples 样本数
	Samples int `json:"samples"`
	// BudgetMs 帧预算
	BudgetMs float64 `json:"budgetMs"`
	// OverrunTotal 累计超帧
	OverrunTotal int `json:"overrunTotal"`
	// SecFrames 上一秒帧数
	SecFrames int `json:"secFrames"`
	// SecOverruns 上一秒超帧数
	SecOverruns int `json:"secOverruns"`
	// SecOverrunPct 上一秒超帧占比
	SecOverrunPct float64 `json:"secOverrunPct"`
	// Segments 逐段耗时
	Segments []SegmentRes `json:"segments"`
}

// SegmentRes 是一条逐段耗时。
type SegmentRes struct {
	// Name 段名
	Name string `json:"name"`
	// AvgMs 平均
	AvgMs float64 `json:"avgMs"`
	// PeakMs 峰值
	PeakMs float64 `json:"peakMs"`
}

// CaptureRes 采集资源截面（廉价——只读运行时代数；进程内存走 PSAPI）。
func CaptureRes() Res {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	ws, private := readProcessMemory()
	py := []ServiceRes{}
	node := []ServiceRes{}
	return Res{
		T: time.Now().UnixMilli(),
		Proc: ProcRes{
			WS:        ws,
			Private:   private,
			Heap:      int64(mem.HeapAlloc),
			Committed: int64(mem.Sys),
		},
		Py:   py,
		Node: node,
		GPU:  GPURes{Available: false, UsedMib: 0, TotalMib: 0, Pct: 0},
		Loop: LoopRes{
			Segments: []SegmentRes{},
		},
	}
}
