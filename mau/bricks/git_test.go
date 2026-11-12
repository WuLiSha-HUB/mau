package bricks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// TestGitBricksArgs 参数校验面——未知键 / 必填 / 数值 / 布尔 / 选项注入逐条命中（零执行）。
func TestGitBricksArgs(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"status 缺 path", first(GitStatus(nil, `{"limit":1}`)), "ERR|BAD_ARGS|缺参数 path（必填：path）"},
		{"status 未知键", first(GitStatus(nil, `{"path":"x","foo":1}`)), "ERR|BAD_ARGS|未知参数: foo（支持 path limit timeout_ms）"},
		{"status limit 非正", first(GitStatus(nil, `{"path":"x","limit":0}`)), "ERR|BAD_ARGS|参数 limit 非正整数: 0"},
		{"log count 非正", first(GitLog(nil, `{"path":"x","count":0}`)), "ERR|BAD_ARGS|参数 count 非正整数: 0"},
		{"diff staged 非布尔", first(GitDiff(nil, `{"path":"x","staged":"maybe"}`)), "ERR|BAD_ARGS|参数 staged 非布尔: maybe"},
		{"diff limit 非正", first(GitDiff(nil, `{"path":"x","limit":-1}`)), "ERR|BAD_ARGS|参数 limit 非正整数: -1"},
		{"show rev 注入", first(GitShow(nil, `{"path":"x","rev":"-x"}`)), "ERR|BAD_ARGS|参数 rev 非法（不接受以 - 开头的形态）: -x"},
		{"commit 缺 message", first(GitCommit(nil, `{"path":"x"}`)), "ERR|BAD_ARGS|缺参数 message（必填：path message）"},
		{"commit message 空白", first(GitCommit(nil, `{"path":"x","message":"  ","add_all":true}`)), "ERR|BAD_ARGS|缺少参数 message（提交信息）"},
		{"commit add_all 非布尔", first(GitCommit(nil, `{"path":"x","message":"m","add_all":"yes"}`)), "ERR|BAD_ARGS|参数 add_all 非布尔: yes"},
		{"commit 未指定范围", first(GitCommit(nil, `{"path":"x","message":"m"}`)), "ERR|BAD_ARGS|未指定提交范围——paths 给显式路径列表，或 add_all=true 覆盖全仓"},
		{"commit paths 注入", first(GitCommit(nil, `{"path":"x","message":"m","paths":["-x"]}`)), "ERR|BAD_ARGS|paths 含非法项（不接受以 - 开头的形态）: -x"},
		{"push 选项注入", first(GitPush(nil, `{"path":"x","remote":"-f"}`)), "ERR|BAD_ARGS|remote / branch 不接受以 - 开头的形态"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: got %q，期望 %q", c.name, c.got, c.want)
		}
	}
}

// first 取积木首返回值。
func first(text string, _ bool) string { return text }

// TestGitNoService 服务未注入——六件一律 GIT_NO_SERVICE（不静默回落）。
func TestGitNoService(t *testing.T) {
	dir := t.TempDir()
	calls := []struct {
		name string
		got  string
	}{
		{"status", first(GitStatus(nil, `{"path":`+quote(dir)+`}`))},
		{"log", first(GitLog(nil, `{"path":`+quote(dir)+`}`))},
		{"diff", first(GitDiff(nil, `{"path":`+quote(dir)+`}`))},
		{"show", first(GitShow(nil, `{"path":`+quote(dir)+`}`))},
		{"commit", first(GitCommit(nil, `{"path":`+quote(dir)+`,"message":"m","add_all":true}`))},
		{"push", first(GitPush(nil, `{"path":`+quote(dir)+`}`))},
	}
	for _, c := range calls {
		if !strings.HasPrefix(c.got, "ERR|GIT_NO_SERVICE|") {
			t.Fatalf("%s: got %q，期望 GIT_NO_SERVICE", c.name, c.got)
		}
	}
}

// TestGitResolveRepo 根边界——空 path / 未知根 / 越界 / 目录不存在 / 只读根写拒绝。
func TestGitResolveRepo(t *testing.T) {
	root := t.TempDir()
	svc := NewGitService([]fs.Root{
		{ID: "tmp", Path: root, Writable: true},
		{ID: "ro", Path: t.TempDir(), Writable: false},
	}, root)
	if _, got := svc.ResolveRepo("", false); got != "ERR|GIT_BAD_ARGS|缺少参数 path（仓库工作目录）" {
		t.Fatalf("空 path：%s", got)
	}
	if _, got := svc.ResolveRepo("nope:x", false); !strings.HasPrefix(got, "ERR|GIT_BAD_PATH|路径非法: 未知受控根 id: nope") {
		t.Fatalf("未知根：%s", got)
	}
	if _, got := svc.ResolveRepo(`Q:\out-of-scope`, false); !strings.HasPrefix(got, "ERR|GIT_BAD_PATH|路径非法: 路径超出受控根范围：") {
		t.Fatalf("越界：%s", got)
	}
	if _, got := svc.ResolveRepo(filepath.Join(root, "missing"), false); !strings.HasPrefix(got, "ERR|GIT_BAD_PATH|目录不存在：") {
		t.Fatalf("目录不存在：%s", got)
	}
	if _, got := svc.ResolveRepo("ro:", true); !strings.HasPrefix(got, "ERR|GIT_ROOT_READONLY|只读根拒绝写操作：") || !strings.Contains(got, "（根 ro 不可写）") {
		t.Fatalf("只读根：%s", got)
	}
	if _, got := svc.ResolveRepo("tmp:", false); got != "" {
		t.Fatalf("根寻址应放行：%s", got)
	}
}

