package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DelayEntry 是延迟条目——内容 + 绝对墙钟时间戳 + 来源（design-ch4-delay §二）。
//
// 排序键 = DueAt 升序（同刻按 CreatedAt / Id 序）——批量到点时保序注入。
// 字段与 C# `DelayQueue.cs` 的 DelayEntry 一一对应（落盘键序随字段序）。
type DelayEntry struct {
	// ID 条目序号——进程内自增（跨会话唯一；前端编辑 / 取消引用）
	ID int64 `json:"id"`
	// CatKey 归属会话——猫 key（会话标识 ≡ 猫 key）
	CatKey string `json:"cat"`
	// Content 注入内容（原样文本，不做解析）
	Content string `json:"content"`
	// DueAt 触发时刻——Unix 毫秒（UTC；绝对墙钟，不是内部倒计时计数）
	DueAt int64 `json:"dueAt"`
	// CreatedAt 登记时刻——Unix 毫秒（观测与同刻排序用）
	CreatedAt int64 `json:"createdAt"`
	// Source 来源标记——delay（人工定时）/ sleep（LLM 等待）/ timer（LLM 排程）/ restart（宿主重启回执）
	Source string `json:"source"`
	// Loop 循环标记——true=触发后按「投递时刻 + 时长」重排回表（不出表）；false=一次性（触发即移除）
	Loop bool `json:"loop"`
	// IntervalMs 循环时长——Unix 毫秒（登记时的相对长度；重排基准 = 投递时刻 + 本值）
	IntervalMs int64 `json:"intervalMs"`
	// Fired 已触发次数——循环条目累计（观测面「已响 N 次」；一次性条目恒 0）
	Fired int `json:"fired"`
}

// 延迟队列常量——对齐 C# `DelayQueue` 的三个 public const（timer / delay 指令族共用）。
const (
	// DelayMaxPerSession 单会话条目上限——超出拒绝（防 LLM 反复登记堆积）
	DelayMaxPerSession = 50
	// DelayMinSleepSeconds 时长下限（秒）——0 无意义（等于立即注入）
	DelayMinSleepSeconds = 1
	// DelayMaxSleepSeconds 时长上限（秒）——LLM 自主挂起防呆；人工登记不受此限
	DelayMaxSleepSeconds = 3600
)

// DelayQueue 是延迟指令队列——定时表 + 到点转移（design-ch4-delay §三 / §四）。
//
// 两段式：本表（未到点）→ 到点按序逐条转入会话即时队（PostSystemMessage）——用户插话不被未到点条目阻塞。
// 时间基准 = 绝对墙钟时间戳（Unix 毫秒）：每帧比较 now >= DueAt，不累加帧计数（无漂移）。
// 落盘：<dataRoot>/runtime/delays.json——变更即原子落盘；条目跨宿主重启保留。
//
// 与 C# 的机制差异（行为等价）：C# 为静态类 + 全局表；Go 侧单会话形态，一个宿主一个实例挂在会话上。
// 线程安全：全部操作锁内完成（HTTP 线程 / 主线程 / 后台线程低频访问）。
type DelayQueue struct {
	// mu 保护 entries / nextID / storePath
	mu sync.Mutex
	// entries 定时表——按 DueAt 升序维护（登记即有序插入）
	entries []*DelayEntry
	// nextID 条目序号——进程内单调自增
	nextID int64
	// storePath 落盘路径（空 = 不落盘，仅内存）
	storePath string
	// now 时钟（可替换——测试注入假时钟；默认本机 UTC 墙钟毫秒）
	now func() int64
}

// NewDelayQueue 建立延迟队列（storePath 空 = 仅内存）。
func NewDelayQueue(storePath string) *DelayQueue {
	return &DelayQueue{nextID: 1, storePath: storePath, now: unixMillis}
}

