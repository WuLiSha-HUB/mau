package bricks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 预置测试文件（LF；不引入 BOM）。
func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFile 读回测试文件。
func readFile(t *testing.T, root, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestTextReplaceExact 精确替换——自证四件（行数差 / 原文摘要 / 窗口范围）逐字对齐。
func TestTextReplaceExact(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "a.txt", "head\nA\nB\ntail")
	out, ok := TextReplace(svc, `{"path":"w:a.txt","old":"A\nB","new":"X"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:a.txt","items":1}` + "\n" +
		`w:a.txt | 替换 1 处 · 行 2→1 ⚠ 净删 1 行——锚点吃掉的每一行都要在 new 里交代 · 原文 2 行 ｜ 首行：A` + "\n" +
		"X\ntail" + `…（文件共 3 行，此处只显示替换点前后 ≤3 行）` + "\n"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
	if got := readFile(t, root, "a.txt"); got != "head\nX\ntail" {
		t.Fatalf("落盘 = %q", got)
	}
}

// TestTextReplaceAmbiguous 唯一性拒绝——候选行（前 5）。
func TestTextReplaceAmbiguous(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "c.txt", "x x x")
	out, ok := TextReplace(svc, `{"path":"w:c.txt","old":"x","new":"y"}`)
	if ok || out != "ERR|ANCHOR_AMBIGUOUS|锚点出现 3 次，候选行: 1, 1, 1" {
		t.Fatalf("回执 = %q ok=%v", out, ok)
	}
	if got := readFile(t, root, "c.txt"); got != "x x x" {
		t.Fatalf("拒绝时不应写盘，实际 = %q", got)
	}
}

// TestTextReplaceNotFoundEntity 未命中——差异字节定位 + 实体提示。
func TestTextReplaceNotFoundEntity(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "b.txt", "a &amp; b <tag>\nline2")
	out, ok := TextReplace(svc, `{"path":"w:b.txt","old":"&lt;tag&gt;","new":"X"}`)
	want := "ERR|ANCHOR_NOT_FOUND|第 3 字节 期望「lt;tag&gt;」实际「amp; b <ta」｜提示：疑似实体写法——锚点直接写裸符号 < > &；若文件本身是实体文本，锚点写双写形态 &amp;lt;"
	if ok || out != want {
		t.Fatalf("回执 = %q\n期望 = %q", out, want)
	}
}

// TestTextReplaceNotFoundEscape 未命中——转义提示。
func TestTextReplaceNotFoundEscape(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "d.txt", "path\\to")
	out, ok := TextReplace(svc, `{"path":"w:d.txt","old":"path/to","new":"X"}`)
	want := `ERR|ANCHOR_NOT_FOUND|第 4 字节 期望「/to」实际「\to」｜提示：锚点按标准 JSON 语义——字面反斜杠写成两个反斜杠、字面引号写成反斜杠加引号；锚点取 text-read 的原文行，勿把源码里的字符串结束引号当作已转义引号。`
	if ok || out != want {
		t.Fatalf("回执 = %q\n期望 = %q", out, want)
	}
}

// TestTextReplaceDeleteMarker 删除标记——按空文本落盘，回执注明显式删除。
func TestTextReplaceDeleteMarker(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "g.txt", "a\nb\nc")
	out, ok := TextReplace(svc, `{"path":"w:g.txt","old":"b\n","new":"黑暗剑+22"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:g.txt","items":1}` + "\n" +
		`w:g.txt | 替换 1 处——本次 New 为空（删除标记「黑暗剑+22」）：删除匹配的 Old 串 · 行 2→0（删除标记·显式删除） · 原文 2 行 ｜ 首行：b` + "\n" +
		`c…（文件共 2 行，此处只显示替换点前后 ≤3 行）` + "\n"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
	if got := readFile(t, root, "g.txt"); got != "a\nc" {
		t.Fatalf("落盘 = %q", got)
	}
}

// TestTextReplaceAllMode all 模式——多处命中 + 命中行截断自证。
func TestTextReplaceAllMode(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "h.txt", "a\na\na\na\na\na\na")
	out, ok := TextReplace(svc, `{"path":"w:h.txt","old":"a","new":"z","mode":"all"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:h.txt","items":7}` + "\n" +
		`w:h.txt | 替换 7 处 · 命中行 1, 2, 3, 4, 5…（前 5 / 共 7） · 原文 1 行 ｜ 首行：a` + "\n" +
		"z\nz\nz\n" + "…（文件共 7 行，此处只显示替换点前后 ≤3 行）\n"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}

// TestTextReplaceRegex 正则替换——捕获组 $2:$1 与跨度自证。
func TestTextReplaceRegex(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "e.txt", "a=1; b=2")
	out, ok := TextReplace(svc, `{"path":"w:e.txt","old":"(\\w)=(\\d)","new":"$2:$1","mode":"regex"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:e.txt","items":2}` + "\n" +
		`w:e.txt | 替换 2 处 · 匹配跨度 最长 1 行 / 3 字符 · 命中合计 6 字符 · 原文 1 行 ｜ 首行：(\w)=(\d)` + "\n" +
		"1:a; 2:b"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
	if got := readFile(t, root, "e.txt"); got != "1:a; 2:b" {
		t.Fatalf("落盘 = %q", got)
	}
}