// TestGitBricksReal 实机面——临时仓库上跑 status / commit / log / diff / show / push 失败面。
func TestGitBricksReal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不在 PATH——跳过实机面")
	}
	root := t.TempDir()
	svc := NewGitService([]fs.Root{{ID: "tmp", Path: root, Writable: true}}, root)
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "--no-pager"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "TestCat")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}

	// status：未跟踪文件 + 分支名（不写死初始分支名）
	got, ok := GitStatus(svc, `{"path":`+quote(root)+`}`)
	if !ok {
		t.Fatalf("status 失败：%s", got)
	}
	if !strings.HasPrefix(got, `{"ok":true,"tool":"git-status","target":`) {
		t.Fatalf("status 头不符：%s", got)
	}
	if !strings.Contains(got, " · 变更 1 项") || !strings.Contains(got, "?? a.txt") {
		t.Fatalf("status 正文不符：%s", got)
	}
	if !strings.Contains(got, `"changed":1`) || !strings.Contains(got, `"branch":`) {
		t.Fatalf("status 字段不符：%s", got)
	}

	// status limit=0 已在校验面；limit=1 + 两个文件 → 截断自证
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	got, _ = GitStatus(svc, `{"path":`+quote(root)+`,"limit":1}`)
	if !strings.Contains(got, "变更 2 项") || !strings.Contains(got, "[截断] 变更共 2 项，已列 1 项（limit 提额可看全）") {
		t.Fatalf("status 截断口径不符：%s", got)
	}

	// commit：add_all 全量
	got, ok = GitCommit(svc, `{"path":`+quote(root)+`,"message":"init","add_all":true}`)
	if !ok {
		t.Fatalf("commit 失败：%s", got)
	}
	if !strings.Contains(got, "已提交 · 全量暂存（add_all）") || !strings.Contains(got, `"add_all":true`) || !strings.Contains(got, `"paths":0`) {
		t.Fatalf("commit 正文不符：%s", got)
	}

	// log：一条提交
	got, ok = GitLog(svc, `{"path":`+quote(root)+`,"count":5}`)
	if !ok {
		t.Fatalf("log 失败：%s", got)
	}
	if !strings.Contains(got, "最近 1 条提交") || !strings.Contains(got, "init") {
		t.Fatalf("log 正文不符：%s", got)
	}

	// diff：改文件后未暂存差异
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("改文件失败：%v", err)
	}
	got, ok = GitDiff(svc, `{"path":`+quote(root)+`}`)
	if !ok {
		t.Fatalf("diff 失败：%s", got)
	}
	if !strings.Contains(got, "未暂存 · 1 个文件 · +1 −0") || !strings.Contains(got, "--- diff ---") || !strings.Contains(got, "+world") {
		t.Fatalf("diff 正文不符：%s", got)
	}

	// staged 态：add 后 diff --staged
	runGit("add", "a.txt")
	got, ok = GitDiff(svc, `{"path":`+quote(root)+`,"staged":true}`)
	if !ok || !strings.Contains(got, "已暂存 · 1 个文件") {
		t.Fatalf("staged diff 不符：%s", got)
	}

	// show：HEAD 详情
	got, ok = GitShow(svc, `{"path":`+quote(root)+`,"rev":"HEAD"}`)
	if !ok {
		t.Fatalf("show 失败：%s", got)
	}
	if !strings.HasPrefix(got, `{"ok":true,"tool":"git-show","target":`) || !strings.Contains(got, `"rev":"HEAD"`) || !strings.Contains(got, "--- show ---") {
		t.Fatalf("show 正文不符：%s", got)
	}

	// push：无远端 → 非零退出转 ERR（提交失败必须看见红）
	got, ok = GitPush(svc, `{"path":`+quote(root)+`}`)
	if ok || !strings.HasPrefix(got, "ERR|GIT_PUSH_") {
		t.Fatalf("push 失败面不符：%s ok=%v", got, ok)
	}
}

// TestGitCommitPaths 显式 paths 提交——逗号分隔与 JSON 数组两形态（含暂存条数回执）。
func TestGitCommitPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不在 PATH——跳过实机面")
	}
	root := t.TempDir()
	svc := NewGitService([]fs.Root{{ID: "tmp", Path: root, Writable: true}}, root)
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "--no-pager"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "TestCat")
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatalf("写文件失败：%v", err)
		}
	}
	got, ok := GitCommit(svc, `{"path":`+quote(root)+`,"message":"two","paths":["a.txt","b.txt"]}`)
	if !ok {
		t.Fatalf("commit paths 失败：%s", got)
	}
	if !strings.Contains(got, "已提交 · 暂存 2 条路径") || !strings.Contains(got, `"paths":2`) {
		t.Fatalf("commit paths 正文不符：%s", got)
	}
	if err := os.WriteFile(filepath.Join(root, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	got, ok = GitCommit(svc, `{"path":`+quote(root)+`,"message":"one","paths":"c.txt"}`)
	if !ok || !strings.Contains(got, "已提交 · 暂存 1 条路径") {
		t.Fatalf("commit 逗号形态不符：%s", got)
	}
}

// quote 把字符串编码成 JSON 字面量（测试构造参数用）。
func quote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
