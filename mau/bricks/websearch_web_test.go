package bricks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestWebSearchArgs 覆盖 web.search 参数面。
func TestWebSearchArgs(t *testing.T) {
	runner := NewWebSearchRunner(nil, nil, nil)
	cases := []struct {
		name string
		args string
		want string
	}{
		{"未知键", `{"query":"q","bogus":1}`, "ERR|BAD_ARGS|未知参数: bogus"},
		{"缺 query", `{"verify":"true"}`, "ERR|BAD_ARGS|缺参数 query"},
		{"verify 非法", `{"query":"q","verify":"yes"}`, "ERR|BAD_ARGS|verify 非法值: yes（true|false）"},
	}
	for _, item := range cases {
		got, ok := runner.WebSearch(item.args)
		if ok {
			t.Fatalf("%s: 期望失败，实际: %s", item.name, got)
		}
		if !strings.Contains(got, item.want) {
			t.Fatalf("%s: 期望含 %q，实际 %q", item.name, item.want, got)
		}
	}
}

// TestWebSearchAPIMissing 覆盖 API 未配置时的降级失败回执（引擎全部预置冷却，不发真实请求）。
func TestWebSearchAPIMissing(t *testing.T) {
	runner := NewWebSearchRunner(&stubWebConfig{values: map[string]string{}, llm: map[string][3]string{}}, nil, nil)
	runner.throttle.cooldownUntilMs = [webEngineCount]int64{
		time.Now().UnixMilli() + 60000, time.Now().UnixMilli() + 60000,
		time.Now().UnixMilli() + 60000, time.Now().UnixMilli() + 60000,
	}
	got, ok := runner.WebSearch(`{"query":"测试查询"}`)
	if !ok {
		t.Fatalf("网页通道全失败仍是工具成功（ERR 文本作回执），实际失败: %s", got)
	}
	for _, want := range []string{
		"ERR|WEB_CHANNELS_FAILED|搜索 API 未配置；网页通道全部失败——",
		"百度：百度验证冷却中（剩约 ",
		"必应：必应验证冷却中（剩约 ",
		"百度移动版：百度移动版验证冷却中（剩约 ",
		"谷歌：谷歌验证冷却中（剩约 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("回执缺片段 %q\n实际：%s", want, got)
		}
	}
}

// TestWebSearchAPIChain 覆盖 API 通道全链（Responses 协议）——成功回执形态。
func TestWebSearchAPIChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"web_search_call"},{"type":"output_text","text":"答案是 X。[citation:1]\n[citation:1] 来源 - https://a.example"}]}`))
	}))
	defer server.Close()

	const apiID = "11111111-1111-1111-1111-111111111111"
	cfg := &stubWebConfig{
		values: map[string]string{
			"search.api_config_id": apiID,
			"search.endpoint":      server.URL,
		},
		llm: map[string][3]string{apiID: {"https://ignored.example", "deepseek-v4-flash", "secret-key"}},
	}
	runner := NewWebSearchRunner(cfg, nil, nil)
	got, ok := runner.WebSearch(`{"query":"什么是 X"}`)
	if !ok {
		t.Fatalf("期望成功，实际: %s", got)
	}
	for _, want := range []string{
		`{"ok":true,"tool":"web-search","target":"\u4EC0\u4E48\u662F X","protocol":"responses","citations":2,"chars":54}`,
		"什么是 X | 54 字 · 2 引用",
		"答案是 X。[citation:1]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("回执缺片段 %q\n实际：%s", want, got)
		}
	}
}

// TestWebSearchAPIHTTPError 覆盖 API 返回非 2xx 时的错误文本。
func TestWebSearchAPIHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()

	const apiID = "22222222-2222-2222-2222-222222222222"
	cfg := &stubWebConfig{
		values: map[string]string{
			"search.api_config_id": apiID,
			"search.endpoint":      server.URL + "/anthropic/v1/messages",
		},
		llm: map[string][3]string{apiID: {"", "", "k"}},
	}
	runner := NewWebSearchRunner(cfg, nil, nil)
	runner.throttle.cooldownUntilMs = [webEngineCount]int64{
		time.Now().UnixMilli() + 60000, time.Now().UnixMilli() + 60000,
		time.Now().UnixMilli() + 60000, time.Now().UnixMilli() + 60000,
	}
	got, ok := runner.WebSearch(`{"query":"q"}`)
	if !ok {
		t.Fatalf("期望工具成功（降级后仍回执），实际失败: %s", got)
	}
	if !strings.Contains(got, "ERR|WEB_CHANNELS_FAILED|搜索 API 调用失败（rate limited）；网页通道全部失败——") {
		t.Fatalf("降级回执不符: %s", got)
	}
}

// TestBuildSearchBodies 覆盖两协议请求体构造。
func TestBuildSearchBodies(t *testing.T) {
	anthropic := buildAnthropicBody("m1", `q"x`)
	for _, want := range []string{`{"model":"m1","max_tokens":8192,"system":`, `,"messages":[{"role":"user","content":"q\"x"}]`, `"tools":[{"type":"web_search_20250305","name":"web_search"}]}`} {
		if !strings.Contains(anthropic, want) {
			t.Fatalf("Anthropic 请求体缺片段 %q\n实际：%s", want, anthropic)
		}
	}
	responses := buildResponsesBody("m2", "q")
	for _, want := range []string{`"instructions":`, `,"input":[{"role":"user","content":"q"}]`, `"tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`} {
		if !strings.Contains(responses, want) {
			t.Fatalf("Responses 请求体缺片段 %q\n实际：%s", want, responses)
		}
	}
}