// TestTextReplaceRegexZeroLength 正则零长匹配枚举——与 .NET 一致（尾部空匹配也计入）。
func TestTextReplaceRegexZeroLength(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "n.txt", "abc")
	out, ok := TextReplace(svc, `{"path":"w:n.txt","old":"[\\s\\S]*","new":"X","mode":"regex"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:n.txt","items":2}` + "\n" +
		`w:n.txt | 替换 2 处 · 匹配跨度 最长 1 行 / 3 字符 · 命中合计 3 字符 · 原文 1 行 ｜ 首行：[\s\S]*` + "\n" +
		"XX"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}

// TestTextReplaceIgnoreCase 忽略大小写——字面匹配 + 保留替换文本字面量。
func TestTextReplaceIgnoreCase(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "i.txt", "Hello World")
	out, ok := TextReplace(svc, `{"path":"w:i.txt","old":"hello","new":"hi","mode":"ignore_case"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:i.txt","items":1}` + "\n" +
		`w:i.txt | 替换 1 处 · 原文 1 行 ｜ 首行：hello` + "\n" +
		"hi World"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}

// TestTextReplaceStructMarks 结构行守护——表行 / 分隔行 / 标题 / 分节线减少即出声。
func TestTextReplaceStructMarks(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "m.txt", "## T\n| a |\n|:--|\n---\nbody")
	out, ok := TextReplace(svc, `{"path":"w:m.txt","old":"## T\n| a |\n|:--|\n---","new":"plain"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:m.txt","items":1}` + "\n" +
		`w:m.txt | 替换 1 处 · 行 4→1 ⚠ 净删 3 行——锚点吃掉的每一行都要在 new 里交代 · 结构行 表行 1→0 / 表格分隔行 1→0 / 标题 1→0 / 分节线 1→0 ⚠ 锚点吃掉结构行而未重建——请核对 new 是否漏抄（表格需分隔行、章节需标题） · 原文 4 行 ｜ 首行：## T` + "\n" +
		"plain\nbody"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}

// TestTextReplaceLiteralDollar 非正则模式——new 里的 $1 按字面落盘。
func TestTextReplaceLiteralDollar(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "j.txt", "a=1")
	out, ok := TextReplace(svc, `{"path":"w:j.txt","old":"=1","new":"=$1"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	if !strings.HasSuffix(out, "w:j.txt | 替换 1 处 · 原文 1 行 ｜ 首行：=1\na=$1") {
		t.Fatalf("回执 = %q", out)
	}
	if got := readFile(t, root, "j.txt"); got != "a=$1" {
		t.Fatalf("落盘 = %q", got)
	}
}

// TestTextReplaceBadArgs 参数面三拒绝 + 服务缺失。
func TestTextReplaceBadArgs(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "x.txt", "abc")
	cases := []struct {
		name string
		args string
		want string
	}{
		{"未知参数", `{"path":"w:x.txt","old":"a","new":"b","zzz":1}`, "ERR|BAD_ARGS|未知参数: zzz（支持 path old new mode）"},
		{"缺必填", `{"path":"w:x.txt","old":"a"}`, "ERR|BAD_ARGS|缺参数 new（必填：path old new）"},
		{"空 new", `{"path":"w:x.txt","old":"a","new":""}`, "ERR|BAD_ARGS|缺参数 new（必填：path old new）"},
		{"非法 mode", `{"path":"w:x.txt","old":"a","new":"b","mode":"weird"}`, "ERR|BAD_ARGS|mode 非法值: weird（exact|ignore_case|all|regex）"},
		{"空载荷", ``, "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := TextReplace(svc, c.args)
			if ok || out != c.want {
				t.Fatalf("回执 = %q ok=%v，期望 %q", out, ok, c.want)
			}
		})
	}
	if out, ok := TextReplace(nil, `{"path":"w:x.txt","old":"a","new":"b"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
}

// TestMetaHeadNetEscaping A214 头转义口径——对齐 System.Text.Json 默认编码器（非 ASCII / HTML 敏感字符走大写 \uXXXX）。
func TestMetaHeadNetEscaping(t *testing.T) {
	got := MetaHead("text-read", true, "workspace:tools/CatTemp/测试-甲.txt", 1)
	want := `{"ok":true,"tool":"text-read","target":"workspace:tools/CatTemp/\u6D4B\u8BD5-\u7532.txt","items":1}`
	if got != want {
		t.Fatalf("中文路径头 = %s，期望 %s", got, want)
	}
	got = MetaHead("x", true, "a+b&c'd", 0)
	want = `{"ok":true,"tool":"x","target":"a\u002Bb\u0026c\u0027d","items":0}`
	if got != want {
		t.Fatalf("敏感字符头 = %s，期望 %s", got, want)
	}
	if got := MetaHead("x", true, "", -1); got != `{"ok":true,"tool":"x"}` {
		t.Fatalf("省略形态 = %s", got)
	}
}

// TestTextReplaceChinesePath 中文路径——A214 头转义、摘要行原样直出（与 C# 实测回执逐字对齐）。
func TestTextReplaceChinesePath(t *testing.T) {
	svc, root := newSvc(t)
	writeFile(t, root, "测试-甲.txt", "abc")
	out, ok := TextReplace(svc, `{"path":"w:测试-甲.txt","old":"b","new":"X"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"text-replace","target":"w:\u6D4B\u8BD5-\u7532.txt","items":1}` + "\n" +
		`w:测试-甲.txt | 替换 1 处 · 原文 1 行 ｜ 首行：b` + "\n" +
		"aXc"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}
