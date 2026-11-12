package inject

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/session"
)

// testResolver 把 "test:<rel>" 映射到 base 目录，其余按绝对路径原样。
func testResolver(base string) Resolver {
	return func(spec string) (string, error) {
		if strings.HasPrefix(spec, "test:") {
			return filepath.Join(base, strings.TrimPrefix(spec, "test:")), nil
		}
		if strings.HasPrefix(spec, "bad:") {
			return "", errors.New("注入清单寻址非法: ROOT_UNKNOWN_ID | 未知受控根 id: bad")
		}
		return spec, nil
	}
}

// testReadText 按 UTF-8 读取（去 BOM）。
func testReadText(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(raw), "\uFEFF"), nil
}

// TestBuildPromptSections 段序与文案——baseRole / persona / injectList 三段的 CRLF 形态。
func TestBuildPromptSections(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "甲内容")
	writeFile(t, filepath.Join(dir, "b.md"), "乙内容")
	resolve := testResolver(dir)

	got := BuildPrompt(resolve, testReadText, "基础角色。", "  猫娘人格  ", []string{"test:a.md", "test:b.md"}, nil)
	want := "基础角色。" +
		"\r\n\r\n【角色设定】猫娘人格" +
		"\r\n\r\n【系统前文来源】（清单 cat.cfg）：" +
		"\r\n\r\n===== test:a.md =====\r\n甲内容" +
		"\r\n\r\n===== test:b.md =====\r\n乙内容"
	if got.Prompt != want {
		t.Fatalf("提示词段序不符\n got=%q\nwant=%q", got.Prompt, want)
	}
	if len(got.Files) != 2 || got.Files[0].Status != StatusOK || got.Files[0].Chars != 3 {
		t.Fatalf("逐文件结果不符: %+v", got.Files)
	}
}

// TestBuildPromptEmptyOptional 空 baseRole / 空 persona / 空清单——只剩必要的段。
func TestBuildPromptEmptyOptional(t *testing.T) {
	got := BuildPrompt(testResolver(t.TempDir()), testReadText, "", "   ", nil, nil)
	if got.Prompt != "" {
		t.Fatalf("全空时应零字节，got=%q", got.Prompt)
	}
	if len(got.Files) != 0 {
		t.Fatalf("全空时应零文件项，got=%d", len(got.Files))
	}
}

// TestBuildPromptDirExpand 目录条目展开——一级 *.md 升序 + 前缀/文件名标签；空目录记 missing。
func TestBuildPromptDirExpand(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "b.md"), "乙")
	writeFile(t, filepath.Join(sub, "A.md"), "甲")
	writeFile(t, filepath.Join(sub, "skip.txt"), "非 md")
	got := BuildPrompt(testResolver(dir), testReadText, "", "", []string{"test:docs"}, nil)
	if len(got.Files) != 2 {
		t.Fatalf("应展开 2 条，got=%d: %+v", len(got.Files), got.Files)
	}
	// OrdinalIgnoreCase 升序——A.md 在前
	if got.Files[0].File != "test:docs/A.md" || got.Files[1].File != "test:docs/b.md" {
		t.Fatalf("展开序或标签不符: %+v", got.Files)
	}
	if !strings.Contains(got.Prompt, "===== test:docs/A.md =====\r\n甲") {
		t.Fatalf("展开正文段不符: %q", got.Prompt)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	got2 := BuildPrompt(testResolver(dir), testReadText, "", "", []string{"test:empty"}, nil)
	if len(got2.Files) != 1 || got2.Files[0].Status != StatusMissing || got2.Files[0].Message != msgDirEmpty {
		t.Fatalf("空目录应记 missing: %+v", got2.Files)
	}
}

// TestBuildPromptMissingAndError 缺失记 missing（可选不阻断）；寻址失败记 error。
func TestBuildPromptMissingAndError(t *testing.T) {
	dir := t.TempDir()
	got := BuildPrompt(testResolver(dir), testReadText, "", "", []string{"test:nope.md", "bad:x.md"}, nil)
	if len(got.Files) != 2 {
		t.Fatalf("应 2 条，got=%d", len(got.Files))
	}
	if got.Files[0].Status != StatusMissing || got.Files[0].Message != msgFileMissing {
		t.Fatalf("缺失项不符: %+v", got.Files[0])
	}
	if got.Files[1].Status != StatusError || !strings.Contains(got.Files[1].Message, "ROOT_UNKNOWN_ID") {
		t.Fatalf("寻址失败项不符: %+v", got.Files[1])
	}
	if strings.Contains(got.Prompt, "===== test:nope.md") {
		t.Fatalf("缺失文件不应进提示词")
	}
}