// TestParseAnthropicResponse 覆盖 Anthropic 解析三态。
func TestParseAnthropicResponse(t *testing.T) {
	normal := `{"content":[{"type":"server_tool_use"},{"type":"text","text":"回答"}],"stop_reason":"end_turn"}`
	if got := parseAnthropicResponse(normal); got != "回答" {
		t.Fatalf("正常解析不符: %q", got)
	}
	noSearch := `{"content":[{"type":"text","text":"没搜"}]}`
	if got := parseAnthropicResponse(noSearch); !strings.Contains(got, "ERR|NO_SEARCH|模型未触发 web_search（Anthropic 响应无 server_tool_use 块）") {
		t.Fatalf("假搜索检测不符: %s", got)
	}
	truncatedWithText := `{"content":[{"type":"server_tool_use"},{"type":"text","text":"部分"}],"stop_reason":"max_tokens"}`
	if got := parseAnthropicResponse(truncatedWithText); !strings.HasSuffix(got, "[搜索响应截断——服务端达到输出上限；以上内容为部分结果]") {
		t.Fatalf("有文本截断不符: %s", got)
	}
	truncatedSources := `{"content":[{"type":"server_tool_use"},{"type":"web_search_tool_result","content":[{"title":"甲","url":"https://a"},{"url":"https://b"}]}],"stop_reason":"max_tokens"}`
	got := parseAnthropicResponse(truncatedSources)
	for _, want := range []string{"[citation:1] 甲 - https://a\n", "[citation:2] https://b\n", "[搜索已执行，但回答生成被输出上限截断"} {
		if !strings.Contains(got, want) {
			t.Fatalf("来源回落缺片段 %q\n实际：%s", want, got)
		}
	}
	emptyTruncated := `{"content":[{"type":"server_tool_use"}],"stop_reason":"max_tokens"}`
	if got := parseAnthropicResponse(emptyTruncated); got != "ERR|TRUNCATED|搜索响应被截断且无可用输出文本" {
		t.Fatalf("空截断不符: %s", got)
	}
}

// TestParseResponsesResponse 覆盖 Responses 解析四态。
func TestParseResponsesResponse(t *testing.T) {
	cases := map[string]string{
		`{"status":"completed","output":[{"type":"web_search_call"},{"type":"output_text","text":"答"}]}`: "答",
		`{"status":"failed"}`:                              "ERR|SEARCH_FAILED|搜索请求失败（服务端 status=failed）",
		`{"status":"incomplete"}`:                          "ERR|TRUNCATED|搜索响应被截断（服务端 status=incomplete）",
		`{"output":[{"type":"output_text","text":"无搜索"}]}`: "ERR|NO_SEARCH|模型未触发 web_search（Responses 响应无 web_search_call 项）",
	}
	for input, want := range cases {
		if got := parseResponsesResponse(input); got != want {
			t.Fatalf("解析 %q 期望 %q，实际 %q", input, want, got)
		}
	}
}

// TestParseBaiduInto 覆盖百度结果页解析。
func TestParseBaiduInto(t *testing.T) {
	html := `<html><body>
<div class="result c-container" mu="https://a.example/1"><h3><a href="/link1">标题一</a></h3><div class="c-abstract">这是一个足够长的摘要文本内容一，用于验证解析逻辑是否正确工作。</div></div>
<div class="result c-container" mu="https://b.example/2"><h3><a href="/link2">标题二</a></h3></div>
<div class="result c-container" mu="https://a.example/1"><h3>重复链接</h3></div>
<div class="result c-container" mu="/relative"><h3>相对链接</h3></div>
</body></html>`
	var list strings.Builder
	rows := parseBaiduInto(html, 8, &list)
	if rows != 2 {
		t.Fatalf("解析条数期望 2，实际 %d：%s", rows, list.String())
	}
	for _, want := range []string{"[1] 标题一\n    https://a.example/1\n", "[2] 标题二\n    https://b.example/2\n"} {
		if !strings.Contains(list.String(), want) {
			t.Fatalf("清单缺片段 %q\n实际：%s", want, list.String())
		}
	}
}

