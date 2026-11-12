package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// TimebackScopeRecord 是一次回收的元数据（落归档文件首行）。
type TimebackScopeRecord struct {
	// ID 全局作用域编号——跨猫 / 跨会话 / 跨重启永久递增
	ID int64
	// CatKey 归属猫 key
	CatKey string
	// Type 域类型
	Type string
	// Purpose 用途标签
	Purpose string
	// Anchor 锚点消息索引（start 的 tool_calls 声明）
	Anchor int
	// StartAt 开锚时刻（Unix 毫秒）
	StartAt int64
	// BackAt 回收时刻（Unix 毫秒）
	BackAt int64
	// Seconds 作用域存活时长（秒）
	Seconds int64
	// N 实际删除的前文条数
	N int
	// KeptUsers 域内人工输入保留条数
	KeptUsers int
	// Tokens 回收时已知前文长度（请求级真实 usage 值）
	Tokens int64
	// Grew 作用域净增（Tokens − 开锚快照）
	Grew int64
	// Released 释放条数预算（back 回执给出的值——与实际 N 对账）
	Released int
	// Findings 带回载荷全文
	Findings string
	// Writes 本域工具台账（工具名 · 目标标识 · 成败）
	Writes []string
}

// TimebackArchive 是 timeback 归档——全局计数 + 每次回收一个作用域文件。
//
// 落点：<dir>/
//
//	count.json            全局计数（跨猫 / 跨重启；{"count":N}）
//	<id>-<时间戳>.jsonl   一次回收一个文件——首行 meta + 次行该猫 info 快照 + 其后逐行被删前文消息
//
// 取号：读 count.json → +1 → 原子写回（进程内锁串行；不扫归档文件）。
// 写面：失败记 ERR + LastWriteFailed（实例）/ LastWriteFailedAny（进程内）——失败不阻断回收，但必须可见。
type TimebackArchive struct {
	// mu 取号锁（多猫共享全局计数）
	mu sync.Mutex
	// dir 归档目录
	dir string
	// lastWriteFailed 最近一次写面结果（本实例）
	lastWriteFailed bool
}

// archiveLastWriteFailedAny 进程内最近一次写面失败标志（跨实例；info 观测面数据源）。
var archiveLastWriteFailedAny bool

// NewTimebackArchive 建立归档实例（目录与计数文件按需创建——首次写入时建目录）。
func NewTimebackArchive(dir string) *TimebackArchive {
	return &TimebackArchive{dir: dir}
}

// LastWriteFailed 写面可用性（本实例）。
func (a *TimebackArchive) LastWriteFailed() bool { return a.lastWriteFailed }

// LastWriteFailedAny 写面可用性（进程内，跨实例）。
func TimebackArchiveLastWriteFailedAny() bool { return archiveLastWriteFailedAny }

// countPath 计数文件路径。
func (a *TimebackArchive) countPath() string { return filepath.Join(a.dir, "count.json") }

// NextID 取下一个编号——全局永久递增（跨猫 / 跨会话 / 跨重启）。
//
// 读失败按 0 起算并出声。
func (a *TimebackArchive) NextID() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.readCount()
	next := current + 1
	a.writeCount(next)
	return next
}

// readCount 读全局计数——文件缺失 / 结构不符 / 读取失败按 0 起算（出声，不静默）。
func (a *TimebackArchive) readCount() int64 {
	if a.dir == "" {
		return 0
	}
	raw, err := os.ReadFile(a.countPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		logAt(LogModuleHost, 3, "timeback 计数读取失败（按 0 起算）: "+err.Error(), LogCategoryTimeback)
		return 0
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		logAt(LogModuleHost, 2, "timeback 计数文件结构不符（按 0 起算）: "+a.countPath(), LogCategoryTimeback)
		return 0
	}
	value, exists := obj["count"]
	if !exists {
		logAt(LogModuleHost, 2, "timeback 计数文件缺 count 字段（按 0 起算）: "+a.countPath(), LogCategoryTimeback)
		return 0
	}
	var count int64
	if err := json.Unmarshal(value, &count); err != nil {
		logAt(LogModuleHost, 2, "timeback 计数文件结构不符（按 0 起算）: "+a.countPath(), LogCategoryTimeback)
		return 0
	}
	return count
}

// writeCount 写全局计数——原子写（临时文件 + 替换）。
func (a *TimebackArchive) writeCount(value int64) bool {
	if err := a.ensureDir(); err != nil {
		a.markWriteFailed("timeback 计数写入失败（编号可能重号）: " + err.Error())
		return false
	}
	body := `{"count":` + itoa64(value) + `}`
	tmp := a.countPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		a.markWriteFailed("timeback 计数写入失败（编号可能重号）: " + err.Error())
		return false
	}
	if err := os.Rename(tmp, a.countPath()); err != nil {
		os.Remove(tmp)
		a.markWriteFailed("timeback 计数写入失败（编号可能重号）: " + err.Error())
		return false
	}
	archiveLastWriteFailedAny = false
	return true
}

// WriteScopeFile 写一次回收的作用域文件——首行 meta + 次行 info 快照 + 其后被删前文消息。
//
// 原子写（临时文件 + 替换）；目标同名时改用后缀（防覆盖）。返回落盘路径与成败。
func (a *TimebackArchive) WriteScopeFile(record *TimebackScopeRecord, infoJSON string, removed []Message) (string, bool) {
	if record == nil {
		a.markWriteFailed("timeback 归档写入失败（记录为空）")
		return "", false
	}
	if err := a.ensureDir(); err != nil {
		a.markWriteFailed("timeback 归档写入失败（本次未落档）: " + err.Error())
		return "", false
	}
	var b strings.Builder
	b.WriteString(buildTimebackMetaLine(record))
	b.WriteByte('\n')
	if strings.TrimSpace(infoJSON) != "" {
		b.WriteString(normalizeJSONLine(infoJSON))
		b.WriteByte('\n')
	}
	for _, msg := range removed {
		b.WriteString(BuildMessageLine(msg))
		b.WriteByte('\n')
	}
	target := a.resolveTargetPath(record)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		a.markWriteFailed("timeback 归档写入失败（本次未落档）: " + err.Error())
		return "", false
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		a.markWriteFailed("timeback 归档写入失败（本次未落档）: " + err.Error())
		return "", false
	}
	a.lastWriteFailed = false
	archiveLastWriteFailedAny = false
	return target, true
}

