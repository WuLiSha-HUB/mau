package session

// 完整前文留档——一会话一份文本，尾部增量采集 + 份数轮转（design-ch4-fullctx）。
//
// 与「前文关键信息」（ViewStore）独立并行：各持落点、各持端点、互不读写。
// 采集不走 agent——消息入前文即触发（节流 30 秒）；会话定稿与端点读取各自兜底强制采集。
// 线程：主线程（消息追加）与 HTTP 线程（端点读取）共用同一把锁串行，文件写互斥。
//
// 对齐 C#：ChatHome4.Core/FullContextStore.cs（格式契约见 design-ch4-fullctx §三——可剥离修饰，
// 字段块按声明字符数切片，不靠分隔符猜测）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// fullCtxCaptureInterval 采集最小间隔——30 秒（常量，不配置；窗口内触发跳过，不排队）
	fullCtxCaptureInterval = 30 * time.Second
	// fullCtxAnchorChars 增量比对锚点长度——400 字符（自文本尾端向前比对，完全一致即停止）
	fullCtxAnchorChars = 400
	// fullCtxDefaultKeep 留档份数缺省值——chat.full_ctx_keep 缺失/非法时回落
	fullCtxDefaultKeep = 3
	// fullCtxMaxKeep 留档份数上限——与 schema 值域一致
	fullCtxMaxKeep = 20
	// fullCtxHeaderVersion 文件头版本行——格式契约标识（剥离时跳过 # 开头行）
	fullCtxHeaderVersion = "# CH4-FULLCTX v1"
	// fullCtxOpen 消息段起始标记前缀
	fullCtxOpen = "[[CH4-CTX "
	// fullCtxClose 消息段结束标记
	fullCtxClose = "[[/CH4-CTX]]"
	// fullCtxHeadChars 巡检头部比对长度——正文首段比对字符数（400）
	fullCtxHeadChars = 400
	// fullCtxBOM UTF-8 BOM（首写落，之后不重复写；与 sessions_old 留档同口径）
	fullCtxBOM = "\uFEFF"
	// fullCtxModule 日志模块名（与 C# LogStore.Add 首参同源）
	fullCtxModule = "FullContextStore"
)

// FullContextEntry 留档条目——一条消息的四可载字段（与 Message 同构；解析产物，供视图与重建消费）。
type FullContextEntry struct {
	// Role 角色（system / user / assistant / tool）
	Role string
	// Time 消息时刻（Unix 毫秒；0 = 无）
	Time int64
	// ToolCallID 工具调用 ID（tool 角色）
	ToolCallID string
	// ToolName 工具名（tool 角色）
	ToolName string
	// Content 消息正文
	Content string
	// ToolCalls assistant 工具调用 JSON 数组
	ToolCalls string
	// Reasoning assistant 思考内容
	Reasoning string
	// Images user 图片附件 JSON 数组
	Images string
}

// FullContextStore 完整前文存储——一会话一份文本留档，尾部增量采集 + 份数轮转。
//
// 采集源 = 会话前文实时真源（messages 读取器）；配置面（份数）与比值面（字符/token）都是读取器，
// 即时生效、不做缓存快照。
type FullContextStore struct {
	// dir 落点目录（<data>/sessions_ctx）
	dir string
	// catKey 猫 key（文件名前缀与段归属）
	catKey string
	// messages 消息读取器——会话前文实时真源（采集时全量构建）
	messages func() []Message
	// keep 留档份数读取器（nil = 缺省）
	keep func() int
	// charsPerToken 字符/token 比值读取器（nil / 返回 0 = 无实测，回落缺省）
	charsPerToken func() float64
	// interval 采集节流窗口（生产 30 秒；测试注入 0 = 每次触发都采集）
	interval time.Duration
	// gate 采集与轮转互斥锁——主线程与 HTTP 线程共用
	gate sync.Mutex
	// path 当前会话文件路径（空 = 本会话尚未创建留档）
	path string
	// stamp 会话起时戳——文件名与段头同源（yyyyMMdd_HHmmss）
	stamp string
	// startText 会话起时刻文本——文件头 start 字段（yyyy-MM-dd HH:mm:ss）
	startText string
	// lastCapture 上次采集时刻（节流基准；零值 = 从未采集）
	lastCapture time.Time
	// headDriftWarned 留档头部漂移告警标志——每会话只出声一次（头部恢复即复位）
	headDriftWarned bool
}