// TestParseBingInto 覆盖必应结果页解析。
func TestParseBingInto(t *testing.T) {
	html := `<html><body>
<li class="b_algo"><h2><a href="https://a.example/1">标题一</a></h2><p>摘要一</p></li>
<li class="b_algo"><h2><a href="https://b.example/2">标题二</a></h2></li>
<li class="b_algo"><h2><a href="https://a.example/1">重复</a></h2></li>
</body></html>`
	var list strings.Builder
	rows := parseBingInto(html, 8, &list)
	if rows != 2 {
		t.Fatalf("解析条数期望 2，实际 %d：%s", rows, list.String())
	}
	for _, want := range []string{"[1] 标题一\n    https://a.example/1\n    摘要一\n", "[2] 标题二\n    https://b.example/2\n"} {
		if !strings.Contains(list.String(), want) {
			t.Fatalf("清单缺片段 %q\n实际：%s", want, list.String())
		}
	}
}

// TestSearchHelpers 覆盖清洗 / 摘要 / 引用计数 / 通道展示名。
func TestSearchHelpers(t *testing.T) {
	if got := decodeHTML("<b>甲</b>&amp;乙&nbsp;丙  \n 丁"); got != "甲 &乙 丙 丁" {
		t.Fatalf("decodeHTML 不符: %q", got)
	}
	if got := shortErr("ERR|HTTP_500|服务 端 错误"); got != "服务 端 错误" {
		t.Fatalf("shortErr 剥前缀不符: %q", got)
	}
	if got := shortErr("ERR|NO_CODE"); got != "ERR|NO_CODE" {
		t.Fatalf("shortErr 无第二竖线不符: %q", got)
	}
	if got := countCitations("a[citation:1]b[citation:2][citation:3]"); got != 3 {
		t.Fatalf("countCitations 不符: %d", got)
	}
	labels := map[string]string{"web-baidu": "百度", "web-bing": "必应", "web-baidu-mobile": "百度移动版", "web-google": "谷歌", "x": "x"}
	for input, want := range labels {
		if got := webProtocolLabel(input); got != want {
			t.Fatalf("webProtocolLabel(%q) 期望 %q，实际 %q", input, want, got)
		}
	}
}

// TestWebThrottleCooldown 覆盖节流冷却与成功复位。
func TestWebThrottleCooldown(t *testing.T) {
	runner := NewWebSearchRunner(&stubWebConfig{values: map[string]string{"search.web_min_interval_ms": "0"}}, nil, nil)
	runner.throttle.cooldownUntilMs[webEngineBaidu] = time.Now().UnixMilli() + 120000
	ready, note := runner.webThrottleReady(webEngineBaidu, "百度")
	if ready || !strings.Contains(note, "百度验证冷却中（剩约 3 分钟）") {
		t.Fatalf("冷却判定不符: ready=%v note=%q", ready, note)
	}
	runner.throttle.cooldownUntilMs[webEngineBaidu] = time.Now().UnixMilli() - 1
	ready, note = runner.webThrottleReady(webEngineBaidu, "百度")
	if !ready || note != "" {
		t.Fatalf("冷却到期应放行: ready=%v note=%q", ready, note)
	}
}

// TestWebSearchMinIntervalClamp 覆盖节流配置夹取。
func TestWebSearchMinIntervalClamp(t *testing.T) {
	cases := map[string]int64{"": 3000, "0": 0, "5000": 5000, "-1": 3000, "999999": 60000, "abc": 3000}
	for input, want := range cases {
		values := map[string]string{}
		if input != "" {
			values["search.web_min_interval_ms"] = input
		}
		runner := NewWebSearchRunner(&stubWebConfig{values: values}, nil, nil)
		if got := runner.webMinIntervalMs(); got != want {
			t.Fatalf("webMinIntervalMs(%q) 期望 %d，实际 %d", input, want, got)
		}
	}
	cooldown := map[string]int64{"": 900000, "1000": 60000, "120000": 120000, "999999999": 86400000}
	for input, want := range cooldown {
		values := map[string]string{}
		if input != "" {
			values["search.web_cooldown_ms"] = input
		}
		runner := NewWebSearchRunner(&stubWebConfig{values: values}, nil, nil)
		if got := runner.webCooldownMs(); got != want {
			t.Fatalf("webCooldownMs(%q) 期望 %d，实际 %d", input, want, got)
		}
	}
}
