package bricks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTPArgs 覆盖三件 HTTP 积木的参数面零容忍。
func TestHTTPArgs(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) (string, bool)
		args string
		want string
	}{
		{"get 未知键", HTTPGet, `{"url":"http://127.0.0.1:1/x","bogus":1}`, "ERR|BAD_ARGS|未知参数: bogus"},
		{"get 缺 url", HTTPGet, `{}`, "ERR|BAD_ARGS|缺参数 url"},
		{"get 空 url", HTTPGet, `{"url":""}`, "ERR|BAD_ARGS|缺参数 url"},
		{"get timeout 非整数", HTTPGet, `{"url":"http://127.0.0.1:1/x","timeout_ms":"abc"}`, "参数 timeout_ms 非整数: abc"},
		{"get follow 非布尔", HTTPGet, `{"url":"http://127.0.0.1:1/x","follow":"maybe"}`, "参数 follow 非布尔: maybe"},
		{"get max_body 非整数", HTTPGet, `{"url":"http://127.0.0.1:1/x","max_body":"-"}`, "参数 max_body 非整数: -"},
		{"post 缺 url", HTTPPost, `{"body":"x"}`, "ERR|BAD_ARGS|缺参数 url"},
		{"probe 未知键", HTTPProbe, `{"url":"http://127.0.0.1:1/x","max_body":0}`, "ERR|BAD_ARGS|未知参数: max_body"},
		{"非对象载荷", HTTPGet, `"plain"`, "ERR|BAD_ARGS|参数必须是 JSON 对象"},
	}
	for _, item := range cases {
		got, ok := item.fn(item.args)
		if ok {
			t.Fatalf("%s: 期望失败，实际成功: %s", item.name, got)
		}
		if !strings.Contains(got, item.want) {
			t.Fatalf("%s: 期望含 %q，实际 %q", item.name, item.want, got)
		}
	}
}

// TestHTTPArgsInvalidURL 覆盖 url 方案校验（参数面错误走 InvalidDataException 口径）。
func TestHTTPArgsInvalidURL(t *testing.T) {
	got, ok := HTTPGet(`{"url":"ftp://example.com/x"}`)
	if ok {
		t.Fatalf("期望失败，实际成功: %s", got)
	}
	if !strings.HasPrefix(got, "ERR|InvalidDataException|url 须为 http:// 或 https:// 绝对地址:") {
		t.Fatalf("回执形态不符: %q", got)
	}
}

// TestHTTPGetReal 覆盖 GET 全链：状态 / 正文 / 头 / A214 头字段。
func TestHTTPGetReal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Echo", r.Method)
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()

	got, ok := HTTPGet(`{"url":"` + server.URL + `/a"}`)
	if !ok {
		t.Fatalf("期望成功，实际: %s", got)
	}
	for _, want := range []string{
		`{"ok":true,"tool":"http-get"`,
		`"status":200`,
		`"chars":11`,
		`"elapsed_ms":`,
		" | 200 OK · 11 字符 · ",
		"\n请求: GET " + server.URL + "/a",
		"\n响应: 200 OK · text/plain; charset=utf-8 · 11 字节",
		"\n正文:\nhello world",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("回执缺片段 %q\n实际：%s", want, got)
		}
	}
	if strings.Contains(got, "[截断]") {
		t.Fatalf("未截断却出现截断提示: %s", got)
	}
}

// TestHTTPGetTruncate 覆盖正文截断与 N0 计数。
func TestHTTPGetTruncate(t *testing.T) {
	payload := strings.Repeat("字", 5000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	got, ok := HTTPGet(`{"url":"` + server.URL + `/t","max_body":10}`)
	if !ok {
		t.Fatalf("期望成功，实际: %s", got)
	}
	for _, want := range []string{
		`"chars":5000`,
		" · 5,000 字符 · ",
		"\n[截断] 正文 5,000 字符，已列 10 字符（max_body 提额可看全 / 0 = 只取响应头）",
		"\n正文:\n" + strings.Repeat("字", 10),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("回执缺片段 %q\n实际：%s", want, got)
		}
	}
	if strings.Contains(got, strings.Repeat("字", 11)) {
		t.Fatalf("正文未按 max_body 截断")
	}
}

// TestHTTPBodyCharsUTF16 覆盖 UTF-16 码元口径（emoji 计 2）。
func TestHTTPBodyCharsUTF16(t *testing.T) {
	if got := utf16Units("a🔴b"); got != 4 {
		t.Fatalf("utf16Units 期望 4，实际 %d", got)
	}
	if got := truncateUTF16Units("a🔴b", 2); got != "a" {
		t.Fatalf("代理对半切期望丢弃，实际 %q", got)
	}
	if got := truncateUTF16Units("a🔴b", 3); got != "a🔴" {
		t.Fatalf("整代理对应保留，实际 %q", got)
	}
}

