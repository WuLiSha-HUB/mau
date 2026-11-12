package bricks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WebConfig 是 web 类积木的配置依赖（宿主装配期注入；nil = 全部按「未配置」口径）。
type WebConfig interface {
	// Config 读配置值（缺失返回空串）
	Config(key string) string
	// LLMConfig 取端点池配置（endpoint / defaultModel / apiKey；ok=false = 未命中）
	LLMConfig(id string) (endpoint, defaultModel, apiKey string, ok bool)
}

// 工具名常量（A214 头的 tool 字段）。
const (
	toolWebDSSearch = "web-ds-search"
)

// dsSearchEndpoint 是 DeepSeek 官方检索端点（Base URL 固定）。
const dsSearchEndpoint = "https://api.deepseek.com/v1/tools/search"

// dsMaxContentChars 是单条正文截断上限（字符）——need_content 开启时防前文爆量。
const dsMaxContentChars = 1500

// dsRequestTimeoutMs 是请求超时毫秒——纯检索接口，20 秒封顶。
const dsRequestTimeoutMs = 20000

// WebDSSearch 执行 web.ds.search 工具——DeepSeek 官方 Search API 结构化检索（原料直出，不做模型总结）。
func WebDSSearch(cfg WebConfig, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "query ori_question count need_content", "query ori_question", "", ""); badArgs != "" {
		return badArgs, false
	}
	query := Get(argsJSON, "query")
	if query == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断）", false
	}
	oriQuestion := Get(argsJSON, "ori_question")
	if oriQuestion == ParseFail {
		oriQuestion = ""
	}
	count, countErr := readDSCount(argsJSON)
	if countErr != "" {
		return countErr, false
	}
	needContent, needErr := readDSNeedContent(argsJSON)
	if needErr != "" {
		return needErr, false
	}
	apiKey, configErr := resolveSearchAPIKey(cfg)
	if configErr != "" {
		return configErr, false
	}
	body := buildDSBody(query, oriQuestion, count, needContent)
	raw := sendDSSearch(body, apiKey)
	if hasERREPrefix(raw) {
		return raw, true
	}
	rendered, items, avgScore, parseErr := parseDSResults(raw)
	if parseErr != "" {
		return parseErr, true
	}
	headline := query + " | " + strconv.Itoa(items) + " 条结果"
	if items > 0 {
		headline += " · 均分 " + strconv.FormatFloat(roundTo(avgScore, 1), 'f', 1, 64)
	}
	fields := [][2]any{
		{"results", items},
		{"avg_score", roundTo(avgScore, 1)},
		{"chars", utf16Units(rendered)},
	}
	return MetaHeadFields(toolWebDSSearch, true, query, items, fields) + "\n" + headline + "\n" + rendered, true
}

// readDSCount 读取并校验 count——缺省 10；非法返回 ERR|BAD_ARGS 文本。
func readDSCount(argsJSON string) (int, string) {
	raw := Get(argsJSON, "count")
	if raw == "" || raw == ParseFail {
		return 10, ""
	}
	parsed, ok := atoiArg(raw)
	if !ok || parsed < 1 || parsed > 50 {
		return 0, "ERR|BAD_ARGS|count 非法值: " + raw + "（取值 1～50 的整数）"
	}
	return parsed, ""
}

// readDSNeedContent 读取并校验 need_content——缺省 false；仅接受 true / false。
func readDSNeedContent(argsJSON string) (bool, string) {
	raw := Get(argsJSON, "need_content")
	if raw == "" || raw == ParseFail {
		return false, ""
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, ""
	case "false":
		return false, ""
	}
	return false, "ERR|BAD_ARGS|need_content 非法值: " + raw + "（true / false）"
}

// resolveSearchAPIKey 解析鉴权——search.api_config_id → LLM 池配置 → key（web.search 共用）。
func resolveSearchAPIKey(cfg WebConfig) (string, string) {
	if cfg == nil {
		return "", "ERR|API_NOT_CONFIGURED|宿主未注入 ConfigStore"
	}
	rawID := cfg.Config("search.api_config_id")
	if rawID == "" {
		return "", "ERR|API_NOT_CONFIGURED|搜索 API 未配置（配置区 search.api_config_id 需引用 LLM 池配置）"
	}
	normalized := ParseGUIDText(rawID)
	if normalized == "" {
		return "", "ERR|API_NOT_CONFIGURED|搜索 API 配置无效（search.api_config_id 不是合法 GUID）"
	}
	_, _, apiKey, ok := cfg.LLMConfig(normalized)
	if !ok {
		return "", "ERR|API_NOT_CONFIGURED|搜索 API 配置不存在（LLM 池中找不到该 apiConfigId）"
	}
	if apiKey == "" {
		return "", "ERR|API_NOT_CONFIGURED|搜索 API 密钥未配置（LLM 池配置缺 key）"
	}
	return apiKey, ""
}