// TestBuildReportJSON 键序与计数——files 五键 / total·ok·missing·failed·injectCount / 工具组聚合。
func TestBuildReportJSON(t *testing.T) {
	files := []FileResult{
		{File: "a.md", Status: StatusOK, Chars: 3, Bytes: 9},
		{File: "b.md", Status: StatusMissing, Message: msgFileMissing},
		{File: "c.md", Status: StatusError, Message: "boom"},
	}
	specs := []session.ToolSpec{
		{Name: "alpha", Description: "A"},
		{Name: "beta", Description: "B"},
		{Name: "gamma", Description: "C"},
	}
	owner := map[string]string{"alpha": "G1", "beta": "G2"}
	got := BuildReportJSON(files, []string{"a.md", "b.md", "c.md"}, specs, owner, "CH4", true, false, "一句话")
	want := `{"files":[` +
		`{"file":"a.md","status":"ok","message":"","chars":3,"bytes":9},` +
		`{"file":"b.md","status":"missing","message":"文件不存在（可选，已跳过）","chars":0,"bytes":0},` +
		`{"file":"c.md","status":"error","message":"boom","chars":0,"bytes":0}],` +
		`"total":3,"ok":1,"missing":1,"failed":1,"injectCount":3,` +
		`"mount":"CH4","mountExpert":true,"mountAuto":false,"mountDesc":"一句话",` +
		`"toolGroups":[{"group":"G1","tools":[{"name":"alpha","desc":"A"}]},` +
		`{"group":"G2","tools":[{"name":"beta","desc":"B"}]},` +
		`{"group":"","tools":[{"name":"gamma","desc":"C"}]}]}`
	if got != want {
		t.Fatalf("注入报告 JSON 不符\n got=%s\nwant=%s", got, want)
	}
}

// TestBuildReportJSONEmpty 零文件 / 零工具——空数组而非 null（对齐 C# RawArray 空形态）。
func TestBuildReportJSONEmpty(t *testing.T) {
	got := BuildReportJSON(nil, nil, nil, nil, "", true, true, "")
	want := `{"files":[],"total":0,"ok":0,"missing":0,"failed":0,"injectCount":0,` +
		`"mount":"","mountExpert":true,"mountAuto":true,"mountDesc":"","toolGroups":[]}`
	if got != want {
		t.Fatalf("空报告不符\n got=%s\nwant=%s", got, want)
	}
}

// TestExtractProjectSummaryThreeWays 摘要三路——定位优先 / 类型次之 / 目的节兜底。
func TestExtractProjectSummaryThreeWays(t *testing.T) {
	located := []string{
		"# 项目档案",
		"",
		"> 类型：演示 / 版本：1.0",
		"> 定位：把 CatHome4 的功能在 Go 里重新实现。",
		"",
		"---",
		"正文",
	}
	if got := ExtractProjectSummary(located); got != "把 CatHome4 的功能在 Go 里重新实现" {
		t.Fatalf("定位抽取不符: %q", got)
	}

	typedOnly := []string{
		"> 类型：自举宿主 / 版本：0.7.2",
		"---",
	}
	if got := ExtractProjectSummary(typedOnly); got != "自举宿主" {
		t.Fatalf("类型抽取不符: %q", got)
	}

	purpose := []string{
		"# 档案",
		"> 无定位字段",
		"---",
		"## 项目目的",
		"",
		"| 表 | 格 |",
		"- 列表项",
		"这一句是真正的目的描述，足够长。",
	}
	if got := ExtractProjectSummary(purpose); got != "这一句是真正的目的描述，足够长" {
		t.Fatalf("目的节抽取不符: %q", got)
	}

	if got := ExtractProjectSummary(nil); got != "" {
		t.Fatalf("空输入应空串: %q", got)
	}
}

// TestExtractProjectSummaryBoundary 字段边界——正文斜杠不误切、粗体清洗、括号内标点不截句。
func TestExtractProjectSummaryBoundary(t *testing.T) {
	lines := []string{
		"> 类型：TypeScript / Node 24 · 工具 / 版本：1.0",
		"---",
	}
	if got := ExtractProjectSummary(lines); got != "TypeScript / Node 24" {
		t.Fatalf("正文斜杠应保留、字段边界应截断: %q", got)
	}

	bold := []string{
		"> 定位：**Go 重实现**（含工具 / 版本链路）",
		"---",
	}
	if got := ExtractProjectSummary(bold); got != "Go 重实现（含工具 / 版本链路）" {
		t.Fatalf("粗体清洗或括号保护不符: %q", got)
	}
}

// TestClampSummary 截断——90 字上限 + 省略号 + 未闭合括号回退。
func TestClampSummary(t *testing.T) {
	long := strings.Repeat("字", 100)
	got := clampSummary(long)
	if !strings.HasSuffix(got, "…") || strings.Count(got, "字") != ProjectSummaryMaxChars {
		t.Fatalf("截断形态不符: %q", got)
	}
	withParen := strings.Repeat("字", 85) + "（超长括号内容继续继续继续）"
	got2 := clampSummary(withParen)
	if strings.HasSuffix(got2, "（") || strings.HasSuffix(got2, "…（") {
		t.Fatalf("括号应整体回退: %q", got2)
	}
	if strings.Count(got2, "字") != 85 {
		t.Fatalf("括号回退位置不符: %q", got2)
	}
}

// writeFile 建立测试文件。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
