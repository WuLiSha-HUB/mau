package bricks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cathome5/mau/runtime"
)

// llmSimpleTimeoutMs 一次性请求超时毫秒（对齐 C# BRIK-LLM-007 的 TimeoutMs 声明）。
const llmSimpleTimeoutMs = 120000

// llmSimpleSessionHeader opencode 系网关会话路由头——固定值，与 C# 逐字一致。
//
// 🔴 LlmSelfTag 的缓存纪律：该固定会话值换掉即换上游缓存槽，历史缓存立即失效——
// 这条优先级高于「产品标识改名」，不是笔误。
const llmSimpleSessionHeader = "cat-home4-simple"

// llmSimpleNoService 服务未注入的具名错误（C# 积木是静态类，Go 侧服务可 nil）。
const llmSimpleNoService = "ERR|LLM_NO_SERVICE|宿主未注入 LLMSimpleService"

// LLMSimpleService 是一次性 LLM 请求的执行面（BRIK-LLM-007/008/009 的 Go 平移）。
//
// 职责边界：给定 endpoint/apikey/model 直接发一次 OpenAI 兼容非流式请求，结果写全局段盒
// llm_simple_reply:<requestId>（文本或 ERR|）+ llm_simple_done:<requestId>（"1"=已结算）；
// 主线程入口立即返回，HTTP 调用在后台 goroutine（语料侧用 llm.simple_ready 轮询结算）。
//
// 与 host/llm 的分工：本服务**不复用端点池**——endpoint/apikey/model 由工单参数给（语料 T_Read 五键），
// 属「调用方自带凭据」的一次性通道；端点池只服务宿主自身的 chat / vision 链路。
type LLMSimpleService struct {
	// box 全局段盒（scope=global；跨 goroutine 安全由 DataBox 自身保证）
	box *runtime.DataBox
	// client HTTP 客户端（nil 回落 http.DefaultClient；超时按每次请求 context 控制）
	client *http.Client
	// selfTag 入站自报头注入（nil = 不发；只在目标是自家 apis 时由实现方判定并附加）
	selfTag func(request *http.Request, endpoint string)
	// logf 日志出口（category 由组合根固定为 LLM）
	logf func(level int, message string)
}

// NewLLMSimpleService 构造一次性 LLM 请求服务。
//
// box 为全局段盒（必有）；selfTag / logf 可为 nil（分别表示不自报、不出声）。
func NewLLMSimpleService(box *runtime.DataBox, selfTag func(request *http.Request, endpoint string), logf func(level int, message string)) *LLMSimpleService {
	return &LLMSimpleService{box: box, client: &http.Client{}, selfTag: selfTag, logf: logf}
}

// SimpleRequest 启动一次性 LLM 请求——后台执行并立即返回。
//
// 返回值的 ok 语义严格对齐 C#：requestId 非法 → false（无段可落，零副作用）；
// endpoint/apikey/model 任一为空 → 失败盒已落 + false；其余 → 已受理 true。
func (s *LLMSimpleService) SimpleRequest(endpoint, apikey, model, system, content, requestID string) (string, bool) {
	if s == nil || s.box == nil {
		return "false", false
	}
	id, ok := parseOfficeID(requestID)
	if !ok {
		return "false", false
	}
	seg := itoa64(id)
	if endpoint == "" || apikey == "" || model == "" {
		badArgs := "ERR|BAD_ARGS|一次性 LLM 请求缺 endpoint/apikey/model"
		s.setBox("llm_simple_reply:"+seg, badArgs)
		s.setBox("llm_simple_done:"+seg, "1")
		s.log(2, badArgs+"（requestId="+seg+"）")
		return "false", false
	}
	// 启动清理——本请求段盒基线干净（reply 清空 + done 清空）
	s.setBox("llm_simple_reply:"+seg, "")
	s.setBox("llm_simple_done:"+seg, "")
	go s.doRequest(endpoint, apikey, model, system, content, seg)
	return "true", true
}