// buildDSBody 构造请求体——query / ori_question（空则省略）/ count / need_content。
func buildDSBody(query, oriQuestion string, count int, needContent bool) string {
	var b strings.Builder
	b.WriteString(`{"query":`)
	b.WriteString(jsonString(query))
	if oriQuestion != "" {
		b.WriteString(`,"ori_question":`)
		b.WriteString(jsonString(oriQuestion))
	}
	b.WriteString(`,"count":`)
	b.WriteString(strconv.Itoa(count))
	b.WriteString(`,"need_content":`)
	b.WriteString(boolText(needContent))
	b.WriteByte('}')
	return b.String()
}

// sendDSSearch 发送请求——Bearer 鉴权；429 等 1 秒重试一次，仍失败返回 ERR|；其它错误不重试。
func sendDSSearch(body, apiKey string) string {
	first, status := sendDSOnce(body, apiKey)
	if status != http.StatusTooManyRequests {
		return first
	}
	time.Sleep(time.Second)
	second, status := sendDSOnce(body, apiKey)
	if status == http.StatusTooManyRequests {
		return "ERR|HTTP_429|搜索接口限速（账号级 QPS 5，已重试一次）: " + second
	}
	return second
}

// sendDSOnce 单次发送——非 2xx 返回 ERR|HTTP_xxx 文本（status 输出原始状态码）。
func sendDSOnce(body, apiKey string) (string, int) {
	client := &http.Client{Timeout: dsRequestTimeoutMs * time.Millisecond, Transport: newHTTPTransport()}
	req, err := http.NewRequest(http.MethodPost, dsSearchEndpoint, strings.NewReader(body))
	if err != nil {
		return "ERR|HTTP_REQUEST|" + err.Error(), 0
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		if isTimeoutError(err) {
			return "ERR|TIMEOUT|搜索请求超时（" + strconv.Itoa(dsRequestTimeoutMs) + " ms）", 0
		}
		return "ERR|HTTP_TRANSPORT|" + err.Error(), 0
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := readAllLimited(resp.Body, 8<<20)
	if readErr != nil {
		return "ERR|HTTP_TRANSPORT|" + readErr.Error(), resp.StatusCode
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "ERR|HTTP_" + strconv.Itoa(resp.StatusCode) + "|" + readErrorMessage(raw), resp.StatusCode
	}
	return raw, resp.StatusCode
}

// parseDSResults 解析 results[] 并渲染——条数与均分；空数组按合法空集合处理。
func parseDSResults(raw string) (rendered string, items int, avgScore float64, parseErr string) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return "", 0, 0, "ERR|BAD_RESPONSE|搜索结果解析失败: " + err.Error()
	}
	resultsRaw, ok := root["results"]
	if !ok {
		return "", 0, 0, "ERR|BAD_RESPONSE|搜索响应缺少 results 数组"
	}
	var results []json.RawMessage
	if err := json.Unmarshal(resultsRaw, &results); err != nil {
		return "", 0, 0, "ERR|BAD_RESPONSE|搜索响应缺少 results 数组"
	}
	if len(results) == 0 {
		return "无匹配结果（0 条）——可放宽关键词或去掉 site: / after: 等限定", 0, 0, ""
	}
	var b strings.Builder
	scoreSum := 0.0
	scoreCount := 0
	for index, itemRaw := range results {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			continue
		}
		appendDSOne(&b, index+1, item, &scoreSum, &scoreCount)
	}
	if scoreCount > 0 {
		avgScore = scoreSum / float64(scoreCount)
	}
	return strings.TrimRight(b.String(), "\r\n"), len(results), avgScore, ""
}

