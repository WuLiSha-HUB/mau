package bricks

import (
	"strconv"
	"strings"
	"testing"
)

// psArgs 构造 ps.exec 的工具参数 JSON。
func psArgs(command string) string {
	return `{"command":` + strconv.Quote(command) + `}`
}

// TestPsExecIntercepts 词法拦截表——十二条拦截面逐条命中。
func TestPsExecIntercepts(t *testing.T) {
	cases := []struct{ name, command, want string }{
		{"多段", "Get-Date; Get-Date", "ERR|PS_MULTI_FORBIDDEN|"},
		{"管道", "Get-Process | Select-Object", "ERR|PS_PIPE_FORBIDDEN|"},
		{"块", "if (1) { 2 }", "ERR|PS_BLOCK_FORBIDDEN|"},
		{"变量赋值", "$x = 1", "ERR|PS_ASSIGN_FORBIDDEN|"},
		{"代码形态", "Get-Date -Format [x]", "ERR|PS_CODE_FORBIDDEN|"},
		{"写文件", "Set-Content a.txt hi", "ERR|PS_WRITE_FORBIDDEN|"},
		{"读文件", "Get-Content a.txt", "ERR|PS_READ_FILE_FORBIDDEN|"},
		{"列目录", "Get-ChildItem .", "ERR|PS_LIST_FORBIDDEN|"},
		{"dir", "dir", "ERR|PS_LIST_FORBIDDEN|"},
		{"重定向", "Get-Date > a.txt", "ERR|PS_WRITE_FORBIDDEN|"},
		{"起进程", "cmd /c Start-Process notepad", "ERR|PS_START_FORBIDDEN|"},
		{"控制台等待", "cmd /c Read-Host x", "ERR|PS_READ_FORBIDDEN|"},
	}
	for _, c := range cases {
		got, ok := PsExec(nil, nil, psArgs(c.command), "powershell")
		if ok || !strings.HasPrefix(got, c.want) {
			t.Fatalf("%s: got %q ok=%v，期望前缀 %q", c.name, got, ok, c.want)
		}
	}
}

// TestPsExecAllows 放行面——git 豁免 / 引号内符号 / 箭头与比较符 / 驱动器路径。
func TestPsExecAllows(t *testing.T) {
	pass := []string{
		"git status",
		"git diff --stat",
		"Get-Date -Format 'yyyy-MM-dd'",
		"Test-Path ccbp:L1/Tree.md",
		"Write-Output 'a>b'",
	}
	for _, command := range pass {
		got, _ := PsExec(nil, nil, psArgs(command), "powershell")
		if strings.HasPrefix(got, "ERR|PS_") {
			t.Fatalf("%q 被误拦：%s", command, got)
		}
	}
}

// TestPsExecRuns 执行面——单条命令实机跑通（头字段 + 摘要行 + 正文）。
func TestPsExecRuns(t *testing.T) {
	got, ok := PsExec(nil, nil, psArgs("Write-Output hello"), "powershell")
	if !ok {
		t.Fatalf("执行失败：%s", got)
	}
	wantHead := `{"ok":true,"tool":"powershell","target":"powershell","exit":0,"truncated":false,"timeout":false,"stdoutLines":1,"stderrLines":0}`
	if !strings.HasPrefix(got, wantHead) {
		t.Fatalf("头不符：\n got=%s\nwant=%s", got, wantHead)
	}
	if !strings.Contains(got, "powershell | 退出 0 · 1 行") {
		t.Fatalf("摘要行缺失：%s", got)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("正文缺失：%s", got)
	}
}

// TestPsExecPwshMissing powershell7 线未配置——明示 ERR，不回落默认解释器。
func TestPsExecPwshMissing(t *testing.T) {
	got, ok := PsExec(nil, func() string { return "" }, psArgs("Write-Output x"), "powershell7")
	if ok || !strings.HasPrefix(got, "ERR|PS_PWSH_MISSING|") {
		t.Fatalf("未配置口径不符：%s ok=%v", got, ok)
	}
}

// TestPsExecMissingCommand 缺 command——PS_BAD_ARGS。
func TestPsExecMissingCommand(t *testing.T) {
	got, ok := PsExec(nil, nil, `{"cwd":"."}`, "powershell")
	if ok || got != "ERR|PS_BAD_ARGS|缺少参数 command" {
		t.Fatalf("缺参口径不符：%s ok=%v", got, ok)
	}
}
