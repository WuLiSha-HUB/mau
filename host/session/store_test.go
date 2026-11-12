package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStoreAppendFormat 追加落盘格式——首条消息前补 meta 行；消息行字段序与 C# 一致。
func TestStoreAppendFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat", "cat.jsonl")
	store := NewSessionStore(path)
	store.SetSessionID("cat")
	store.Append(Message{Role: RoleUser, Content: "你好"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("行数应为 2（meta + 消息），实得 %d: %q", len(lines), string(raw))
	}
	if lines[0] != `{"t":"meta","SessionId":"cat"}` {
		t.Fatalf("meta 行不符: %s", lines[0])
	}
	want := `{"Role":1,"Content":"你好","ToolCallId":"","ToolName":"","ToolCallsJson":"","ReasoningContent":"","ImagesJson":"","CreatedAt":0,"t":"m"}`
	if lines[1] != want {
		t.Fatalf("消息行不符:\n实得 %s\n期望 %s", lines[1], want)
	}
}

// TestStoreRoundTrip 追加 → 载入往返；meta 行不进读面。
func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	store := NewSessionStore(path)
	store.SetSessionID("a")
	store.Append(Message{Role: RoleSystem, Content: "sys", CreatedAt: 1})
	store.Append(Message{Role: RoleUser, Content: "u", CreatedAt: 2})
	store.Append(Message{Role: RoleAssistant, ToolCallsJSON: `[{"id":"x","function":{"name":"t","arguments":"{}"}}]`, ReasoningContent: "想", CreatedAt: 3})
	store.Append(Message{Role: RoleTool, ToolCallID: "x", ToolName: "t", Content: "结果", CreatedAt: 4})

	loaded, ok := NewSessionStore(path).TryLoad()
	if !ok || len(loaded) != 4 {
		t.Fatalf("载入应为 4 条: ok=%v len=%d", ok, len(loaded))
	}
	if loaded[2].ReasoningContent != "想" || loaded[2].ToolCallsJSON == "" {
		t.Fatalf("assistant 字段丢失: %+v", loaded[2])
	}
	if loaded[3].ToolCallID != "x" || loaded[3].ToolName != "t" {
		t.Fatalf("tool 字段丢失: %+v", loaded[3])
	}
	if loaded[1].CreatedAt != 2 {
		t.Fatalf("CreatedAt 丢失: %+v", loaded[1])
	}
}

// TestStoreRepairTruncatedLastLine 末行残缺补全 + 标注 + 回写。
func TestStoreRepairTruncatedLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.jsonl")
	body := `{"t":"meta","SessionId":"b"}` + "\n" +
		`{"Role":1,"Content":"完整","ToolCallId":"","ToolName":"","ToolCallsJson":"","ReasoningContent":"","ImagesJson":"","CreatedAt":5,"t":"m"}` + "\n" +
		`{"Role":2,"Content":"半截`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, ok := NewSessionStore(path).TryLoad()
	if !ok || len(loaded) != 2 {
		t.Fatalf("应载入 2 条（含补全）: ok=%v len=%d", ok, len(loaded))
	}
	if !strings.HasSuffix(loaded[1].Content, TruncatedRepairNote) {
		t.Fatalf("补全消息应带修复标注: %q", loaded[1].Content)
	}
	if loaded[1].CreatedAt <= loaded[0].CreatedAt {
		t.Fatalf("残缺消息时间戳应取前一条 +1: %d / %d", loaded[1].CreatedAt, loaded[0].CreatedAt)
	}
	// 回写——磁盘不再有残缺行
	raw, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		if !isJSONObject(line) {
			t.Fatalf("磁盘应已回写为合法行: %s", line)
		}
	}
}

// TestStoreSkipBadMiddleLine 非末行坏行跳过（其余照常加载）。
func TestStoreSkipBadMiddleLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.jsonl")
	body := `{"t":"meta","SessionId":"c"}` + "\n" +
		`坏行` + "\n" +
		`{"Role":1,"Content":"好","CreatedAt":1,"t":"m"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, ok := NewSessionStore(path).TryLoad()
	if !ok || len(loaded) != 1 || loaded[0].Content != "好" {
		t.Fatalf("坏行应跳过: ok=%v %+v", ok, loaded)
	}
}

// TestStoreBackupCorrupt 全文件不可解析 → 备份 .bad。
func TestStoreBackupCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.jsonl")
	if err := os.WriteFile(path, []byte("全坏\n另一行\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := NewSessionStore(path).TryLoad(); ok {
		t.Fatal("全坏文件应载入失败")
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("应备份为 .bad: %v", err)
	}
}

// TestStoreRewrite 原子重写——meta 行 + 全量消息。
func TestStoreRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.jsonl")
	store := NewSessionStore(path)
	store.SetSessionID("e")
	store.Append(Message{Role: RoleUser, Content: "旧", CreatedAt: 1})
	store.Rewrite([]Message{{Role: RoleUser, Content: "新", CreatedAt: 2}})
	loaded, ok := NewSessionStore(path).TryLoad()
	if !ok || len(loaded) != 1 || loaded[0].Content != "新" {
		t.Fatalf("重写后应只留新前文: ok=%v %+v", ok, loaded)
	}
}

// TestNewSessionIDShape 会话标识形态——当前年代为 18 位十进制（.NET ticks 同形态）。
func TestNewSessionIDShape(t *testing.T) {
	id := NewSessionID()
	if len(id) != 18 {
		t.Fatalf("标识应为 18 位十进制（2026 年 .NET ticks 位数），实得 %q", id)
	}
	if strings.Trim(id, "0123456789") != "" {
		t.Fatalf("标识应为纯数字: %q", id)
	}
}
