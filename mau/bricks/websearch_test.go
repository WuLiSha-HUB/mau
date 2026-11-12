package bricks

import (
	"strings"
	"testing"
)

// stubWebConfig 是 WebConfig 的测试替身。
type stubWebConfig struct {
	values map[string]string
	llm    map[string][3]string
}

// Config 读配置值。
func (s *stubWebConfig) Config(key string) string { return s.values[key] }

// LLMConfig 取端点池配置。
func (s *stubWebConfig) LLMConfig(id string) (string, string, string, bool) {
	entry, ok := s.llm[id]
	if !ok {
		return "", "", "", false
	}
	return entry[0], entry[1], entry[2], true
}

// TestWebDSSearchArgs 覆盖 web.ds.search 参数面与配置面。
func TestWebDSSearchArgs(t *testing.T) {
	cfg := &stubWebConfig{values: map[string]string{}, llm: map[string][3]string{}}
	cases := []struct {
		name string
		args string
		want string
	}{
		{"未知键", `{"query":"q","ori_question":"o","bogus":1}`, "ERR|BAD_ARGS|未知参数: bogus"},
		{"缺 query", `{"ori_question":"o"}`, "ERR|BAD_ARGS|缺参数 query"},
		{"count 越界", `{"query":"q","ori_question":"o","count":51}`, "ERR|BAD_ARGS|count 非法值: 51（取值 1～50 的整数）"},
		{"count 非整数", `{"query":"q","ori_question":"o","count":"x"}`, "ERR|BAD_ARGS|count 非法值: x（取值 1～50 的整数）"},
		{"need_content 非法", `{"query":"q","ori_question":"o","need_content":"yes"}`, "ERR|BAD_ARGS|need_content 非法值: yes（true / false）"},
	}
	for _, item := range cases {
		got, ok := WebDSSearch(cfg, item.args)
		if ok {
			t.Fatalf("%s: 期望失败，实际: %s", item.name, got)
		}
		if !strings.Contains(got, item.want) {
			t.Fatalf("%s: 期望含 %q，实际 %q", item.name, item.want, got)
		}
	}
}

// TestWebDSSearchConfigErrors 覆盖鉴权解析的错误面。
func TestWebDSSearchConfigErrors(t *testing.T) {
	if got, ok := WebDSSearch(nil, `{"query":"q","ori_question":"o"}`); ok || got != "ERR|API_NOT_CONFIGURED|宿主未注入 ConfigStore" {
		t.Fatalf("nil 配置口径不符: ok=%v %s", ok, got)
	}
	empty := &stubWebConfig{values: map[string]string{}, llm: map[string][3]string{}}
	if got, ok := WebDSSearch(empty, `{"query":"q","ori_question":"o"}`); ok || !strings.Contains(got, "搜索 API 未配置") {
		t.Fatalf("未配置口径不符: ok=%v %s", ok, got)
	}
	badID := &stubWebConfig{values: map[string]string{"search.api_config_id": "not-a-guid"}, llm: map[string][3]string{}}
	if got, ok := WebDSSearch(badID, `{"query":"q","ori_question":"o"}`); ok || !strings.Contains(got, "不是合法 GUID") {
		t.Fatalf("非法 GUID 口径不符: ok=%v %s", ok, got)
	}
	missing := &stubWebConfig{values: map[string]string{"search.api_config_id": "11111111-1111-1111-1111-111111111111"}, llm: map[string][3]string{}}
	if got, ok := WebDSSearch(missing, `{"query":"q","ori_question":"o"}`); ok || !strings.Contains(got, "LLM 池中找不到该 apiConfigId") {
		t.Fatalf("池未命中口径不符: ok=%v %s", ok, got)
	}
	noKey := &stubWebConfig{
		values: map[string]string{"search.api_config_id": "11111111-1111-1111-1111-111111111111"},
		llm:    map[string][3]string{"11111111-1111-1111-1111-111111111111": {"https://x", "m", ""}},
	}
	if got, ok := WebDSSearch(noKey, `{"query":"q","ori_question":"o"}`); ok || !strings.Contains(got, "密钥未配置") {
		t.Fatalf("缺密钥口径不符: ok=%v %s", ok, got)
	}
}

