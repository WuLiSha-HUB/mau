package toolset

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cathome5/mau/graph"
	"cathome5/mau/runtime"
)

// 迁移模式——重载时旧图状态怎么处置。
const (
	// ModeFresh 全新图：状态回初始、私有盒清空、旧工单回收（默认——最安全）
	ModeFresh = "fresh"
	// ModeCarry 尽力延续：同名状态机取值合法则沿用、私有盒全量迁移
	ModeCarry = "carry"
	// ModeDrain 排空后换：导线在途与工单在途归零才换图（工具组用）
	ModeDrain = "drain"
)

// ReloadResult 是一次重载的结果——回执与观测共用同一份事实。
type ReloadResult struct {
	// Group 组名
	Group string
	// Mode 生效的迁移模式
	Mode string
	// Gen 新图代际（替换后 +1；pending = 未生效时为 0）
	Gen int64
	// CorpusHash 语料内容哈希前 8 位（看得见的重载——不是 mtime 而是字节）
	CorpusHash string
	// Wires / States / Boxes 新图规模
	Wires  int
	States int
	Boxes  int
	// Carried carry 实际迁移条数（states + boxes）
	Carried int
	// InFlight 换图前该组在途工单数（open + work）
	InFlight int
	// BusyWires drain 超时时仍在途的导线
	BusyWires []string
	// Registered 是否首次注册（true = 组原先不存在）
	Registered bool
	// Pending 是否登记为等待在途归零（下一帧起由 TickPending 收尾）
	Pending bool
	// Elapsed 本次动作耗时
	Elapsed time.Duration
	// Error 非空即失败——现状未改
	Error string
}

// pendingSwap 是等待在途归零的换图计划（drain 模式）。
type pendingSwap struct {
	group      Group
	hash       string
	mode       string
	registered bool
	oldID      int64
	deadline   time.Time
	enqueued   time.Time
}

// Reloader 是语料组热重载执行面——门禁加载 → 迁移 → 原子换图。
//
// 线程纪律：Reload 必须与 Runner.Tick 串行（主循环内直调，或经 PostToMain 投递）。
// 原因：换图要读写旧图的内部 map（carry 快照），与 Tick 并发是数据竞争。
//
// 三模式共用同一套门禁（LoadGroup）——门禁不过即不产图，调用方保持现状。
type Reloader struct {
	// CorpusDir 语料根（corpus/ch4）
	CorpusDir string
	// BricksIndex 积木索引路径（空 = 只做运行时门禁）
	BricksIndex string
	// Bricks 运行时积木表（与启动期同一份）
	Bricks graph.BrickSet
	// Runner 帧序宿主
	Runner *runtime.Runner
	// OA 工单撮合器（nil = 不做出清）
	OA *runtime.OA
	// Logf 出声回调（nil = 静默）
	Logf func(level int, message string)
	// DrainTimeout drain 等待上限（<=0 取 5s）
	DrainTimeout time.Duration

	mu      sync.Mutex
	gen     map[string]int64
	hash    map[string]string
	pending map[string]*pendingSwap
}

// NewReloader 创建热重载执行面（gen / hash / pending 三表随实例）。
func NewReloader(corpusDir, bricksIndex string, bricks graph.BrickSet, runner *runtime.Runner, oa *runtime.OA, logf func(level int, message string)) *Reloader {
	return &Reloader{
		CorpusDir:   corpusDir,
		BricksIndex: bricksIndex,
		Bricks:      bricks,
		Runner:      runner,
		OA:          oa,
		Logf:        logf,
		gen:         map[string]int64{},
		hash:        map[string]string{},
		pending:     map[string]*pendingSwap{},
	}
}

// Prime 登记启动期已加载组的内容哈希（gen 从 0 起）——重载前观测面即可看到语料代。
func (r *Reloader) Prime(groups []Group) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, group := range groups {
		r.hash[group.Name] = corpusHash(group.Path)
		if _, exists := r.gen[group.Name]; !exists {
			r.gen[group.Name] = 0
		}
	}
}

// Gen 返回某组当前代际。
func (r *Reloader) Gen(name string) int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen[name]
}

// Hash 返回某组当前语料哈希（启动期登记 / 重载后更新）。
func (r *Reloader) Hash(name string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hash[name]
}

// PendingOf 返回某组是否有等待中的换图。
func (r *Reloader) PendingOf(name string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.pending[name]
	return exists
}