// SimpleReady 完成探测——段盒 llm_simple_done:<requestId> 已置 "1"。
//
// 语义：ok = 已结算（成功与失败都算，失败文本在 reply 盒里）；requestId 非法 / 未结算 → false。
func (s *LLMSimpleService) SimpleReady(requestID string) (string, bool) {
	if s == nil || s.box == nil {
		return "false", false
	}
	id, ok := parseOfficeID(requestID)
	if !ok {
		return "false", false
	}
	if s.box.GetString("global", "llm_simple_done:"+itoa64(id)) == "1" {
		return "true", true
	}
	return "false", false
}

// SimpleRead 取结果——读段盒 llm_simple_reply:<requestId>；缺失 / 空串 → false。
//
// 失败侧的 ERR| 文本同样在值里（成功与失败由文本内容区分——失败可见）。
func (s *LLMSimpleService) SimpleRead(requestID string) (string, bool) {
	if s == nil || s.box == nil {
		return "", false
	}
	id, ok := parseOfficeID(requestID)
	if !ok {
		return "", false
	}
	text := s.box.GetString("global", "llm_simple_reply:"+itoa64(id))
	if text != "" {
		return text, true
	}
	return "", false
}

// doRequest 后台执行——HTTP POST → 解析 → 段盒落盘（完成标志置位）。
//
// 日志分级对齐 C#：解析类失败（ERR|PARSE / ERR|HTTP_xxx）走「完成」0 级；
// 只有真异常（网络 / 超时）走「异常」3 级。
func (s *LLMSimpleService) doRequest(endpoint, apikey, model, system, content, seg string) {
	text, err := s.execute(endpoint, apikey, model, system, content)
	if err != nil {
		typeName, message := classifyLLMError(err)
		s.setBox("llm_simple_reply:"+seg, "ERR|"+typeName+"|"+message)
		s.setBox("llm_simple_done:"+seg, "1")
		s.log(3, "一次性请求异常（requestId="+seg+" · "+typeName+"）："+trimLLMText(message, 200))
		return
	}
	s.setBox("llm_simple_reply:"+seg, text)
	s.setBox("llm_simple_done:"+seg, "1")
	s.log(0, "一次性请求完成：requestId="+seg+" · "+strconv.Itoa(utf16Units(text))+" 字符")
}

// execute 单次 HTTP 往返——非 2xx 与解析失败都算「已结算」（返回文本，error 为 nil）。
func (s *LLMSimpleService) execute(endpoint, apikey, model, system, content string) (string, error) {
	target := buildLLMURL(endpoint)
	body := buildLLMSimpleBody(model, system, content)
	ctx, cancel := context.WithTimeout(context.Background(), llmSimpleTimeoutMs*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+apikey)
	// opencode 系网关会话路由头——缺失即 400（MissingSessionID）；一次性请求用固定命名空间即可
	request.Header.Set("x-opencode-session", llmSimpleSessionHeader)
	// 自家 apis 入站自报（积木无猫上下文，只发通道头——与 C# tag==null 分支同口径）
	if s.selfTag != nil {
		s.selfTag(request, target)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	raw, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return "", readErr
	}
	text := string(raw)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "ERR|HTTP_" + strconv.Itoa(response.StatusCode) + "|" + trimLLMText(text, 300), nil
	}
	return parseLLMSimpleText(text), nil
}

// setBox 写全局段盒（box 缺失静默跳过——构造期已保证非 nil）。
func (s *LLMSimpleService) setBox(key, value string) {
	if s == nil || s.box == nil {
		return
	}
	s.box.Set("global", key, value)
}

// log 出声（nil 出口静默）。
func (s *LLMSimpleService) log(level int, message string) {
	if s == nil || s.logf == nil {
		return
	}
	s.logf(level, message)
}

// classifyLLMError 把 Go 错误映射为 C# 异常类名的等价分类。
//
// 类型名是语言差异（C# TaskCanceledException / HttpRequestException），
// 映射只为让 ERR| 文本的分段形态一致（第三段 message 天然各自运行时文案）。
func classifyLLMError(err error) (string, string) {
	if err == nil {
		return "HttpRequestException", ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "TaskCanceledException", err.Error()
	}
	return "HttpRequestException", err.Error()
}

