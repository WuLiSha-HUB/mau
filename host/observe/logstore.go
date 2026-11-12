package observe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 日志危险等级——与 C# LogStore 一致：1=INFO / 2=WARN / 3=ERR。
const (
	// LevelInfo 信息级。
	LevelInfo = 1
	// LevelWarn 告警级。
	LevelWarn = 2
	// LevelErr 错误级（err_all.txt 投影条件）。
	LevelErr = 3
)

// DefaultLogCapacity 是内存总账环形容量。
//
// C# 版总账是无限 List；Go 侧改为定长环形（内存可控），容量取 8192——
// 覆盖 /api/v1/logs 的 2000 条上限与快照 ?logs=200 的消费面。
const DefaultLogCapacity = 8192

// Entry 是一条日志条目（与 C# LogStore.LogEntry 七字段对齐）。
type Entry struct {
	// Time 时间戳（yyyy-MM-dd HH:mm:ss）
	Time string
	// Module 模块名
	Module string
	// Level 危险等级（1/2/3；0 归一 1）
	Level int
	// Message 消息
	Message string
	// Category 类别（CHAT/TOOL/CMD/OA/LLM/AUDIT/SYS/BOOT/CONFIG/WORKSPACE 等）
	Category string
	// Frame 写入时全局帧号
	Frame int64
	// Type 审计事件名（audit.*；非审计空串）
	Type string
	// Payload 结构化载荷 JSON（非审计空串）
	Payload string
}

// LogStore 是程序级日志总账：内存环形 + 磁盘三投影。
//
// 磁盘投影对齐 C# LoggerStore 的 runs 目录四文件的三个：
//   - log_all.txt 全量时序
//   - oa_all.txt  category=OA 投影
//   - err_all.txt level>=3 投影
//
// 并发：内存面用 mu，磁盘面用 writeMu（C# 同款两把锁——内存快照不被磁盘 IO 阻塞）。
type LogStore struct {
	mu    sync.Mutex
	ring  []Entry
	start int
	count int
	total int

	writeMu  sync.Mutex
	runDir   string
	filePath string
	logFile  *os.File
	oaFile   *os.File
	errFile  *os.File

	consoleSink func(string)

	// lastConfigureError 是最近一次 ConfigureRuns 的失败原因（空 = 成功）
	lastConfigureError string
}

// NewLogStore 创建内存总账；capacity <= 0 用默认容量。
func NewLogStore(capacity int) *LogStore {
	if capacity <= 0 {
		capacity = DefaultLogCapacity
	}
	return &LogStore{ring: make([]Entry, capacity)}
}

// ConfigureRuns 配置运行目录并打开三文件写者（append 模式）。
//
// 语义与 C# ConfigureRuns 一致：目录创建失败不抛——返回错误串由调用方换落点重试；
// 打开失败的写者保持 nil（该类投影静默缺失，但 log_all 失败会经 LastConfigureError
// 上报给启动健康面）。重复调用幂等（先关旧写者）。
func (s *LogStore) ConfigureRuns(runDir string) string {
	if runDir == "" {
		return ""
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.closeWritersLocked()
	s.lastConfigureError = ""
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		s.lastConfigureError = err.Error()
		s.runDir = ""
		s.filePath = ""
		return s.lastConfigureError
	}
	s.runDir = runDir
	s.filePath = filepath.Join(runDir, "log_all.txt")
	s.logFile = openWriterWithRetry(filepath.Join(runDir, "log_all.txt"))
	s.oaFile = openWriterWithRetry(filepath.Join(runDir, "oa_all.txt"))
	s.errFile = openWriterWithRetry(filepath.Join(runDir, "err_all.txt"))
	if s.logFile == nil {
		s.lastConfigureError = "log_all.txt 打开失败"
	}
	return s.lastConfigureError
}

