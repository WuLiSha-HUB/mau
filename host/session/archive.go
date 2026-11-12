package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本文件复现旧会话留档（session.new 清空前导出）。
//
// 契约对齐 `mau:CatHome4/CatHome4.Core/SessionViewStore.cs` 的 ArchiveLegacy / BuildLegacyMarkdown：
// 只留四部分（user 消息 / 正式回复 / 加载报告 / 每轮结算）；思考 / 工具卡 / 间隙文本 / 错误 / 重试不进档；
// 四部分全空也照常出档（档案面留痕优先于体积）。
// 落点 `<data>/sessions_old/<显示名>_<yyyyMMdd_HHmmss>.md`，UTF-8 BOM。

// legacyTimeLayout 留档时间戳格式（本地时间；C# "MM-dd HH:mm:ss"）。
const legacyTimeLayout = "01-02 15:04:05"

// ArchiveLegacy 旧会话留档——返回落盘绝对路径（空串 = 未生成）。
func (v *ViewStore) ArchiveLegacy(catKey, displayName string) string {
	if v == nil || v.path == "" {
		return ""
	}
	// [段1] 落点——sessions_old 与 sessions 同级
	sessionDir := filepath.Dir(v.path)
	sessionsRoot := filepath.Dir(sessionDir)
	if !strings.EqualFold(filepath.Base(sessionsRoot), "sessions") {
		return ""
	}
	dataRoot := filepath.Dir(sessionsRoot)
	archiveDir := filepath.Join(dataRoot, "sessions_old")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return ""
	}
	// [段2] 文件名——显示名 + 归档时间戳（非法字符清洗；显示名空回落猫 key；同秒重名加序号）
	prefix := sanitizeFileName(displayName)
	if prefix == "" {
		prefix = sanitizeFileName(catKey)
	}
	stamp := time.Now().Format("20060102_150405")
	file := filepath.Join(archiveDir, prefix+"_"+stamp+".md")
	suffix := 1
	for {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			break
		}
		suffix++
		file = filepath.Join(archiveDir, prefix+"_"+stamp+"_"+itoa(suffix)+".md")
	}
	// [段3] 生成 + 落盘（UTF-8 BOM——与 C# new UTF8Encoding(true) 同形）
	text := v.buildLegacyMarkdown(catKey, displayName, stamp)
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...)
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		return ""
	}
	return file
}

// buildLegacyMarkdown 生成留档 Markdown——文件头 + 新会话加载报告 + 对话时序。
func (v *ViewStore) buildLegacyMarkdown(catKey, displayName, stamp string) string {
	var body strings.Builder
	firstTs := int64(0)
	lastTs := int64(0)
	hasTs := false
	userCount := 0
	replyCount := 0
	sumCount := 0
	for _, block := range v.CollectLegacyBlocks() {
		switch block.RenderType {
		case TypeUser:
			userCount++
		case TypeText:
			replyCount++
		case TypeRoundSum:
			sumCount++
		}
		if !hasTs || block.Timestamp < firstTs {
			firstTs = block.Timestamp
		}
		hasTs = true
		if block.Timestamp > lastTs {
			lastTs = block.Timestamp
		}
		appendLegacyBlock(&body, block)
	}
	rangeText := "—"
	if hasTs {
		rangeText = formatLegacyTime(firstTs) + " ~ " + formatLegacyTime(lastTs)
	}
	var sb strings.Builder
	sb.WriteString("# 会话留档 — " + displayName + "\n\n")
	sb.WriteString("- 猫：`" + catKey + "`（" + displayName + "）\n")
	sb.WriteString("- 归档：" + stamp + "\n")
	sb.WriteString("- 区间：" + rangeText + "\n")
	sb.WriteString("- 块数：user " + itoa(userCount) + " · 回复 " + itoa(replyCount) + " · 轮结算 " + itoa(sumCount) + "\n\n")
	sb.WriteString("---\n\n")
	sb.WriteString("## 新会话加载报告\n\n")
	appendInjectReportBody(&sb, v.GetInjectReport())
	sb.WriteString("---\n\n")
	sb.WriteString("## 对话\n\n")
	sb.WriteString(body.String())
	return sb.String()
}

// CollectLegacyBlocks 归并留档条目——blocks 只取 user/text，roundSums 全取，按时间戳升序（相等取 block）。
func (v *ViewStore) CollectLegacyBlocks() []*ViewBlock {
	v.mu.Lock()
	defer v.mu.Unlock()
	seq := make([]*ViewBlock, 0, len(v.blocks)+len(v.roundSums))
	blockCursor := 0
	sumCursor := 0
	for {
		for blockCursor < len(v.blocks) && !isLegacyBlock(v.blocks[blockCursor].RenderType) {
			blockCursor++
		}
		hasBlock := blockCursor < len(v.blocks)
		hasSum := sumCursor < len(v.roundSums)
		if !hasBlock && !hasSum {
			break
		}
		takeBlock := hasBlock && (!hasSum || v.blocks[blockCursor].Timestamp <= v.roundSums[sumCursor].Timestamp)
		if takeBlock {
			seq = append(seq, v.blocks[blockCursor])
			blockCursor++
		} else {
			seq = append(seq, v.roundSums[sumCursor])
			sumCursor++
		}
	}
	return seq
}

// isLegacyBlock 留档入选判定——user 消息与正式回复（text）进档。
func isLegacyBlock(renderType string) bool {
	return renderType == TypeUser || renderType == TypeText
}