// TestBuildDSBody 覆盖请求体字段与省略规则。
func TestBuildDSBody(t *testing.T) {
	withOri := buildDSBody("q\"1", "ori", 5, true)
	want := `{"query":"q\"1","ori_question":"ori","count":5,"need_content":true}`
	if withOri != want {
		t.Fatalf("正文不符:\n实际 %s\n期望 %s", withOri, want)
	}
	withoutOri := buildDSBody("q", "", 10, false)
	wantNo := `{"query":"q","count":10,"need_content":false}`
	if withoutOri != wantNo {
		t.Fatalf("省略 ori_question 形态不符:\n实际 %s\n期望 %s", withoutOri, wantNo)
	}
}

// TestParseDSResults 覆盖结果渲染：元信息 / 链接 / 摘要 / 正文截断 / 空数组。
func TestParseDSResults(t *testing.T) {
	raw := `{"results":[
		{"title":"标题一","site_name":"站点甲","date":"2026-11-11T10:00:00Z","authority":3,"rerank_score":8.25,"url":"https://a.example/1","summary":"摘要\n第二行"},
		{"title":"","rerank_score":7,"url":"https://b.example/2","snippet":"片段"},
		{"title":"标题三","content":"` + strings.Repeat("正", 1600) + `"}
	]}`
	rendered, items, avg, parseErr := parseDSResults(raw)
	if parseErr != "" {
		t.Fatalf("解析失败: %s", parseErr)
	}
	if items != 3 {
		t.Fatalf("条数期望 3，实际 %d", items)
	}
	if avg < 7.624 || avg > 7.626 {
		t.Fatalf("均分期望 7.625（未舍入原始值），实际 %v", avg)
	}
	for _, want := range []string{
		"[1] 标题一 (站点甲 · 2026-11-11 · 权威3 · 分8.2)\n    https://a.example/1\n    摘要\n    第二行\n",
		"[2] （无标题） (分7.0)\n    https://b.example/2\n    片段\n",
		"[3] 标题三",
		"[正文截断 共 1600 字]",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("渲染缺片段 %q\n实际：%s", want, rendered)
		}
	}
	if strings.HasSuffix(rendered, "\n") {
		t.Fatalf("渲染尾部应去空行")
	}

	emptyRendered, emptyItems, emptyAvg, emptyErr := parseDSResults(`{"results":[]}`)
	if emptyErr != "" || emptyItems != 0 || emptyAvg != 0 {
		t.Fatalf("空集合处理不符: %q/%d/%v/%q", emptyRendered, emptyItems, emptyAvg, emptyErr)
	}
	if !strings.Contains(emptyRendered, "无匹配结果（0 条）") {
		t.Fatalf("空集合文案不符: %s", emptyRendered)
	}

	if _, _, _, err := parseDSResults(`{"items":[]}`); !strings.Contains(err, "ERR|BAD_RESPONSE|搜索响应缺少 results 数组") {
		t.Fatalf("缺 results 口径不符: %s", err)
	}
	if _, _, _, err := parseDSResults(`not-json`); !strings.Contains(err, "ERR|BAD_RESPONSE|搜索结果解析失败") {
		t.Fatalf("非 JSON 口径不符: %s", err)
	}
}

// TestReadErrorMessage 覆盖错误消息提取与截断回落。
func TestReadErrorMessage(t *testing.T) {
	if got := readErrorMessage(`{"error":{"message":"限速了"}}`); got != "限速了" {
		t.Fatalf("标准错误体不符: %s", got)
	}
	if got := readErrorMessage(""); got != "响应体为空" {
		t.Fatalf("空体不符: %s", got)
	}
	long := strings.Repeat("a", 300)
	if got := readErrorMessage(long); !strings.HasSuffix(got, "...") || utf16Units(got) != 203 {
		t.Fatalf("长文本回落不符: %d 字", utf16Units(got))
	}
}

// TestParseGUIDText 覆盖 GUID 归一。
func TestParseGUIDText(t *testing.T) {
	if got := ParseGUIDText("{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"); got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("归一不符: %s", got)
	}
	if got := ParseGUIDText("not-a-guid"); got != "" {
		t.Fatalf("非法输入应为空串: %s", got)
	}
}