// appendDSOne 渲染单条结果——标题行（站点 / 日期 / 权威 / 分数）+ 链接 + 摘要 + 正文（截断）。
func appendDSOne(b *strings.Builder, index int, item map[string]json.RawMessage, scoreSum *float64, scoreCount *int) {
	title := rawString(item, "title")
	if title == "" {
		title = "（无标题）"
	}
	b.WriteString("[" + strconv.Itoa(index) + "] " + title)
	meta := make([]string, 0, 4)
	if siteName := rawString(item, "site_name"); siteName != "" {
		meta = append(meta, siteName)
	}
	if date := rawString(item, "date"); len(date) >= 10 {
		meta = append(meta, date[:10])
	}
	if authority, ok := rawInt(item, "authority"); ok && authority >= 0 {
		meta = append(meta, "权威"+strconv.Itoa(authority))
	}
	if score, ok := rawFloat(item, "rerank_score"); ok {
		meta = append(meta, "分"+strconv.FormatFloat(score, 'f', 1, 64))
		*scoreSum += score
		*scoreCount++
	}
	if len(meta) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(meta, " · "))
		b.WriteString(")")
	}
	b.WriteString("\n")
	if targetURL := rawString(item, "url"); targetURL != "" {
		b.WriteString("    " + targetURL + "\n")
	}
	summary := rawString(item, "summary")
	if summary == "" {
		summary = rawString(item, "snippet")
	}
	if summary != "" {
		b.WriteString("    " + strings.ReplaceAll(strings.ReplaceAll(summary, "\r\n", "\n"), "\n", "\n    ") + "\n")
	}
	if content := rawString(item, "content"); content != "" {
		flat := strings.ReplaceAll(content, "\r\n", "\n")
		if utf16Units(flat) > dsMaxContentChars {
			truncated := truncateUTF16Units(flat, dsMaxContentChars)
			b.WriteString("    " + strings.ReplaceAll(truncated, "\n", "\n    "))
			b.WriteString("\n    [正文截断 共 " + strconv.Itoa(utf16Units(flat)) + " 字]\n")
		} else {
			b.WriteString("    " + strings.ReplaceAll(flat, "\n", "\n    ") + "\n")
		}
	}
	b.WriteString("\n")
}

// readErrorMessage 读取错误响应体 message——OpenAI 兼容格式；解析失败回退原文前 200 字。
func readErrorMessage(raw string) string {
	message := ""
	if raw != "" {
		var doc struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err == nil {
			message = doc.Error.Message
		}
	}
	if message == "" && utf16Units(raw) > 200 {
		message = truncateUTF16Units(raw, 200) + "..."
	}
	if message == "" {
		message = "响应体为空"
	}
	return message
}

// ParseGUIDText 归一 GUID 文本为 D 形态（小写 + 短横）；非法返回空串。
func ParseGUIDText(value string) string {
	raw := strings.TrimSpace(value)
	raw = strings.TrimPrefix(raw, "{")
	raw = strings.TrimSuffix(raw, "}")
	if raw == "" {
		return ""
	}
	digits := make([]byte, 0, 32)
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '-' || ch == ' ' || ch == '\t' {
			continue
		}
		if !isHexByte(ch) {
			return ""
		}
		digits = append(digits, ch)
	}
	if len(digits) != 32 {
		return ""
	}
	lower := strings.ToLower(string(digits))
	return lower[0:8] + "-" + lower[8:12] + "-" + lower[12:16] + "-" + lower[16:20] + "-" + lower[20:32]
}

// isHexByte 十六进制字符判定。
func isHexByte(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

// roundTo 四舍五入到指定小数位（对齐 C# Math.Round(x, digits) 的银行家舍入）。
func roundTo(value float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.RoundToEven(value*scale) / scale
}

// isTimeoutError 超时类错误判定。
func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// readAllLimited 读取响应体（上限字节防爆量）。
func readAllLimited(body io.Reader, limit int64) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// rawString 从原始 JSON 对象取字符串字段（非字符串 / 缺失返回空串）。
func rawString(item map[string]json.RawMessage, key string) string {
	raw, ok := item[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// rawInt 从原始 JSON 对象取整型字段。
func rawInt(item map[string]json.RawMessage, key string) (int, bool) {
	raw, ok := item[key]
	if !ok {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return int(value), true
}

// rawFloat 从原始 JSON 对象取浮点字段。
func rawFloat(item map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := item[key]
	if !ok {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}