// ConfigureLogFile 配置单文件兼容模式（C# ConfigureLogFile 的等价面）。
func (s *LogStore) ConfigureLogFile(path string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.closeWritersLocked()
	s.filePath = path
	if path == "" {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	s.logFile = openWriterWithRetry(path)
}

// LastConfigureError 返回最近一次运行目录配置的失败原因（空 = 成功）。
func (s *LogStore) LastConfigureError() string {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.lastConfigureError
}

// RunDir 返回当前运行目录（空 = 仅内存）。
func (s *LogStore) RunDir() string {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.runDir
}

// SetConsoleSink 设置 Console 订阅（每条非 skipDisk 日志回调一行）。
func (s *LogStore) SetConsoleSink(fn func(string)) {
	s.writeMu.Lock()
	s.consoleSink = fn
	s.writeMu.Unlock()
}

// Close 关闭全部写者（退出前调用）。
func (s *LogStore) Close() {
	s.writeMu.Lock()
	s.closeWritersLocked()
	s.writeMu.Unlock()
}

// closeWritersLocked 关闭写者（调用方持 writeMu）。
func (s *LogStore) closeWritersLocked() {
	for _, f := range []*os.File{s.logFile, s.oaFile, s.errFile} {
		if f != nil {
			_ = f.Close()
		}
	}
	s.logFile = nil
	s.oaFile = nil
	s.errFile = nil
}

// openWriterWithRetry 打开 append 写者——瞬时占用（安全软件扫描 / 残留句柄）退避重试。
func openWriterWithRetry(path string) *os.File {
	delays := []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 700 * time.Millisecond}
	var last error
	for i := 0; i <= len(delays); i++ {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			return f
		}
		last = err
		if i < len(delays) {
			time.Sleep(delays[i])
		}
	}
	reportOpenFailure(path, last)
	return nil
}

// reportOpenFailure 把写者打开失败追加到运行目录同级诊断文件（自身失败静默，避免递归）。
func reportOpenFailure(path string, err error) {
	dir := filepath.Dir(path)
	note := filepath.Join(dir, "log_open_error.txt")
	line := time.Now().Format("2006-01-02 15:04:05") + " | " + path + " | " + errorText(err) + "\r\n"
	_ = os.WriteFile(note, []byte(line), 0o644)
	// 诊断文件本身失败也不抛——观测不可写不得阻断宿主（只在 stderr 出声）
	fmt.Fprintln(os.Stderr, "[LogStore] 日志写者打开失败: "+path+" | "+errorText(err))
}

// errorText 安全取错误文本。
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Add 写入一条结构化日志（全参数入口）。
//
// 语义与 C# LogStore.Add 一致：内存必留；磁盘三投影（skipDisk 例外）；
// Console 订阅在磁盘之后（同构行）。
func (s *LogStore) Add(module string, level int, message, category, typeName, payload string, maxLen int, frame int64, skipDisk bool) {
	if level <= 0 {
		level = LevelInfo
	}
	if maxLen > 0 {
		runes := []rune(message)
		if len(runes) > maxLen {
			message = string(runes[:maxLen]) + "…[截断:" + itoa(len(runes)) + "]"
		}
	}
	entry := Entry{
		Time:     time.Now().Format("2006-01-02 15:04:05"),
		Module:   safeText(module),
		Level:    level,
		Message:  message,
		Category: category,
		Frame:    frame,
		Type:     typeName,
		Payload:  payload,
	}
	s.mu.Lock()
	s.appendRingLocked(entry)
	s.mu.Unlock()
	if skipDisk {
		return
	}
	s.writeProjections(entry)
}

// AddSimple 是简便入口（无 Type / Payload / 帧号覆盖）。
func (s *LogStore) AddSimple(module string, level int, message, category string, frame int64) {
	s.Add(module, level, message, category, "", "", 0, frame, false)
}

// appendRingLocked 追加环形（调用方持 mu）。
func (s *LogStore) appendRingLocked(e Entry) {
	if s.count < len(s.ring) {
		s.ring[(s.start+s.count)%len(s.ring)] = e
		s.count++
	} else {
		s.ring[s.start] = e
		s.start = (s.start + 1) % len(s.ring)
	}
	s.total++
}