// Reload 重载单组语料——门禁 → 迁移 → 换图（drain 遇在途则登记 pending）。
//
// 调用方须与 Tick 串行。返回的 result.Error 非空表示现状未改。
func (r *Reloader) Reload(name, mode string) ReloadResult {
	if r == nil {
		return ReloadResult{Group: name, Mode: mode, Error: "ERR|RELOAD_NO_SERVICE|热重载面未接线"}
	}
	start := time.Now()
	result := ReloadResult{Group: name, Mode: mode}
	if mode == "" {
		mode = ModeFresh
		result.Mode = ModeFresh
	}
	if mode != ModeFresh && mode != ModeCarry && mode != ModeDrain {
		result.Error = "ERR|RELOAD_MODE|未知迁移模式: " + mode + "（fresh|carry|drain）"
		return result
	}
	if r.Runner == nil {
		result.Error = "ERR|RELOAD_NO_RUNNER|帧序宿主未接线"
		return result
	}
	// 1) 门禁加载——三条门禁与启动期同源，失败即不产图
	group, reason := LoadGroup(r.CorpusDir, name, r.BricksIndex, r.Bricks, r.Logf)
	if reason != "" {
		result.Error = "ERR|RELOAD_GATE|" + reason
		return result
	}
	result.CorpusHash = corpusHash(group.Path)
	result.Wires = group.Graph.WireCount()
	result.States = group.Graph.StateCount()
	result.Boxes = group.Graph.BoxCount()

	oldID, existed := r.Runner.FlowID(name)
	result.Registered = !existed
	result.InFlight = r.inFlight(oldID)

	// 2) drain：有在途即登记 pending，由 TickPending 在归零后收尾（主循环内sleep 会卡死排空）
	if mode == ModeDrain && existed && (result.InFlight > 0 || r.busyCount(name) > 0) {
		r.mu.Lock()
		r.pending[name] = &pendingSwap{
			group:      group,
			hash:       result.CorpusHash,
			mode:       mode,
			registered: false,
			oldID:      oldID,
			deadline:   start.Add(r.drainTimeout()),
			enqueued:   start,
		}
		r.mu.Unlock()
		result.Pending = true
		r.report(1, "热重载待换 "+name+"：在途工单 "+strconv.Itoa(result.InFlight)+" / 在途导线 "+strconv.Itoa(r.busyCount(name))+"——归零后自动换图")
		result.Elapsed = time.Since(start)
		return result
	}

	// 3) 立即换图
	swap := &pendingSwap{
		group:      group,
		hash:       result.CorpusHash,
		mode:       mode,
		registered: result.Registered,
		oldID:      oldID,
	}
	r.apply(swap, &result)
	result.Elapsed = time.Since(start)
	return result
}

// TickPending 推进 pending 换图——主循环每帧调用（Runner.Tick 之后）。
//
// 判据：该组导线全部回投 + 该组工单在途归零 → 换图；超时则强制换并出声（不静默卡住）。
func (r *Reloader) TickPending() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return
	}
	names := make([]string, 0, len(r.pending))
	for name := range r.pending {
		names = append(names, name)
	}
	r.mu.Unlock()

	now := time.Now()
	for _, name := range names {
		r.mu.Lock()
		swap, exists := r.pending[name]
		r.mu.Unlock()
		if !exists {
			continue
		}
		busy := r.busyCount(name)
		inFlight := r.inFlight(swap.oldID)
		if busy > 0 || inFlight > 0 {
			if now.Before(swap.deadline) {
				continue
			}
			r.report(2, "热重载 drain 超时 "+name+"：在途工单 "+strconv.Itoa(inFlight)+" / 在途导线 "+strconv.Itoa(busy)+"——强制换图（旧结果进旧图，不回投新图）")
		}
		r.mu.Lock()
		delete(r.pending, name)
		r.mu.Unlock()
		result := ReloadResult{Group: name, Mode: swap.mode, CorpusHash: swap.hash, Pending: true}
		result.Wires = swap.group.Graph.WireCount()
		result.States = swap.group.Graph.StateCount()
		result.Boxes = swap.group.Graph.BoxCount()
		result.InFlight = inFlight
		r.apply(swap, &result)
		r.report(1, "热重载已换 "+name+"：gen="+strconv.FormatInt(result.Gen, 10)+" hash="+swap.hash+"（pending 收尾）")
	}
}

