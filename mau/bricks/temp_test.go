package bricks

import (
	"strings"
	"testing"
)

// TestTempExecArgs 参数面零容忍——文案与 C# JsonArgs 逐字一致。
func TestTempExecArgs(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"空载荷", "", "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）"},
		{"缺 key", `{"content":"hi"}`, "ERR|BAD_ARGS|缺参数 key（必填：key content）"},
		{"缺 content", `{"key":"echo"}`, "ERR|BAD_ARGS|缺参数 content（必填：key content）"},
		{"key 空串", `{"key":"","content":"hi"}`, "ERR|BAD_ARGS|缺参数 key（必填：key content）"},
		{"未知参数", `{"key":"echo","content":"hi","foo":1}`, "ERR|BAD_ARGS|未知参数: foo（支持 key content）"},
		{"非对象", `["x"]`, "ERR|BAD_ARGS|参数必须是 JSON 对象"},
	}
	for _, tc := range cases {
		got, ok := TempExec(NewTempRegistry(), tc.args)
		if ok {
			t.Fatalf("%s：期望失败，实际成功（%q）", tc.name, got)
		}
		if got != tc.want {
			t.Fatalf("%s：期望 %q，实际 %q", tc.name, tc.want, got)
		}
	}
	// 非法 JSON——错误文本含 encoding/json 的细节，只断言前缀
	got, ok := TempExec(NewTempRegistry(), `{bad json`)
	if ok || !strings.HasPrefix(got, "ERR|BAD_ARGS|参数 JSON 解析失败: ") {
		t.Fatalf("非法 JSON：期望解析失败前缀，实际 %q（ok=%v）", got, ok)
	}
}

// TestTempExecEmptyRegistry 空注册表——未知 Key 报错并给空可用清单（有尾空格，逐字对齐 C#）。
func TestTempExecEmptyRegistry(t *testing.T) {
	got, ok := TempExec(nil, `{"key":"echo","content":"hi"}`)
	if ok {
		t.Fatalf("空注册表：期望失败，实际成功（%q）", got)
	}
	want := "ERR|TEMP_KEY_NOT_FOUND|可用Key: "
	if got != want {
		t.Fatalf("空注册表：期望 %q，实际 %q", want, got)
	}
}

// TestTempExecSuccess 注册自定义 handler 的成功路径——A214 键序 + 摘要行 + 正文。
func TestTempExecSuccess(t *testing.T) {
	reg := NewTempRegistry()
	reg.Register("echo", func(content string) string { return "echo:" + content })
	got, ok := TempExec(reg, `{"key":"echo","content":"hi"}`)
	if !ok {
		t.Fatalf("期望成功，实际失败（%q）", got)
	}
	want := "{\"ok\":true,\"tool\":\"temp-exec\",\"target\":\"echo\",\"chars\":7}\necho | 7 字\necho:hi"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	// 空返回体——只有头，无摘要行与载荷行
	reg.Register("noop", func(content string) string { return "" })
	got, ok = TempExec(reg, `{"key":"noop","content":"x"}`)
	if !ok || got != "{\"ok\":true,\"tool\":\"temp-exec\",\"target\":\"noop\",\"chars\":0}" {
		t.Fatalf("空返回体：实际 %q（ok=%v）", got, ok)
	}
}

// TestTempExecUTF16Chars chars 按 UTF-16 码元计长（emoji 计 2，对齐 C# string.Length）。
func TestTempExecUTF16Chars(t *testing.T) {
	reg := NewTempRegistry()
	reg.Register("emoji", func(content string) string { return "a😀b" })
	got, ok := TempExec(reg, `{"key":"emoji","content":"x"}`)
	if !ok {
		t.Fatalf("期望成功，实际失败（%q）", got)
	}
	want := "{\"ok\":true,\"tool\":\"temp-exec\",\"target\":\"emoji\",\"chars\":4}\nemoji | 4 字\na😀b"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}

// TestTempExecPanic handler 异常回收——ERR|TEMP_EXEC|<类型>|<消息>，不向外冒泡。
func TestTempExecPanic(t *testing.T) {
	reg := NewTempRegistry()
	reg.Register("boom", func(content string) string { panic("炸了") })
	got, ok := TempExec(reg, `{"key":"boom","content":"x"}`)
	if ok {
		t.Fatalf("期望失败，实际成功（%q）", got)
	}
	want := "ERR|TEMP_EXEC|string|炸了"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}

// TestTempKeysEmpty 空注册表——items=0 + keys 空数组 + 无载荷行。
func TestTempKeysEmpty(t *testing.T) {
	got, ok := TempKeys(NewTempRegistry())
	if !ok {
		t.Fatalf("期望成功，实际失败（%q）", got)
	}
	want := `{"ok":true,"tool":"temp-info","items":0,"keys":[]}`
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}

// TestTempKeysNonEmpty 非空注册表——字典序 + items 计数 + 摘要行 + 逗号清单。
func TestTempKeysNonEmpty(t *testing.T) {
	reg := NewTempRegistry()
	reg.Register("beta", func(content string) string { return "" })
	reg.Register("alpha", func(content string) string { return "" })
	got, ok := TempKeys(reg)
	if !ok {
		t.Fatalf("期望成功，实际失败（%q）", got)
	}
	want := "{\"ok\":true,\"tool\":\"temp-info\",\"items\":2,\"keys\":[\"alpha\",\"beta\"]}\n2 个 Key\nalpha,beta"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}

// TestTempRegistryNilSafe nil 注册表按空表处理——读取不 panic。
func TestTempRegistryNilSafe(t *testing.T) {
	if keys := (*TempRegistry)(nil).ListKeys(); keys != "" {
		t.Fatalf("nil 注册表 ListKeys 期望空串，实际 %q", keys)
	}
	if _, ok := (*TempRegistry)(nil).Find("x"); ok {
		t.Fatal("nil 注册表 Find 期望 false")
	}
	(*TempRegistry)(nil).Register("x", func(string) string { return "" })
}