// writeProjections 写磁盘三投影 + Console 订阅。
//
// 写者缺失（打开失败）时静默跳过该投影——失败已由 ConfigureRuns / reportOpenFailure 出声；
// 不在日志热路径上重试打开（会以 1s 级退避阻塞 Add）。
func (s *LogStore) writeProjections(entry Entry) {
	s.writeMu.Lock()
	logFile, oaFile, errFile, sink := s.logFile, s.oaFile, s.errFile, s.consoleSink
	if logFile != nil || oaFile != nil || errFile != nil {
		line := FormatLine(entry) + "\r\n"
		var writeErr error
		if logFile != nil {
			if _, err := logFile.WriteString(line); err != nil {
				writeErr = err
			}
		}
		if oaFile != nil && entry.Category == "OA" {
			if _, err := oaFile.WriteString(line); err != nil {
				writeErr = err
			}
		}
		if errFile != nil && entry.Level >= LevelErr {
			if _, err := errFile.WriteString(line); err != nil {
				writeErr = err
			}
		}
		if writeErr != nil {
			fmt.Fprintln(os.Stderr, "[LogStore] 磁盘投影写入失败: "+writeErr.Error())
		}
	}
	s.writeMu.Unlock()
	if sink != nil {
		defer func() {
			// 订阅异常不影响日志主链
			_ = recover()
		}()
		sink(FormatLine(entry))
	}
}

// Snapshot 返回内存总账尾部 tail 条（0 = 全量，受环形容量所限）；按时间序（旧→新）。
func (s *LogStore) Snapshot(tail int) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := 0
	if tail > 0 && s.count > tail {
		start = s.count - tail
	}
	out := make([]Entry, 0, s.count-start)
	for i := start; i < s.count; i++ {
		out = append(out, s.ring[(s.start+i)%len(s.ring)])
	}
	return out
}

// SnapshotFrom 返回绝对索引 from 之后的条目，并回报末端游标（累计写入数）。
//
// 环形已丢弃的区间自动收敛到最旧条目——游标语义不退（消费方按 endIndex 推进）。
func (s *LogStore) SnapshotFrom(from int) ([]Entry, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldest := s.total - s.count
	if from < oldest {
		from = oldest
	}
	if from < 0 {
		from = 0
	}
	if from > s.total {
		from = s.total
	}
	start := from - oldest
	out := make([]Entry, 0, s.count-start)
	for i := start; i < s.count; i++ {
		out = append(out, s.ring[(s.start+i)%len(s.ring)])
	}
	return out, s.total
}

// LevelText 级别文本——0/1=INFO 2=WARN >=3=ERR。
func LevelText(level int) string {
	if level >= LevelErr {
		return "ERR"
	}
	if level == LevelWarn {
		return "WARN"
	}
	return "INFO"
}

// FormatLine 行格式（log_all.txt / err_all.txt / Console 同构）：
//
//	{time} | F{frame} | {LEVEL} | [{category}] | {message} | #{type} | {payload 摘要}
func FormatLine(entry Entry) string {
	var b strings.Builder
	b.WriteString(entry.Time)
	b.WriteString(" | F")
	b.WriteString(itoa64(entry.Frame))
	b.WriteString(" | ")
	b.WriteString(LevelText(entry.Level))
	if entry.Category != "" {
		b.WriteString(" | [")
		b.WriteString(entry.Category)
		b.WriteString("]")
	}
	b.WriteString(" | ")
	b.WriteString(entry.Message)
	if entry.Type != "" {
		b.WriteString(" | #")
		b.WriteString(entry.Type)
	}
	if entry.Payload != "" {
		if summary := summarizePayload(entry.Payload); summary != "" {
			b.WriteString(" | ")
			b.WriteString(summary)
		}
	}
	return b.String()
}

// summarizePayload 载荷摘要——审计 props（[{"k":..,"v":..}]）渲染为 k=v 逗号拼接；
// 解析失败或非数组回落原文截断 120 字符（与 C# SummarizePayload 同口径）。
func summarizePayload(payload string) string {
	if parsed, ok := parsePayloadPairs(payload); ok {
		return parsed
	}
	runes := []rune(payload)
	if len(runes) > 120 {
		return string(runes[:120]) + "..."
	}
	return payload
}

// IsTraceAudit 判定 trace 类审计（audit.*.trace.* 不进前端）。
func IsTraceAudit(entry Entry) bool {
	if entry.Type == "" {
		return false
	}
	return strings.HasPrefix(entry.Type, "audit.") && strings.Contains(entry.Type, ".trace.")
}

// safeText 清洗模块名（空值保持空串）。
func safeText(v string) string {
	return strings.TrimSpace(v)
}

// itoa 小整数转字符串。
func itoa(v int) string { return itoa64(int64(v)) }

// itoa64 整数转字符串。
func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