// FlowsReport 生成 host-flows 文本——注册表 + 语料代 + 在途 + pending。
func (r *Reloader) FlowsReport() string {
	if r == nil || r.Runner == nil {
		return "ERR|FLOWS_NO_SERVICE|帧序宿主未接线"
	}
	entries := r.Runner.Entries()
	var builder strings.Builder
	pendingCount := 0
	if r != nil {
		r.mu.Lock()
		pendingCount = len(r.pending)
		r.mu.Unlock()
	}
	builder.WriteString("FLOWS|组 ")
	builder.WriteString(strconv.Itoa(len(entries)))
	builder.WriteString(" | pending ")
	builder.WriteString(strconv.Itoa(pendingCount))
	for _, entry := range entries {
		gen := r.Gen(entry.Name)
		hash := r.Hash(entry.Name)
		if hash == "" {
			hash = "-"
		}
		inFlight := r.inFlight(entry.ID)
		pending := "-"
		if r.PendingOf(entry.Name) {
			pending = "wait"
		}
		// 规模取图的真实计数（不是状态副本长度——换图后到下一帧前副本为空，会误导）
		wires := len(entry.Status.Wires)
		states := len(entry.Status.StateLines)
		if flow, exists := r.Runner.FlowObject(entry.Name); exists {
			if typed, isGraph := flow.(*graph.Graph); isGraph {
				wires = typed.WireCount()
				states = typed.StateCount()
			}
		}
		builder.WriteString("\n")
		builder.WriteString(strconv.FormatInt(entry.ID, 10))
		builder.WriteString(". ")
		builder.WriteString(entry.Name)
		builder.WriteString(" | gen=")
		builder.WriteString(strconv.FormatInt(gen, 10))
		builder.WriteString(" | hash=")
		builder.WriteString(hash)
		builder.WriteString(" | wires=")
		builder.WriteString(strconv.Itoa(wires))
		builder.WriteString(" | states=")
		builder.WriteString(strconv.Itoa(states))
		builder.WriteString(" | inFlight=")
		builder.WriteString(strconv.Itoa(inFlight))
		builder.WriteString(" | pending=")
		builder.WriteString(pending)
	}
	return builder.String()
}

// apply 执行换图（调用方已与 Tick 串行）。
func (r *Reloader) apply(swap *pendingSwap, result *ReloadResult) {
	name := swap.group.Name
	carried := 0
	// carry：从旧图快照迁移（旧图不存在时无源可迁）
	if swap.mode == ModeCarry {
		if old, ok := r.Runner.FlowObject(name); ok {
			if oldGraph, isGraph := old.(*graph.Graph); isGraph {
				carried += swap.group.Graph.RestoreStates(oldGraph.SnapshotStates())
				carried += swap.group.Graph.RestoreBoxes(oldGraph.SnapshotBoxes())
			}
		}
	}
	result.Carried = carried

	r.mu.Lock()
	gen := r.gen[name] + 1
	r.gen[name] = gen
	r.hash[name] = swap.hash
	r.mu.Unlock()
	swap.group.Graph.SetGen(gen)
	result.Gen = gen

	if swap.registered {
		id := r.Runner.RegisterFlow(swap.group.Graph, name)
		result.Registered = true
		r.report(1, "热重载注册新组 "+name+"：id="+strconv.FormatInt(id, 10)+" gen="+strconv.FormatInt(gen, 10))
		return
	}
	id, ok := r.Runner.ReplaceFlow(name, swap.group.Graph)
	if !ok {
		// 组在门禁与换图之间消失（并发摘除）——按新增补注册，不静默丢
		id = r.Runner.RegisterFlow(swap.group.Graph, name)
		result.Registered = true
		r.report(2, "热重载发现组已摘除 "+name+"：按新增补注册 id="+strconv.FormatInt(id, 10))
	}
	// fresh / drain 回收旧单（语义已失效——新图无从认领）；carry 保留在途单续认领
	if swap.mode != ModeCarry && r.OA != nil {
		if removed := r.OA.RemoveByOwner(swap.oldID); removed > 0 {
			r.report(1, "热重载回收旧工单 "+name+"："+strconv.Itoa(removed)+" 张（挂单方 #"+strconv.FormatInt(swap.oldID, 10)+"）")
		}
	}
}

// inFlight 返回该组在途工单数（open + work）；OA 未接线回 0。
func (r *Reloader) inFlight(flowID int64) int {
	if r == nil || r.OA == nil || flowID == 0 {
		return 0
	}
	open, work := r.OA.InFlightByOwner(flowID)
	return open + work
}

// busyCount 返回该组在途导线数。
func (r *Reloader) busyCount(name string) int {
	if r == nil || r.Runner == nil {
		return 0
	}
	flow, ok := r.Runner.FlowObject(name)
	if !ok {
		return 0
	}
	typed, isGraph := flow.(*graph.Graph)
	if !isGraph {
		return 0
	}
	return len(typed.BusyWires())
}

// drainTimeout 等待上限（<=0 取 5s）。
func (r *Reloader) drainTimeout() time.Duration {
	if r.DrainTimeout > 0 {
		return r.DrainTimeout
	}
	return 5 * time.Second
}

// report 出声（nil 安全）。
func (r *Reloader) report(level int, message string) {
	if r != nil && r.Logf != nil {
		r.Logf(level, message)
	}
}

// CorpusHash 计算某组语料当前内容哈希（不产图）——开发态监听的轻量探针。
//
// 读不到语料回空串（调用方按「未变化」处置，不臆造一个哈希）。
func CorpusHash(corpusDir, name string) string {
	return corpusHash(findCorpusFile(filepath.Join(corpusDir, name)))
}

// corpusHash 语料内容哈希前 8 位（读不到回空——不臆造）。
func corpusHash(path string) string {
	if path == "" {
		return ""
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:4])
}