// Configure 重设落盘路径（宿主启动期注入；空 = 仅内存）。
func (q *DelayQueue) Configure(storePath string) {
	q.mu.Lock()
	q.storePath = storePath
	q.mu.Unlock()
}

// SetNowProvider 替换时钟（测试注入假时钟；nil = 恢复系统时钟）。
func (q *DelayQueue) SetNowProvider(fn func() int64) {
	q.mu.Lock()
	if fn == nil {
		q.now = unixMillis
	} else {
		q.now = fn
	}
	q.mu.Unlock()
}

// Now 当前时刻——Unix 毫秒（UTC）。
func (q *DelayQueue) Now() int64 {
	q.mu.Lock()
	fn := q.now
	q.mu.Unlock()
	if fn == nil {
		return unixMillis()
	}
	return fn()
}

// FormatDelayTime 时刻文本——本机时区可读形态（日志与列表用）。
func FormatDelayTime(unixMs int64) string {
	return time.UnixMilli(unixMs).Local().Format("2006-01-02 15:04:05")
}

// unixMillis 系统时钟——Unix 毫秒（UTC）。
func unixMillis() int64 {
	return time.Now().UnixMilli()
}

// Add 登记条目——调用方负责把相对时长 / 绝对时刻换算为 dueAt。
//
// 返回结果 JSON（ok/id/dueAt/loop/due；失败 ERR| 前缀）——与 C# `DelayQueue.Add` 逐字同形。
func (q *DelayQueue) Add(catKey, content, source string, dueAt, intervalMs int64, loop bool) string {
	if catKey == "" {
		return "ERR|DELAY_NO_CAT|延迟条目缺少归属会话"
	}
	if content == "" {
		return "ERR|BAD_ARGS|延迟条目内容为空"
	}
	if dueAt <= 0 {
		return "ERR|BAD_ARGS|延迟条目触发时刻非法"
	}
	useSource := source
	if useSource == "" {
		useSource = "delay"
	}
	q.mu.Lock()
	owned := 0
	for _, entry := range q.entries {
		if entry.CatKey == catKey {
			owned++
		}
	}
	if owned >= DelayMaxPerSession {
		q.mu.Unlock()
		logAt(LogModuleHost, 2, "延迟条目登记被拒（上限）: cat="+catKey+" | 已达 "+itoa(DelayMaxPerSession)+" 条", "DELAY")
		return "ERR|DELAY_LIMIT|本会话待触发条目已达上限 " + itoa(DelayMaxPerSession) + "（可取消后再登记）"
	}
	entry := &DelayEntry{
		ID:         q.nextID,
		CatKey:     catKey,
		Content:    content,
		DueAt:      dueAt,
		CreatedAt:  q.nowLocked(),
		Source:     useSource,
		Loop:       loop && intervalMs > 0,
		IntervalMs: intervalMs,
	}
	q.nextID++
	q.insertSortedLocked(entry)
	q.saveLocked()
	loopNote := ""
	if entry.Loop {
		loopNote = " | loop=" + itoa64(intervalMs) + "ms"
	}
	q.mu.Unlock()
	logAt(LogModuleHost, 1, "延迟条目登记: id=#"+itoa64(entry.ID)+" | cat="+catKey+" | due="+FormatDelayTime(dueAt)+" | source="+useSource+loopNote, "DELAY")
	loopText := "false"
	if entry.Loop {
		loopText = "true"
	}
	return `{"ok":true,"id":` + itoa64(entry.ID) +
		`,"dueAt":` + itoa64(dueAt) +
		`,"loop":` + loopText +
		`,"due":` + jsonStr(FormatDelayTime(dueAt)) + `}`
}