// NewFullContextStore 建立完整前文存储。
//
// keep / charsPerToken 为读取器（nil 允许）——采集时实时取值，不做构造期快照。
func NewFullContextStore(dir, catKey string, messages func() []Message, keep func() int, charsPerToken func() float64) *FullContextStore {
	return &FullContextStore{
		dir:           dir,
		catKey:        catKey,
		messages:      messages,
		keep:          keep,
		charsPerToken: charsPerToken,
		interval:      fullCtxCaptureInterval,
	}
}

// ResolveFullCtxKeep 解析留档份数配置（chat.full_ctx_keep）——0..20 合法；非法值出声后回落缺省。
//
// 0 = 不销毁（全部保留——清理交由外部策略）；与 schema 值域与描述同口径。
func ResolveFullCtxKeep(raw string) int {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fullCtxDefaultKeep
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 || value > fullCtxMaxKeep {
		logAt(fullCtxModule, 2, "chat.full_ctx_keep 非法值（"+raw+"）——按缺省 "+strconv.Itoa(fullCtxDefaultKeep)+" 份", LogCategoryChat)
		return fullCtxDefaultKeep
	}
	return value
}

// Capture 采集一次——全量构建文本后按尾部锚点增量追加。
//
// force=true 忽略节流窗口（定稿 / 端点读取兜底）。
func (f *FullContextStore) Capture(force bool) {
	if f == nil {
		return
	}
	f.gate.Lock()
	defer f.gate.Unlock()
	if !force && f.interval > 0 && !f.lastCapture.IsZero() && time.Since(f.lastCapture) < f.interval {
		return
	}
	f.captureLocked()
}

// Finalize 会话定稿——强制采集 + 当前份转历史 + 轮转（保留 keep 份，销毁更旧）。
//
// 调用点：session.new 清前文之前。返回定稿文件绝对路径（空串 = 本会话无落盘）。
func (f *FullContextStore) Finalize() string {
	if f == nil {
		return ""
	}
	f.gate.Lock()
	defer f.gate.Unlock()
	f.captureLocked()
	done := f.path
	f.path = ""
	f.stamp = ""
	f.startText = ""
	f.lastCapture = time.Time{}
	keep := f.keepCount()
	removed := f.rotate(keep)
	target := "（本会话无落盘）"
	if done != "" {
		target = done
	}
	logAt(LogModuleHost, 1, "完整前文定稿: "+target+" | 保留历史 "+strconv.Itoa(keep)+" 份，销毁 "+strconv.Itoa(removed)+" 份", LogCategoryChat)
	return done
}

