package bricks

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// newSvc 建带临时根的服务。
func newSvc(t *testing.T) (*fs.Service, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fs.Service{Roots: []fs.Root{{ID: "w", Path: root, Writable: true}}}, root
}

// TestValidate 参数面零容忍。
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		allowed string
		want    string
	}{
		{"正常", `{"path":"a"}`, "path", ""},
		{"未知参数", `{"path":"a","x":1}`, "path", "ERR|BAD_ARGS|未知参数: x（支持 path）"},
		{"catId 放行", `{"path":"a","catId":"c1"}`, "path", ""},
		{"缺必填", `{"x":1}`, "path x", "ERR|BAD_ARGS|缺参数 path（必填：path x）"},
		{"空串视同缺失", `{"path":""}`, "path", "ERR|BAD_ARGS|缺参数 path（必填：path）"},
		{"非对象", `[1,2]`, "path", "ERR|BAD_ARGS|参数必须是 JSON 对象"},
		{"空载荷", ``, "path", "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Validate(c.args, c.allowed, c.allowed, "", "")
			if got != c.want {
				t.Fatalf("Validate = %q，期望 %q", got, c.want)
			}
		})
	}
}

// TestValidateEnum 枚举校验。
func TestValidateEnum(t *testing.T) {
	bad := Validate(`{"mode":"weird"}`, "mode", "", "mode", "exact|all")
	if bad != "ERR|BAD_ARGS|mode 非法值: weird（exact|all）" {
		t.Fatalf("枚举校验 = %q", bad)
	}
	if got := Validate(`{"mode":"all"}`, "mode", "", "mode", "exact|all"); got != "" {
		t.Fatalf("合法枚举应放行，实际 %q", got)
	}
}

// TestGet 取参——字符串 / 非字符串 / 缺失 / 解析失败。
func TestGet(t *testing.T) {
	if got := Get(`{"a":"x"}`, "a"); got != "x" {
		t.Fatalf("字符串取值 = %q", got)
	}
	if got := Get(`{"a":123}`, "a"); got != "123" {
		t.Fatalf("数字原文 = %q", got)
	}
	if got := Get(`{"a":"x"}`, "b"); got != "" {
		t.Fatalf("缺失应为空串，实际 %q", got)
	}
	if got := Get(`notjson`, "a"); got != ParseFail {
		t.Fatalf("解析失败应为 ParseFail，实际 %q", got)
	}
}

// TestMetaHead 头格式与键序。
func TestMetaHead(t *testing.T) {
	got := MetaHead("text-read", true, "w:a.md", 3)
	want := `{"ok":true,"tool":"text-read","target":"w:a.md","items":3}`
	if got != want {
		t.Fatalf("MetaHead = %s，期望 %s", got, want)
	}
	if got := MetaHead("x", true, "", -1); got != `{"ok":true,"tool":"x"}` {
		t.Fatalf("省略形态 = %s", got)
	}
}

// TestTextReadWriteAppend 端到端读写。
func TestTextReadWriteAppend(t *testing.T) {
	svc, _ := newSvc(t)
	out, ok := TextWrite(svc, `{"path":"w:a.md","content":"hello\nworld"}`)
	if !ok {
		t.Fatalf("写入失败: %s", out)
	}
	if !strings.HasPrefix(out, `{"ok":true,"tool":"text-write","target":"w:a.md","items":2}`) {
		t.Fatalf("写回执头 = %s", out)
	}
	if !strings.Contains(out, "已覆写 · 2 行 · UTF-8 BOM / LF") {
		t.Fatalf("写回执摘要 = %s", out)
	}

	read, ok := TextRead(svc, `{"path":"w:a.md"}`)
	if !ok {
		t.Fatalf("读取失败: %s", read)
	}
	if !strings.HasSuffix(read, "w:a.md | 2 行\nhello\nworld") {
		t.Fatalf("读回执 = %q", read)
	}

	app, ok := TextAppend(svc, `{"path":"w:a.md","content":"\nthird"}`)
	if !ok {
		t.Fatalf("追加失败: %s", app)
	}
	if !strings.Contains(app, "已追加 · 2 行") {
		t.Fatalf("追加回执 = %s", app)
	}
}

// TestTextReadLines 行号区间。
func TestTextReadLines(t *testing.T) {
	svc, _ := newSvc(t)
	TextWrite(svc, `{"path":"w:l.txt","content":"a\nb\nc"}`)
	out, ok := TextReadLines(svc, `{"path":"w:l.txt","start":2,"end":2}`)
	if !ok {
		t.Fatalf("读行失败: %s", out)
	}
	if !strings.HasSuffix(out, "w:l.txt | 1 行\n2: b") {
		t.Fatalf("读行结果 = %q", out)
	}
}