// Pump 帧泵——摘出到点条目 → 按序投递（锁外）→ 未受理的重新入表。
//
// 积压语义：多个到点条目按序一次性立即注入（不丢、不合并、不覆盖）。
// loop 语义：投递成功且 Loop=true → 按「投递时刻 + IntervalMs」重排回表（不出表；Fired 累计）。
// deliver 返回 true=已受理 / false=未受理（保留待重投）。返回本次投递条数。
func (q *DelayQueue) Pump(deliver func(catKey, content, source string) bool) int {
	if deliver == nil {
		return 0
	}
	q.mu.Lock()
	if len(q.entries) == 0 {
		q.mu.Unlock()
		return 0
	}
	now := q.nowLocked()
	due := make([]*DelayEntry, 0, 2)
	kept := make([]*DelayEntry, 0, len(q.entries))
	for _, entry := range q.entries {
		if entry.DueAt <= now {
			due = append(due, entry)
			continue
		}
		kept = append(kept, entry)
	}
	q.entries = kept
	q.mu.Unlock()
	if len(due) == 0 {
		return 0
	}
	delivered := 0
	rejected := make([]*DelayEntry, 0, 1)
	repeated := make([]*DelayEntry, 0, 1)
	for _, entry := range due {
		if !deliver(entry.CatKey, entry.Content, entry.Source) {
			rejected = append(rejected, entry)
			continue
		}
		delivered++
		logAt(LogModuleHost, 1, "延迟条目到点投递: id=#"+itoa64(entry.ID)+" | cat="+entry.CatKey+" | source="+entry.Source, "DELAY")
		if !entry.Loop {
			continue
		}
		if entry.IntervalMs > 0 {
			entry.Fired++
			entry.DueAt = q.Now() + entry.IntervalMs
			repeated = append(repeated, entry)
			logAt(LogModuleHost, 1, "延迟条目循环重排: id=#"+itoa64(entry.ID)+" | next="+FormatDelayTime(entry.DueAt)+" | 已响 "+itoa(entry.Fired)+" 次", "DELAY")
			continue
		}
		logAt(LogModuleHost, 2, "延迟条目循环重排跳过（缺循环时长）: id=#"+itoa64(entry.ID), "DELAY")
	}
	q.mu.Lock()
	for _, entry := range rejected {
		q.insertSortedLocked(entry)
	}
	for _, entry := range repeated {
		q.insertSortedLocked(entry)
	}
	if delivered > 0 || len(rejected) > 0 || len(repeated) > 0 {
		q.saveLocked()
	}
	q.mu.Unlock()
	return delivered
}

// Load 加载落盘条目——宿主启动期调用（跨重启保留）。
//
// 文件缺失静默跳过；损坏出声并按空表处理（不抛）。
func (q *DelayQueue) Load() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.storePath == "" {
		return
	}
	raw, err := os.ReadFile(q.storePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logAt(LogModuleHost, 2, "延迟队列加载失败（按空表处理）: "+err.Error(), "DELAY")
		}
		return
	}
	var doc struct {
		Version int          `json:"version"`
		NextID  int64        `json:"nextId"`
		Entries []DelayEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		logAt(LogModuleHost, 2, "延迟队列加载失败（按空表处理）: "+err.Error(), "DELAY")
		return
	}
	q.entries = nil
	for i := range doc.Entries {
		entry := doc.Entries[i]
		if entry.ID <= 0 || entry.CatKey == "" {
			logAt(LogModuleHost, 2, "延迟队列坏条目已跳过: id=#"+itoa64(entry.ID), "DELAY")
			continue
		}
		copied := entry
		q.insertSortedLocked(&copied)
	}
	if doc.NextID > q.nextID {
		q.nextID = doc.NextID
	}
	if len(q.entries) > 0 {
		logAt(LogModuleHost, 1, "延迟队列加载: "+itoa(len(q.entries))+" 条（跨重启保留）", "DELAY")
	}
}