// appendLegacyBlock 留档条目渲染——user/text 取载荷 text；roundsum 转简洁统计行。
func appendLegacyBlock(sb *strings.Builder, block *ViewBlock) {
	title := "用户"
	switch block.RenderType {
	case TypeText:
		title = "回复"
	case TypeRoundSum:
		title = "轮结算"
	}
	sb.WriteString("### " + formatLegacyTime(block.Timestamp) + " · " + title + "\n\n")
	if block.RenderType == TypeRoundSum {
		sb.WriteString(buildRoundSumLine(block.Payload) + "\n\n")
		return
	}
	var payload struct {
		Text string `json:"text"`
	}
	text := ""
	if err := json.Unmarshal([]byte(block.Payload), &payload); err == nil {
		text = payload.Text
	}
	sb.WriteString(text + "\n\n")
}

// buildRoundSumLine 轮结算行渲染——Token / 缓存 / 工具次数 / 请求次数 / 用时。
func buildRoundSumLine(payloadJSON string) string {
	var payload struct {
		Data *struct {
			Prompt     int64 `json:"prompt"`
			Completion int64 `json:"completion"`
			CacheHit   int64 `json:"cacheHit"`
			ToolCount  int64 `json:"toolCount"`
			Requests   int64 `json:"requests"`
			ElapsedMs  int64 `json:"elapsedMs"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || payload.Data == nil {
		return "（载荷缺 data 字段）"
	}
	data := payload.Data
	seconds := float64(data.ElapsedMs) / 1000.0
	return "Token 上 " + itoa64(data.Prompt) + " / 下 " + itoa64(data.Completion) +
		"（缓存 " + itoa64(data.CacheHit) + "）· 工具 " + itoa64(data.ToolCount) + " 次" +
		" · 请求 " + itoa64(data.Requests) + " 次 · 用时 " + formatSeconds(seconds) + " 秒"
}

// appendInjectReportBody 加载报告正文——逐文件清单 + 工具组清单（无报告记一行）。
func appendInjectReportBody(sb *strings.Builder, injectReport string) {
	if injectReport == "" {
		sb.WriteString("- 无（旧会话未生成注入报告）\n\n")
		return
	}
	var report struct {
		Total   int64 `json:"total"`
		OK      int64 `json:"ok"`
		Missing int64 `json:"missing"`
		Failed  int64 `json:"failed"`
		Files   []struct {
			File   string `json:"file"`
			Status string `json:"status"`
			Chars  int64  `json:"chars"`
		} `json:"files"`
		ToolGroups []struct {
			Group string `json:"group"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"toolGroups"`
	}
	if err := json.Unmarshal([]byte(injectReport), &report); err != nil {
		sb.WriteString("- 解析失败，原文：\n\n```json\n" + injectReport + "\n```\n")
		sb.WriteString("\n")
		return
	}
	sb.WriteString("- 注入清单：" + itoa64(report.Total) + " 个文件（ok " + itoa64(report.OK) +
		" / missing " + itoa64(report.Missing) + " / 失败 " + itoa64(report.Failed) + "）\n")
	if len(report.Files) > 0 {
		sb.WriteString("\n")
		for _, file := range report.Files {
			sb.WriteString("- `" + file.File + "` — " + file.Status + "（" + itoa64(file.Chars) + " 字符）\n")
		}
	}
	if len(report.ToolGroups) > 0 {
		sb.WriteString("\n")
		for _, group := range report.ToolGroups {
			sb.WriteString("- 工具组 `" + group.Group + "`：")
			for i, tool := range group.Tools {
				if i > 0 {
					sb.WriteString("、")
				}
				sb.WriteString(tool.Name)
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")
}

// formatLegacyTime 留档时间戳格式化——Unix 毫秒 → 本地「MM-dd HH:mm:ss」（非正值记「—」）。
func formatLegacyTime(ts int64) string {
	if ts <= 0 {
		return "—"
	}
	return time.UnixMilli(ts).Format(legacyTimeLayout)
}

// formatSeconds 秒数保留一位小数（对齐 C# "0.0"）。
func formatSeconds(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', 1, 64)
}

// FindLatestArchive 找该猫最近一份留档——按修改时间降序扫 *.md，文件头 512 字节含 `catKey` 即命中。
func FindLatestArchive(sessionsOldDir, catKey string) string {
	if sessionsOldDir == "" || catKey == "" {
		return ""
	}
	entries, err := os.ReadDir(sessionsOldDir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	files := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{path: filepath.Join(sessionsOldDir, entry.Name()), mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	needle := "`" + catKey + "`"
	for _, file := range files {
		if strings.Contains(readHead(file.path, 512), needle) {
			return file.path
		}
	}
	return ""
}

// TryReadLastLegacyEntry 留档 Markdown → 末条指定类别条目（段头「### MM-dd HH:mm:ss · 用户」）。
//
// 返回段头时刻文本与条目正文；未命中 / 空正文返回 false。
func TryReadLastLegacyEntry(markdown, title string) (timeText, content string, ok bool) {
	if markdown == "" || title == "" {
		return "", "", false
	}
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "### ") || !strings.Contains(line, "· "+title) {
			continue
		}
		header := strings.TrimPrefix(line, "### ")
		if parts := strings.SplitN(header, "·", 2); len(parts) > 0 {
			timeText = strings.TrimSpace(parts[0])
		}
		var body strings.Builder
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "### ") {
				break
			}
			body.WriteString(lines[j])
			body.WriteString("\n")
		}
		content = strings.TrimSpace(body.String())
		if content == "" {
			return "", "", false
		}
		return timeText, content, true
	}
	return "", "", false
}

// readHead 读文件头前 limit 字节（留档扫描判据——不整读大文件）。
func readHead(path string, limit int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	buf := make([]byte, limit)
	n, err := file.Read(buf)
	if n <= 0 || err != nil && n == 0 {
		return ""
	}
	return string(buf[:n])
}