// ReadRecentMeta 读面——最近若干次回收的首行 meta（按文件名编号排序取末尾 max 个；坏行跳过）。
func (a *TimebackArchive) ReadRecentMeta(max int) []string {
	if max <= 0 || a.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			logAt(LogModuleHost, 3, "timeback 归档目录读取失败（读面不可用）: "+err.Error(), LogCategoryTimeback)
		}
		return nil
	}
	type named struct {
		name string
		id   int64
	}
	ordered := make([]named, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := parseTimebackIDPrefix(entry.Name())
		if id <= 0 {
			continue
		}
		ordered = append(ordered, named{name: entry.Name(), id: id})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].id != ordered[j].id {
			return ordered[i].id < ordered[j].id
		}
		return ordered[i].name < ordered[j].name
	})
	start := len(ordered) - max
	if start < 0 {
		start = 0
	}
	result := make([]string, 0, max)
	for _, item := range ordered[start:] {
		if line := readFirstLine(filepath.Join(a.dir, item.name)); line != "" {
			result = append(result, line)
		}
	}
	return result
}

// parseTimebackIDPrefix 解析文件名编号前缀——<id>-<时间戳>.jsonl（含防覆盖后缀）取首个分隔符之前部分。
func parseTimebackIDPrefix(fileName string) int64 {
	head := fileName
	if dash := strings.Index(fileName, "-"); dash > 0 {
		head = fileName[:dash]
	}
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// readFirstLine 读文件首行（只读一行即止；读取失败 / 空文件 = 空串）。
func readFirstLine(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		logAt(LogModuleHost, 3, "timeback 归档首行读取失败: "+err.Error(), LogCategoryTimeback)
		return ""
	}
	text := string(raw)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}

// resolveTargetPath 目标文件路径——<id>-<时间戳>.jsonl；同名已存在时改后缀（防覆盖，出声）。
func (a *TimebackArchive) resolveTargetPath(record *TimebackScopeRecord) string {
	name := itoa64(record.ID) + "-" + itoa64(record.BackAt)
	target := filepath.Join(a.dir, name+".jsonl")
	suffix := 2
	for {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return target
		}
		logAt(LogModuleHost, 2, "timeback 归档文件名冲突——改用后缀避免覆盖: "+filepath.Base(target), LogCategoryTimeback)
		target = filepath.Join(a.dir, name+"-"+strconv.Itoa(suffix)+".jsonl")
		suffix++
	}
}

// ensureDir 确保归档目录存在。
func (a *TimebackArchive) ensureDir() error {
	if a.dir == "" {
		return nil
	}
	return os.MkdirAll(a.dir, 0o755)
}

// markWriteFailed 标记写面失败——记 ERR + 实例标志 + 进程内标志（失败必须可见，不阻断回收）。
func (a *TimebackArchive) markWriteFailed(message string) {
	a.lastWriteFailed = true
	archiveLastWriteFailedAny = true
	logAt(LogModuleHost, 3, message, LogCategoryTimeback)
}

// normalizeJSONLine info 快照归一为单行——JSONL 要求每行独立；解析失败按原样返回（不静默改写）。
func normalizeJSONLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.Contains(trimmed, "\n") {
		return trimmed
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(trimmed)); err != nil {
		logAt(LogModuleHost, 2, "timeback info 快照归一失败（按原样入档）: "+err.Error(), LogCategoryTimeback)
		return trimmed
	}
	return buf.String()
}

// buildTimebackMetaLine 构建首行 meta——记录字段（t=timeback 便于识别）。
func buildTimebackMetaLine(record *TimebackScopeRecord) string {
	var b strings.Builder
	b.WriteString(`{"t":"timeback","id":`)
	b.WriteString(itoa64(record.ID))
	b.WriteString(`,"catKey":`)
	b.WriteString(jsonStr(record.CatKey))
	b.WriteString(`,"type":`)
	b.WriteString(jsonStr(record.Type))
	b.WriteString(`,"purpose":`)
	b.WriteString(jsonStr(record.Purpose))
	b.WriteString(`,"anchor":`)
	b.WriteString(itoa(record.Anchor))
	b.WriteString(`,"startAt":`)
	b.WriteString(itoa64(record.StartAt))
	b.WriteString(`,"backAt":`)
	b.WriteString(itoa64(record.BackAt))
	b.WriteString(`,"seconds":`)
	b.WriteString(itoa64(record.Seconds))
	b.WriteString(`,"n":`)
	b.WriteString(itoa(record.N))
	b.WriteString(`,"kept":`)
	b.WriteString(itoa(record.KeptUsers))
	b.WriteString(`,"tokens":`)
	b.WriteString(itoa64(record.Tokens))
	b.WriteString(`,"grew":`)
	b.WriteString(itoa64(record.Grew))
	b.WriteString(`,"released":`)
	b.WriteString(itoa(record.Released))
	b.WriteString(`,"tools":[`)
	for i, item := range record.Writes {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonStr(item))
	}
	b.WriteString(`],"findings":`)
	b.WriteString(jsonStr(record.Findings))
	b.WriteByte('}')
	return b.String()
}