// BuildEntriesFragment 待触发条目数组 JSON——视图出口 state 段 delay 字段数据源。
//
// 八字段（id / content / dueAt / createdAt / source / loop / intervalMs / fired），空表 = []。
func (q *DelayQueue) BuildEntriesFragment(catKey string) string {
	if q == nil {
		return "[]"
	}
	q.mu.Lock()
	list := make([]DelayEntry, 0, len(q.entries))
	for _, entry := range q.entries {
		if entry.CatKey == catKey {
			list = append(list, *entry)
		}
	}
	q.mu.Unlock()
	var b strings.Builder
	b.WriteString("[")
	for i := range list {
		if i > 0 {
			b.WriteString(",")
		}
		loopText := "false"
		if list[i].Loop {
			loopText = "true"
		}
		b.WriteString(`{"id":` + itoa64(list[i].ID) +
			`,"content":` + jsonStr(list[i].Content) +
			`,"dueAt":` + itoa64(list[i].DueAt) +
			`,"createdAt":` + itoa64(list[i].CreatedAt) +
			`,"source":` + jsonStr(list[i].Source) +
			`,"loop":` + loopText +
			`,"intervalMs":` + itoa64(list[i].IntervalMs) +
			`,"fired":` + itoa(list[i].Fired) + `}`)
	}
	b.WriteString("]")
	return b.String()
}

// nowLocked 取时钟（调用方持锁）。
func (q *DelayQueue) nowLocked() int64 {
	if q.now == nil {
		return unixMillis()
	}
	return q.now()
}

// insertSortedLocked 有序插入——DueAt 升序；同刻按 CreatedAt / ID 升序（调用方持锁）。
func (q *DelayQueue) insertSortedLocked(entry *DelayEntry) {
	idx := len(q.entries)
	for i, item := range q.entries {
		if entry.DueAt < item.DueAt ||
			(entry.DueAt == item.DueAt && entry.CreatedAt < item.CreatedAt) ||
			(entry.DueAt == item.DueAt && entry.CreatedAt == item.CreatedAt && entry.ID < item.ID) {
			idx = i
			break
		}
	}
	q.entries = append(q.entries, nil)
	copy(q.entries[idx+1:], q.entries[idx:])
	q.entries[idx] = entry
}

// saveLocked 落盘——原子写（临时文件 + 覆盖移动）；失败出声不抛（调用方持锁）。
//
// 落盘格式：{"version":1,"nextId":N,"entries":[{id,cat,content,dueAt,createdAt,source,loop,intervalMs,fired}]}。
func (q *DelayQueue) saveLocked() {
	if q.storePath == "" {
		return
	}
	var b strings.Builder
	b.WriteString(`{"version":1,"nextId":`)
	b.WriteString(itoa64(q.nextID))
	b.WriteString(`,"entries":[`)
	for i, entry := range q.entries {
		if i > 0 {
			b.WriteString(",")
		}
		loopText := "false"
		if entry.Loop {
			loopText = "true"
		}
		b.WriteString(`{"id":` + itoa64(entry.ID) +
			`,"cat":` + jsonStr(entry.CatKey) +
			`,"content":` + jsonStr(entry.Content) +
			`,"dueAt":` + itoa64(entry.DueAt) +
			`,"createdAt":` + itoa64(entry.CreatedAt) +
			`,"source":` + jsonStr(entry.Source) +
			`,"loop":` + loopText +
			`,"intervalMs":` + itoa64(entry.IntervalMs) +
			`,"fired":` + itoa(entry.Fired) + `}`)
	}
	b.WriteString("]}")
	if err := os.MkdirAll(filepath.Dir(q.storePath), 0o755); err != nil {
		logAt(LogModuleHost, 3, "延迟队列落盘失败: "+err.Error(), "DELAY")
		return
	}
	tmp := q.storePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		logAt(LogModuleHost, 3, "延迟队列落盘失败: "+err.Error(), "DELAY")
		return
	}
	if err := os.Rename(tmp, q.storePath); err != nil {
		os.Remove(tmp)
		logAt(LogModuleHost, 3, "延迟队列落盘失败: "+err.Error(), "DELAY")
	}
}