// BuildView 完整前文视图 JSON——对话页弹层「完整前文」数据源（GET /api/v1/fullctx）。
//
// 读取前强制采集一次（展示面不受 30 秒节流滞后影响）；条目形态与 /api/v1/context、/api/v1/keyinfo 同构。
func (f *FullContextStore) BuildView(max int) string {
	if f == nil {
		return `{"ok":true,"session":"","path":"","ratio":0,"estimated":true,"count":0,"start":0,"shown":0,"chars":0,"tokens":0,"items":[]}`
	}
	f.Capture(true)
	f.gate.Lock()
	path := f.path
	text := ""
	if path != "" {
		if raw, err := readFullCtxFile(path); err == nil {
			text = raw
		}
	}
	f.gate.Unlock()

	ratio, measured := f.resolveRatio()
	var b strings.Builder
	b.WriteString(`{"ok":true,"session":`)
	b.WriteString(jsonStr(f.catKey))
	b.WriteString(`,"path":`)
	b.WriteString(jsonStr(path))
	b.WriteString(`,"ratio":`)
	b.WriteString(strconv.FormatFloat(roundTo(ratio, 2), 'f', -1, 64))
	b.WriteString(`,"estimated":`)
	b.WriteString(strconv.FormatBool(!measured))
	if text == "" {
		b.WriteString(`,"count":0,"start":0,"shown":0,"chars":0,"tokens":0,"items":[]}`)
		return b.String()
	}
	entries, err := ParseFullContext(text)
	if err != nil {
		// 结构非法 = 可见失败（不静默留白）——文件原样保留供排查
		logAt(fullCtxModule, 3, "完整前文解析失败: "+err.Error()+"（"+path+"）", LogCategoryChat)
		b.WriteString(`,"ok":false,"error":`)
		b.WriteString(jsonStr("完整前文解析失败: " + err.Error()))
		b.WriteString("}")
		return b.String()
	}
	total := len(entries)
	totalChars := int64(0)
	items := make([]string, 0, total)
	for i := range entries {
		body := fullCtxDisplayBody(entries[i])
		chars := int64(utf16Len(body))
		totalChars += chars
		items = append(items, buildFullCtxItem(entries[i], i, body, chars))
	}
	start := 0
	if max > 0 && total > max {
		start = total - max
	}
	window := make([]string, 0, total-start+1)
	if start > 0 {
		// 首条恒在窗口内——前文头部（system 注入块）不因尾部窗口而不可见（C# 2026-10-02 判例）
		window = append(window, items[0])
	}
	window = append(window, items[start:]...)
	b.WriteString(`,"count":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`,"start":`)
	b.WriteString(strconv.Itoa(start + 1))
	b.WriteString(`,"shown":`)
	b.WriteString(strconv.Itoa(len(window)))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.FormatInt(totalChars, 10))
	b.WriteString(`,"tokens":`)
	b.WriteString(strconv.FormatInt(estimateFullCtxTokens(totalChars, ratio), 10))
	b.WriteString(`,"items":[`)
	b.WriteString(strings.Join(window, ","))
	b.WriteString("]}")
	return b.String()
}

// HeadMatches 巡检判据——前文正文首段与留档正文首段逐字符一致（design-ch4-ctx-rebuild §二）。
//
// 无留档 / 留档为空 = 无判据（返回 true，不触发重建）；首段不一致 = 前文丢头或被裁剪。
func (f *FullContextStore) HeadMatches(msgs []Message) bool {
	if f == nil {
		return true
	}
	f.gate.Lock()
	defer f.gate.Unlock()
	if f.path == "" || len(msgs) == 0 {
		return true
	}
	stored, err := readFullCtxFile(f.path)
	if err != nil || stored == "" {
		return true
	}
	live := f.buildHead(msgs)
	return !headDiffers(stored, live)
}

// TryRebuild 重建源——留档剥离为消息序列 + 留档末条之后的前文消息续接（节流窗口内的新消息不丢）。
func (f *FullContextStore) TryRebuild(live []Message) ([]Message, error) {
	if f == nil {
		return nil, fmt.Errorf("完整前文留档未挂载")
	}
	f.gate.Lock()
	defer f.gate.Unlock()
	if f.path == "" {
		return nil, fmt.Errorf("本会话无完整前文留档")
	}
	text, err := readFullCtxFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("留档读取失败: %w", err)
	}
	entries, err := ParseFullContext(text)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("留档无消息段")
	}
	out := make([]Message, 0, len(entries)+len(live))
	for i := range entries {
		msg, ok := fullCtxEntryToMessage(entries[i])
		if !ok {
			return nil, fmt.Errorf("留档角色非法: %s", entries[i].Role)
		}
		out = append(out, msg)
	}
	resume := matchFullCtxTail(entries, live)
	if resume < 0 {
		logAt(fullCtxModule, 2, "前文重建：留档末条未在当前前文中命中——尾部不续接（留档 "+strconv.Itoa(len(entries))+" 条）", LogCategoryChat)
	} else {
		out = append(out, live[resume:]...)
	}
	return out, nil
}

// captureLocked 采集实现——须在 gate 内调用（重入安全）。
func (f *FullContextStore) captureLocked() {
	f.lastCapture = time.Now()
	if err := f.capture(); err != nil {
		logAt(fullCtxModule, 3, "完整前文采集失败: "+err.Error(), LogCategoryChat)
	}
}

// capture 采集一次——全量构建文本后按尾部锚点增量追加（错误上抛，由调用方出声）。
func (f *FullContextStore) capture() error {
	var msgs []Message
	if f.messages != nil {
		msgs = f.messages()
	}
	if len(msgs) == 0 {
		return nil
	}
	path, err := f.ensurePath()
	if err != nil {
		return err
	}
	newText := f.buildText(msgs)
	stored, err := readFullCtxFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if stored == "" {
		return writeFullCtxFile(path, newText)
	}
	// 头部漂移守卫——留档正文首段 ≠ 前文正文首段 = 前文丢头：拒绝全量重写
	// （留档是重建唯一源，被覆写即失去修复依据）；出声一次，正文首段恢复即复位。
	if headDiffers(stored, newText) {
		if !f.headDriftWarned {
			f.headDriftWarned = true
			logAt(fullCtxModule, 3, "留档头部与前文不一致——拒绝全量重写（等待前文重建）: "+path, LogCategoryChat)
		}
		return nil
	}
	f.headDriftWarned = false
	if len([]rune(stored)) >= fullCtxAnchorChars {
		anchor := tailRunes(stored, fullCtxAnchorChars)
		hit := strings.LastIndex(newText, anchor)
		if hit >= 0 && hit == len(stored)-len(anchor) && hit+len(anchor) <= len(newText) {
			// 头部刷新——tokens / ratio 随采集重算（头部字节长度变化时锚点位置本就不符，走全量重写）
			if !tryRefreshFullCtxHeader(path, newText, stored) {
				return writeFullCtxFile(path, newText)
			}
			if hit+len(anchor) < len(newText) {
				return appendFullCtxFile(path, newText[hit+len(anchor):])
			}
			return nil
		}
	}
	// 已存不足锚点长度 / 锚点未命中 / 位置不符（回卷或结构修复改写了既有文本）→ 全量重写
	return writeFullCtxFile(path, newText)
}

// ensurePath 当前会话文件路径——懒创建（首次采集时定名，时戳即会话起点）；同秒重复加序号，不覆盖既有档案。
func (f *FullContextStore) ensurePath() (string, error) {
	if f.path != "" {
		return f.path, nil
	}
	if f.dir == "" {
		return "", fmt.Errorf("完整前文落点目录未配置")
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return "", err
	}
	now := time.Now()
	stamp := now.Format("20060102_150405")
	name := stamp
	path := filepath.Join(f.dir, f.catKey+"_"+name+".txt")
	for seq := 1; ; seq++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		name = stamp + "_" + strconv.Itoa(seq+1)
		path = filepath.Join(f.dir, f.catKey+"_"+name+".txt")
	}
	f.stamp = name
	f.startText = now.Format("2006-01-02 15:04:05")
	f.path = path
	logAt(LogModuleHost, 1, "完整前文留档起点: "+path, LogCategoryChat)
	return path, nil
}

// buildText 全量文本——文件头 + 逐消息段（采集构建面）。
func (f *FullContextStore) buildText(msgs []Message) string {
	var b strings.Builder
	f.appendHeader(&b, msgs)
	for _, msg := range msgs {
		appendFullCtxSection(&b, msg)
	}
	return b.String()
}

// buildHead 前文首段——文件头 + 逐消息段（累积到 chars 字符即截断；与留档同一构建面）。
func (f *FullContextStore) buildHead(msgs []Message) string {
	var b strings.Builder
	f.appendHeader(&b, msgs)
	limit := fullCtxHeadChars
	for _, msg := range msgs {
		if len([]rune(b.String())) >= limit {
			break
		}
		appendFullCtxSection(&b, msg)
	}
	text := b.String()
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

// appendHeader 文件头——版本行 + 猫 / 会话 / 起时 + token 折算行（采集与首段构建共用同一出口）。
func (f *FullContextStore) appendHeader(b *strings.Builder, msgs []Message) {
	ratio, measured := f.resolveRatio()
	tokens := estimateFullCtxTokens(fullCtxContextChars(msgs), ratio)
	source := "default"
	if measured {
		source = "measured"
	}
	b.WriteString(fullCtxHeaderVersion)
	b.WriteByte('\n')
	b.WriteString("# cat=")
	b.WriteString(f.catKey)
	b.WriteString(" session=")
	b.WriteString(f.stamp)
	b.WriteString(" start=")
	b.WriteString(f.startText)
	b.WriteByte('\n')
	b.WriteString("# tokens=")
	b.WriteString(strconv.FormatInt(tokens, 10))
	b.WriteString(" ratio=")
	b.WriteString(strconv.FormatFloat(ratio, 'f', 2, 64))
	b.WriteString(" source=")
	b.WriteString(source)
	b.WriteByte('\n')
}

// resolveRatio 字符/token 比值——本会话实测值优先；无实测回落缺省 2.4 并标记。
func (f *FullContextStore) resolveRatio() (float64, bool) {
	ratio := 0.0
	if f.charsPerToken != nil {
		ratio = f.charsPerToken()
	}
	if ratio > 0 {
		return ratio, true
	}
	return defaultContextRatio, false
}

// keepCount 留档份数——读取器缺省 / 越界收敛（与 schema 值域同口径）。
func (f *FullContextStore) keepCount() int {
	if f.keep == nil {
		return fullCtxDefaultKeep
	}
	value := f.keep()
	if value == 0 {
		// 0 = 不清理（保留全部历史份——止血出口；定时清理归外部策略）
		return 0
	}
	if value < 0 {
		return fullCtxDefaultKeep
	}
	if value > fullCtxMaxKeep {
		return fullCtxMaxKeep
	}
	return value
}

// rotate 轮转——按文件名时戳序（同一猫前缀下字典序即时间序）保留最新 keep 份，其余销毁。
//
// keep <= 0 = 不轮转（保留全部历史份）。返回销毁份数。
func (f *FullContextStore) rotate(keep int) int {
	if keep <= 0 || f.dir == "" {
		return 0
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return 0
	}
	prefix := f.catKey + "_"
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".txt") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	removed := 0
	for i := 0; i < len(names)-keep; i++ {
		if err := os.Remove(filepath.Join(f.dir, names[i])); err == nil {
			removed++
		}
	}
	return removed
}

// appendFullCtxSection 单消息段——段头（四属性恒全写）+ 非空字段块 + 段尾。
func appendFullCtxSection(b *strings.Builder, msg Message) {
	b.WriteString(fullCtxOpen)
	b.WriteString("role=")
	b.WriteString(RoleName(msg.Role))
	b.WriteString(" time=")
	b.WriteString(strconv.FormatInt(msg.CreatedAt, 10))
	b.WriteString(" toolcallid=")
	b.WriteString(msg.ToolCallID)
	b.WriteString(" tool=")
	b.WriteString(msg.ToolName)
	b.WriteString("]]\n")
	appendFullCtxField(b, "content", msg.Content)
	appendFullCtxField(b, "tool_calls", msg.ToolCallsJSON)
	appendFullCtxField(b, "reasoning", msg.ReasoningContent)
	appendFullCtxField(b, "images", msg.ImagesJSON)
	b.WriteString(fullCtxClose)
	b.WriteByte('\n')
}

// appendFullCtxField 字段块——空正文整块不出现；字符数为 UTF-16 码元（剥离按此切片）。
func appendFullCtxField(b *strings.Builder, name, body string) {
	if body == "" {
		return
	}
	b.WriteString("[[")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(utf16Len(body)))
	b.WriteString("]]\n")
	b.WriteString(body)
	b.WriteString("\n[[/")
	b.WriteString(name)
	b.WriteString("]]\n")
}

// ParseFullContext 剥离器——格式文本 → 消息条目序列（重建前文入口；按字段声明的字符数切片）。
//
// 字符数口径 = UTF-16 码元（C# string.Length 同口径）；结构非法返回错误（调用方出声，不静默降级）。
func ParseFullContext(text string) ([]FullContextEntry, error) {
	entries := make([]FullContextEntry, 0, 64)
	if text == "" {
		return entries, nil
	}
	runes := []rune(strings.TrimPrefix(text, fullCtxBOM))
	pos := 0
	current := -1
	for pos < len(runes) {
		lineEnd := indexRuneFrom(runes, pos, '\n')
		if lineEnd < 0 {
			lineEnd = len(runes)
		}
		line := string(runes[pos:lineEnd])
		switch {
		case line == "":
			pos = lineEnd + 1
			continue
		case strings.HasPrefix(line, "# "):
			pos = lineEnd + 1
			continue
		case strings.HasPrefix(line, fullCtxOpen) && strings.HasSuffix(line, "]]"):
			entry, ok := parseFullCtxSectionHead(line)
			if !ok {
				return nil, fmt.Errorf("消息段头非法（字符位置 %d）", pos)
			}
			entries = append(entries, entry)
			current = len(entries) - 1
			pos = lineEnd + 1
			continue
		case line == fullCtxClose:
			if current < 0 {
				return nil, fmt.Errorf("消息段结束标记无对应段头")
			}
			current = -1
			pos = lineEnd + 1
			continue
		case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
			if current < 0 {
				return nil, fmt.Errorf("字段块出现在消息段之外: %s", line)
			}
			space := strings.LastIndex(line, " ")
			if space < 0 {
				return nil, fmt.Errorf("字段块头非法（缺字符数）: %s", line)
			}
			count, err := strconv.Atoi(line[space+1 : len(line)-2])
			if err != nil || count < 0 {
				return nil, fmt.Errorf("字段块头非法（缺字符数）: %s", line)
			}
			name := line[2:space]
			pos = lineEnd + 1
			body, next, ok := takeUTF16Units(runes, pos, count)
			if !ok {
				return nil, fmt.Errorf("字段 %s 正文越界（声明 %d 字符）", name, count)
			}
			pos = next
			if pos >= len(runes) || runes[pos] != '\n' {
				return nil, fmt.Errorf("字段 %s 正文后缺换行", name)
			}
			pos++
			tail := "[[/" + name + "]]"
			if !hasRunePrefix(runes, pos, tail) {
				return nil, fmt.Errorf("字段 %s 正文后缺结束标记", name)
			}
			pos += len([]rune(tail))
			if pos < len(runes) && runes[pos] == '\n' {
				pos++
			}
			if !assignFullCtxField(&entries[current], name, body) {
				return nil, fmt.Errorf("未知字段: %s", name)
			}
			continue
		}
		return nil, fmt.Errorf("未识别行: %s", headRunes(line, 40))
	}
	if current >= 0 {
		return nil, fmt.Errorf("消息段未闭合")
	}
	return entries, nil
}

// parseFullCtxSectionHead 段头解析——四属性列固定；缺 role 视为非法段头。
func parseFullCtxSectionHead(line string) (FullContextEntry, bool) {
	entry := FullContextEntry{}
	inner := line[len(fullCtxOpen) : len(line)-2]
	hasRole := false
	for _, part := range strings.Split(inner, " ") {
		eq := strings.Index(part, "=")
		if eq <= 0 {
			continue
		}
		key := part[:eq]
		value := part[eq+1:]
		switch key {
		case "role":
			entry.Role = value
			hasRole = value != ""
		case "time":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				entry.Time = parsed
			}
		case "toolcallid":
			entry.ToolCallID = value
		case "tool":
			entry.ToolName = value
		}
	}
	return entry, hasRole
}

// assignFullCtxField 字段名 → 条目字段（未知字段 = 结构非法）。
func assignFullCtxField(entry *FullContextEntry, name, body string) bool {
	switch name {
	case "content":
		entry.Content = body
		return true
	case "tool_calls":
		entry.ToolCalls = body
		return true
	case "reasoning":
		entry.Reasoning = body
		return true
	case "images":
		entry.Images = body
		return true
	}
	return false
}

// fullCtxEntryToMessage 留档条目 → 消息——四字段 + 配对 ID + 时刻全字段还原（角色非法返回 false）。
func fullCtxEntryToMessage(entry FullContextEntry) (Message, bool) {
	role, ok := fullCtxRoleFromText(entry.Role)
	if !ok {
		return Message{}, false
	}
	return Message{
		Role:             role,
		Content:          entry.Content,
		ToolCallID:       entry.ToolCallID,
		ToolName:         entry.ToolName,
		ToolCallsJSON:    entry.ToolCalls,
		ReasoningContent: entry.Reasoning,
		ImagesJSON:       entry.Images,
		CreatedAt:        entry.Time,
	}, true
}

// fullCtxRoleFromText 角色文本 → 角色（wire 四 role 小写；未知返回 false）。
func fullCtxRoleFromText(text string) (Role, bool) {
	switch text {
	case "system":
		return RoleSystem, true
	case "user":
		return RoleUser, true
	case "assistant":
		return RoleAssistant, true
	case "tool":
		return RoleTool, true
	}
	return RoleTool, false
}

// matchFullCtxTail 尾部续接定位——留档末条在现前文中的位置（逐字段全等，自尾端向前找），命中返回其后一条下标。
//
// 未命中返回 -1（两端不同源——不续接，由调用方出声）。
func matchFullCtxTail(entries []FullContextEntry, live []Message) int {
	if len(entries) == 0 || len(live) == 0 {
		return -1
	}
	last := entries[len(entries)-1]
	for i := len(live) - 1; i >= 0; i-- {
		if sameFullCtxMessage(last, live[i]) {
			return i + 1
		}
	}
	return -1
}

// sameFullCtxMessage 留档条目与消息逐字段全等——续接定位判据（角色 / 四字段 / 配对 ID / 时刻）。
func sameFullCtxMessage(entry FullContextEntry, msg Message) bool {
	role, ok := fullCtxRoleFromText(entry.Role)
	if !ok || role != msg.Role || entry.Time != msg.CreatedAt {
		return false
	}
	return entry.Content == msg.Content &&
		entry.ToolCallID == msg.ToolCallID &&
		entry.ToolCalls == msg.ToolCallsJSON &&
		entry.Reasoning == msg.ReasoningContent &&
		entry.Images == msg.ImagesJSON
}

// fullCtxDisplayBody 条目展示正文——与 /api/v1/context 同口径（四字段合并）。
func fullCtxDisplayBody(entry FullContextEntry) string {
	return MergeBody(Message{
		Content:          entry.Content,
		ToolCallsJSON:    entry.ToolCalls,
		ReasoningContent: entry.Reasoning,
		ImagesJSON:       entry.Images,
	})
}

// buildFullCtxItem 单条完整前文条目——i / role / tool / chars / time / truncated / preview / content。
func buildFullCtxItem(entry FullContextEntry, index int, body string, chars int64) string {
	truncated := chars > contextItemLimit
	shown := body
	if truncated {
		shown = truncateUTF16(body, contextItemLimit)
	}
	var b strings.Builder
	b.WriteString(`{"i":`)
	b.WriteString(strconv.Itoa(index + 1))
	b.WriteString(`,"role":`)
	b.WriteString(jsonStr(entry.Role))
	b.WriteString(`,"tool":`)
	b.WriteString(jsonStr(entry.ToolName))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.FormatInt(chars, 10))
	b.WriteString(`,"time":`)
	b.WriteString(strconv.FormatInt(entry.Time, 10))
	b.WriteString(`,"truncated":`)
	b.WriteString(strconv.FormatBool(truncated))
	b.WriteString(`,"preview":`)
	b.WriteString(jsonStr(previewText(body, contextPreviewLimit)))
	b.WriteString(`,"content":`)
	b.WriteString(jsonStr(shown))
	b.WriteString("}")
	return b.String()
}

// fullCtxContextChars 前文字符数——消息序列四字段合并字符数之和（与 /api/v1/context 的 chars 同口径）。
func fullCtxContextChars(msgs []Message) int64 {
	total := int64(0)
	for _, msg := range msgs {
		total += int64(utf16Len(MergeBody(msg)))
	}
	return total
}

// estimateFullCtxTokens token 折算——字符数 ÷ 比值（四舍五入；字符或比值非法 = 0）。
func estimateFullCtxTokens(chars int64, ratio float64) int64 {
	if chars <= 0 || ratio <= 0 {
		return 0
	}
	return int64(roundTo(float64(chars)/ratio, 0))
}

// bodyStart 正文起点——跳过文件头 `# ` 注释行（与剥离器同口径）。
//
// 头部字段可变（tokens / ratio / source），比对与原地刷新都以正文起点为界。
func bodyStart(text string) int {
	pos := 0
	for pos+1 < len(text) && text[pos] == '#' && text[pos+1] == ' ' {
		lineEnd := strings.IndexByte(text[pos:], '\n')
		if lineEnd < 0 {
			return len(text)
		}
		pos += lineEnd + 1
	}
	return pos
}

// headDiffers 首段比对——留档正文首段与现构正文首段是否不一致（比对面 = 正文，头部折算值不参与）。
func headDiffers(stored, live string) bool {
	storedBody := stored[bodyStart(stored):]
	liveBody := live[bodyStart(live):]
	left := headRunes(storedBody, fullCtxHeadChars)
	right := headRunes(liveBody, fullCtxHeadChars)
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if n == 0 {
		return false
	}
	return left[:n] != right[:n]
}

// headRunes 取文本前 n 个字符（rune 边界完整——不截半个字符）。
func headRunes(text string, n int) string {
	count := 0
	for index := range text {
		if count == n {
			return text[:index]
		}
		count++
	}
	return text
}

// tailRunes 取文本尾部 n 个字符（锚点用）。
func tailRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[len(runes)-n:])
}

// takeUTF16Units 从 runes[pos] 起取 units 个 UTF-16 码元——返回正文与下一位置（越界 / 半代理对 = false）。
func takeUTF16Units(runes []rune, pos, units int) (string, int, bool) {
	used := 0
	start := pos
	for pos < len(runes) && used < units {
		width := 1
		if runes[pos] > 0xFFFF {
			width = 2
		}
		if used+width > units {
			return "", pos, false
		}
		used += width
		pos++
	}
	if used != units {
		return "", pos, false
	}
	return string(runes[start:pos]), pos, true
}

// hasRunePrefix 位置比对——runes[pos:] 是否以 value 开头（不分配子串）。
func hasRunePrefix(runes []rune, pos int, value string) bool {
	for _, r := range value {
		if pos >= len(runes) || runes[pos] != r {
			return false
		}
		pos++
	}
	return true
}

// indexRuneFrom 自 pos 起查找 rune——返回索引（未命中 = -1）。
func indexRuneFrom(runes []rune, pos int, target rune) int {
	for i := pos; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// readFullCtxFile 读取留档文本——剥 UTF-8 BOM（与 C# File.ReadAllText 自动剥离同口径）。
func readFullCtxFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(raw), fullCtxBOM), nil
}

// writeFullCtxFile 全量写留档——UTF-8 BOM（首写落，与 sessions_old 留档同口径）。
func writeFullCtxFile(path, text string) error {
	return os.WriteFile(path, []byte(fullCtxBOM+text), 0o644)
}

// appendFullCtxFile 尾部追加——BOM 只在首写时落（文件非空不重复写）。
func appendFullCtxFile(path, text string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(text)
	return err
}

// tryRefreshFullCtxHeader 头部原地刷新——把在盘文件头的 `# ` 注释行覆盖为新头部（正文与尾部不动）。
//
// 新头部与在盘头部字节长度不一致 = 返回 false（调用方全量重写）；一致 = 原地覆盖。
func tryRefreshFullCtxHeader(path, newText, stored string) bool {
	header := newText[:bodyStart(newText)]
	storedEnd := bodyStart(stored)
	if header == "" || storedEnd == 0 {
		return false
	}
	storedHeader := stored[:storedEnd]
	if storedHeader == header {
		return true
	}
	if len(storedHeader) != len(header) {
		return false
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		logAt(fullCtxModule, 3, "留档头部刷新失败（走全量重写）: "+err.Error(), LogCategoryChat)
		return false
	}
	defer file.Close()
	if _, err := file.WriteAt([]byte(header), int64(fullCtxBOMLen(path))); err != nil {
		logAt(fullCtxModule, 3, "留档头部刷新失败（走全量重写）: "+err.Error(), LogCategoryChat)
		return false
	}
	return true
}

// fullCtxBOMLen BOM 字节数——文件头三字节探测（EF BB BF = 3；无 BOM = 0）。
func fullCtxBOMLen(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	head := make([]byte, 3)
	n, _ := file.Read(head)
	if n == 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF {
		return 3
	}
	return 0
}