// TestHTTPStatusCodes 覆盖 4xx / 5xx 属有效响应（工具仍成功）。
func TestHTTPStatusCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	for _, item := range []struct {
		path   string
		status string
	}{{"missing", "\n响应: 404 Not Found"}, {"boom", "\n响应: 500 Internal Server Error"}} {
		got, ok := HTTPGet(`{"url":"` + server.URL + "/" + item.path + `"}`)
		if !ok {
			t.Fatalf("%s: 4xx/5xx 应算成功，实际: %s", item.path, got)
		}
		if !strings.Contains(got, item.status) {
			t.Fatalf("%s: 缺状态片段 %q\n实际：%s", item.path, item.status, got)
		}
	}
}

// TestHTTPMaxBodyZero 覆盖 max_body=0（只取响应头）。
func TestHTTPMaxBodyZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should not appear"))
	}))
	defer server.Close()

	got, ok := HTTPGet(`{"url":"` + server.URL + `","max_body":0}`)
	if !ok {
		t.Fatalf("期望成功，实际: %s", got)
	}
	if !strings.Contains(got, "\n[正文] 未读取（max_body=0——只取响应头）") {
		t.Fatalf("缺 max_body=0 提示：%s", got)
	}
	if strings.Contains(got, "should not appear") {
		t.Fatalf("max_body=0 不应读取正文：%s", got)
	}
}

// TestHTTPPost 覆盖 POST 正文与回执头部行。
func TestHTTPPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		_, _ = w.Write([]byte("echo:" + string(buf[:n])))
	}))
	defer server.Close()

	got, ok := HTTPPost(`{"url":"` + server.URL + `/p","body":"hi","content_type":"text/plain"}`)
	if !ok {
		t.Fatalf("期望成功，实际: %s", got)
	}
	for _, want := range []string{
		`{"ok":true,"tool":"http-post"`,
		"\n请求: POST " + server.URL + "/p（正文 2 字符 · text/plain）",
		"\n正文:\necho:hi",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("回执缺片段 %q\n实际：%s", want, got)
		}
	}
}

// TestHTTPGetUnreachable 覆盖连不上（GET 算工具失败）。
func TestHTTPGetUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	got, ok := HTTPGet(`{"url":"` + url + `/x","timeout_ms":2000}`)
	if ok {
		t.Fatalf("期望失败，实际: %s", got)
	}
	if !strings.HasPrefix(got, "ERR|HTTP_UNREACHABLE|") {
		t.Fatalf("回执形态不符: %q", got)
	}
}

// TestHTTPProbe 覆盖探测三态：可达 / 不可达 / 重定向不跟随。
func TestHTTPProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("body"))
	}))
	defer server.Close()

	got, ok := HTTPProbe(`{"url":"` + server.URL + `/a"}`)
	if !ok || !strings.Contains(got, `{"ok":true,"tool":"http-probe"`) || !strings.Contains(got, " | 200 OK · ") {
		t.Fatalf("可达探测不符: ok=%v %s", ok, got)
	}

	redirected, ok := HTTPProbe(`{"url":"` + server.URL + `/moved"}`)
	if !ok {
		t.Fatalf("重定向探测期望成功，实际: %s", redirected)
	}
	for _, want := range []string{
		"\n响应: 302 Found",
		"\n[重定向] 未跟随（follow=false）· Location: " + server.URL + "/target",
		"\n[提示] 加 follow=true 可跟到终点看最终状态码",
	} {
		if !strings.Contains(redirected, want) {
			t.Fatalf("缺片段 %q\n实际：%s", want, redirected)
		}
	}

	followed, ok := HTTPProbe(`{"url":"` + server.URL + `/moved","follow":true}`)
	if !ok || !strings.Contains(followed, "\n[重定向] 已跟随 → "+server.URL+"/target") {
		t.Fatalf("跟随探测不符: ok=%v %s", ok, followed)
	}

	broken := server.URL
	server.Close()
	unreachable, ok := HTTPProbe(`{"url":"` + broken + `/a","timeout_ms":2000}`)
	if !ok {
		t.Fatalf("不可达属探测结论，应算成功: %s", unreachable)
	}
	if !strings.Contains(unreachable, `{"ok":true,"tool":"http-probe"`) || !strings.Contains(unreachable, "不可达 · ") || !strings.Contains(unreachable, "\n原因: ") {
		t.Fatalf("不可达回执形态不符: %s", unreachable)
	}
}

// TestFormatN0 覆盖千分位渲染。
func TestFormatN0(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4567: "-4,567"}
	for input, want := range cases {
		if got := formatN0(input); got != want {
			t.Fatalf("formatN0(%d) 期望 %q，实际 %q", input, want, got)
		}
	}
}
