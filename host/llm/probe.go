package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// 探针哨兵词与超时——C# LlmEndpointProbe.ExpectToken / TimeoutSeconds 逐字。
const (
	// probeExpectToken 哨兵词——探针要求模型原样返回，用于判定「响应内容是否可信」
	probeExpectToken = "PONG"
	// probeTimeout 探针超时——与「请求发出 60 秒无回应即判异常」同口径
	probeTimeout = 60 * time.Second
)

// probeHTTPClient 探针共享客户端——客户端级超时关闭，改由请求级 context 控制
// （C# 每次新建 HttpClient + Timeout 的等价语义；共享客户端保住连接复用）。
var probeHTTPClient = &http.Client{}

// ProbeEndpoint 探测一个端点是否可用——C# LlmEndpointProbe.ProbeAsync 等价。
//
// 判据：HTTP 2xx 且 choices[0].message.content 含哨兵词 = 可用；
// 超时 / 传输失败 / 非 2xx / 非预期内容一律 false——探针自身不抛错，不向调用方传播。
// 消费方：Client 的故障转移判定（同时探「当前站」与「候选站」）。
func ProbeEndpoint(ctx context.Context, endpoint, apiKey, model string) bool {
	if strings.TrimSpace(endpoint) == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodPost, endpoint, strings.NewReader(buildProbeBody(model)))
	if err != nil {
		return false
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// C# StringContent(body, UTF8, "application/json") 的头形态逐字（含 charset）
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	// 自家 apis 入站自报（探针无猫上下文，只发通道头——channel=probe；C# LlmSelfTag.Apply 同口径）
	ApplySelfTag(request, endpoint, nil, "probe")
	response, err := probeHTTPClient.Do(request)
	if err != nil {
		return false
	}
	defer closeBody(response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return false
	}
	return containsExpectToken(string(raw))
}

// buildProbeBody 构造探针请求体——单条 user 消息 + 低 token 上限（非流式便于一次性判定）。
//
// 与 C# BuildProbeBody 逐字节同形：model 为空时不传；非空时按 C# 口径转义 \ 与 "。
func buildProbeBody(model string) string {
	var sb strings.Builder
	sb.WriteString(`{"messages":[{"role":"user","content":"Reply with exactly one word: `)
	sb.WriteString(probeExpectToken)
	sb.WriteString(`"}],"max_tokens":8,"stream":false`)
	if model != "" {
		escaped := strings.ReplaceAll(model, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		sb.WriteString(`,"model":"`)
		sb.WriteString(escaped)
		sb.WriteString(`"`)
	}
	sb.WriteString("}")
	return sb.String()
}

// containsExpectToken 响应体是否含哨兵词——非 JSON / 缺 choices / 缺 content 一律不可用。
func containsExpectToken(raw string) bool {
	if raw == "" {
		return false
	}
	var doc struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return false
	}
	if len(doc.Choices) == 0 {
		return false
	}
	return strings.Contains(strings.ToLower(doc.Choices[0].Message.Content), strings.ToLower(probeExpectToken))
}