// buildLLMURL 构造请求 URL——端点含 /chat/completions 原样；否则补 /chat/completions（base_url 形态）。
func buildLLMURL(endpoint string) string {
	target := strings.TrimSpace(endpoint)
	if strings.Contains(strings.ToLower(target), "/chat/completions") {
		return target
	}
	if strings.HasSuffix(target, "/") {
		return target + "chat/completions"
	}
	return target + "/chat/completions"
}

// buildLLMSimpleBody 构造请求体——OpenAI 兼容 chat/completions（system + user 两条消息；stream=false）。
//
// 字段序与 C# BRIK-LLM-007 逐字一致（model / stream / messages）。
func buildLLMSimpleBody(model, system, content string) string {
	var b strings.Builder
	b.WriteString(`{"model":`)
	b.WriteString(llmJSONStr(model))
	b.WriteString(`,"stream":false,"messages":[{"role":"system","content":`)
	b.WriteString(llmJSONStr(system))
	b.WriteString(`},{"role":"user","content":`)
	b.WriteString(llmJSONStr(content))
	b.WriteString(`}]}`)
	return b.String()
}

// llmJSONStr 把字符串编码成 JSON 字面量——转义口径逐字复现 Mau.Runtime.TextUtil.JsonEscape：
// 短转义 + 控制字符 \u00xx（小写），**非 ASCII 与 HTML 敏感字符原样直出**。
//
// 🔴 与包内 jsonString（A214 头用，非 ASCII 走 \uXXXX）刻意不同口径：请求体字节要与 C# 对齐。
func llmJSONStr(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 8)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < ' ' {
				b.WriteString(`\u`)
				b.WriteString(padHex4(int64(r)))
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// padHex4 小写四位十六进制（对齐 C# ToString("x4")）。
func padHex4(value int64) string {
	text := strconv.FormatInt(value, 16)
	for len(text) < 4 {
		text = "0" + text
	}
	return text
}

// parseLLMSimpleText 解析响应——choices[0].message.content；失败返回 ERR| 文本。
func parseLLMSimpleText(raw string) string {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return "ERR|PARSE|响应 JSON 解析失败"
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return "ERR|EMPTY_RESULT|响应非对象"
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		return "ERR|EMPTY_RESULT|响应无 choices"
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return "ERR|EMPTY_RESULT|响应无 message"
	}
	message, ok := first["message"].(map[string]any)
	if !ok {
		return "ERR|EMPTY_RESULT|响应无 message"
	}
	content, _ := message["content"].(string)
	if content == "" {
		// 空内容不是有效回执：原样落空串会让调用方按「未就绪」无限轮询（C# A287 同口径）
		return "ERR|EMPTY_CONTENT|模型返回空内容"
	}
	return content
}

// trimLLMText 按 UTF-16 码元截断日志 / 错误文本——超限补省略号（对齐 C# TrimText）。
func trimLLMText(text string, limit int) string {
	if utf16Units(text) <= limit {
		return text
	}
	return truncateUTF16Units(text, limit) + "..."
}

// LLMSimpleRequest 复现 llm.simple_request——启动后台一次性调用（六参：endpoint / apikey / model / system / content / office）。
func LLMSimpleRequest(svc *LLMSimpleService, endpoint, apikey, model, system, content, requestID string) (string, bool) {
	if svc == nil {
		return llmSimpleNoService, false
	}
	return svc.SimpleRequest(endpoint, apikey, model, system, content, requestID)
}

// LLMSimpleReady 复现 llm.simple_ready——完成探测（ok = 段盒已结算）。
func LLMSimpleReady(svc *LLMSimpleService, requestID string) (string, bool) {
	if svc == nil {
		return llmSimpleNoService, false
	}
	return svc.SimpleReady(requestID)
}

// LLMSimpleRead 复现 llm.simple_read——取结果文本（ok = 键存在且非空）。
func LLMSimpleRead(svc *LLMSimpleService, requestID string) (string, bool) {
	if svc == nil {
		return llmSimpleNoService, false
	}
	return svc.SimpleRead(requestID)
}