// TestTextReadBetween 锚点区间。
func TestTextReadBetween(t *testing.T) {
	svc, _ := newSvc(t)
	TextWrite(svc, `{"path":"w:b.txt","content":"head\nA\nB\ntail"}`)
	out, ok := TextReadBetween(svc, `{"path":"w:b.txt","str1":"head\n","str2":"\ntail"}`)
	if !ok {
		t.Fatalf("区间读失败: %s", out)
	}
	if !strings.HasSuffix(out, "| 2 行\nA\nB") {
		t.Fatalf("区间结果 = %q", out)
	}
	if _, ok := TextReadBetween(svc, `{"path":"w:b.txt","str1":"nope"}`); ok {
		t.Fatal("锚点未找到应失败")
	}
}

// TestTextErrors 参数错误文本。
func TestTextErrors(t *testing.T) {
	svc, _ := newSvc(t)
	if out, ok := TextRead(svc, `{"path":""}`); ok || out != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("空 path = %q", out)
	}
	if out, ok := TextRead(svc, `{"x":1}`); ok || !strings.Contains(out, "未知参数: x") {
		t.Fatalf("未知参数 = %q", out)
	}
	if out, ok := TextRead(svc, ``); ok || !strings.Contains(out, "工具参数 JSON 解析失败") {
		t.Fatalf("空载荷 = %q", out)
	}
	if out, ok := TextReadLines(svc, `{"path":"w:a.txt","start":"abc"}`); ok || !strings.Contains(out, "参数 start 非整数") {
		t.Fatalf("start 非整数 = %q", out)
	}
}

// TestSingleLineJSONLimit 单行 JSON 限幅——只治单行 JSON，多行 / 非 JSON 不动。
func TestSingleLineJSONLimit(t *testing.T) {
	svc, root := newSvc(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// ① 单行 JSON 超上限——text-read_lines 截断 + 提示
	write("big.json", `{"rows":["`+strings.Repeat("x", 21000)+`"]}`)
	out, ok := TextReadLines(svc, `{"path":"w:big.json","start":1,"end":1}`)
	if !ok {
		t.Fatalf("单行 JSON 读行失败: %s", out)
	}
	if !strings.Contains(out, "…[截断: 单行 JSON 共 ") {
		t.Fatalf("单行 JSON 应截断，实际尾段: %q", tail(out, 80))
	}
	if n := len([]rune(out)); n > 20600 {
		t.Fatalf("截断后仍超长: %d 字符", n)
	}

	// ② 单行 JSON 超上限——text-read_between 同样截断
	out, ok = TextReadBetween(svc, `{"path":"w:big.json"}`)
	if !ok {
		t.Fatalf("单行 JSON 区间读失败: %s", out)
	}
	if !strings.Contains(out, "…[截断: 单行 JSON 共 ") {
		t.Fatalf("区间读也应截断，实际尾段: %q", tail(out, 80))
	}

	// ③ 多行 JSON 超上限——不截断（多行可按行区间分段读）
	var b strings.Builder
	b.WriteString("[\n")
	for i := 0; i < 400; i++ {
		b.WriteString(`  {"id":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"note":"`)
		b.WriteString(strings.Repeat("y", 60))
		b.WriteString("\"},\n")
	}
	b.WriteString("]")
	write("multi.json", b.String())
	out, ok = TextReadLines(svc, `{"path":"w:multi.json"}`)
	if !ok {
		t.Fatalf("多行 JSON 读失败: %s", out)
	}
	if strings.Contains(out, "…[截断: 单行 JSON 共 ") {
		t.Fatal("多行 JSON 不应套单行限幅")
	}

	// ④ 单行非 JSON 超上限——不截断（只治单行 JSON）
	write("plain.txt", strings.Repeat("a", 30000))
	out, ok = TextReadLines(svc, `{"path":"w:plain.txt","start":1,"end":1}`)
	if !ok {
		t.Fatalf("单行纯文本读失败: %s", out)
	}
	if strings.Contains(out, "…[截断: 单行 JSON 共 ") {
		t.Fatal("单行纯文本不应套 JSON 限幅")
	}

	// ⑤ 单行 JSON 未超上限——不截断
	write("small.json", `{"a":1}`)
	out, ok = TextReadLines(svc, `{"path":"w:small.json","start":1,"end":1}`)
	if !ok || !strings.HasSuffix(out, `1: {"a":1}`) {
		t.Fatalf("小 JSON 不应截断: %q", out)
	}
}

// tail 取字符串尾段（测试诊断用，按字符截）。
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}
