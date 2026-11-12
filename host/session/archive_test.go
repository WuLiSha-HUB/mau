package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newArchiveFixture 造一份带四部分内容的视图存储（user / reason / text / roundsum）。
func newArchiveFixture(t *testing.T) (*ViewStore, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "sessions", "majordomo", "majordomo.view.jsonl")
	store := NewViewStore(path)
	store.blocks = []*ViewBlock{
		{Timestamp: 1762800000000, RenderType: TypeUser, Payload: `{"text":"第一问"}`},
		{Timestamp: 1762800001000, RenderType: TypeReason, Payload: `{"text":"思考内容不进档"}`},
		{Timestamp: 1762800002000, RenderType: TypeText, Payload: `{"text":"第一答"}`},
	}
	store.roundSums = []*ViewBlock{
		{Timestamp: 1762800003000, RenderType: TypeRoundSum,
			Payload: `{"type":"roundsum","data":{"prompt":10,"completion":3,"cacheHit":4,"toolCount":0,"requests":1,"elapsedMs":300}}`},
	}
	return store, root
}

// TestArchiveLegacyMarkdown 归档产物可复验——头部 / 加载报告段 / 对话段三部分齐备。
func TestArchiveLegacyMarkdown(t *testing.T) {
	store, root := newArchiveFixture(t)
	store.SetInjectReport(`{"total":2,"ok":2,"missing":0,"failed":0,` +
		`"files":[{"file":"AGENTS.md","status":"ok","chars":10}],` +
		`"toolGroups":[{"group":"TextCat","tools":[{"name":"text-read"}]}]}`)

	path := store.ArchiveLegacy("majordomo", "主干")
	if path == "" {
		t.Fatalf("归档未生成")
	}
	if filepath.Dir(path) != filepath.Join(root, "sessions_old") {
		t.Fatalf("落点不符: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读归档失败: %v", err)
	}
	if len(raw) < 3 || raw[0] != 0xEF || raw[1] != 0xBB || raw[2] != 0xBF {
		t.Fatalf("归档缺 UTF-8 BOM")
	}
	text := string(raw[3:])
	for _, want := range []string{
		"# 会话留档 — 主干",
		"- 猫：`majordomo`（主干）",
		"- 块数：user 1 · 回复 1 · 轮结算 1",
		"## 新会话加载报告",
		"- 注入清单：2 个文件（ok 2 / missing 0 / 失败 0）",
		"- `AGENTS.md` — ok（10 字符）",
		"- 工具组 `TextCat`：text-read",
		"## 对话",
		"第一问",
		"第一答",
		"Token 上 10 / 下 3（缓存 4）· 工具 0 次 · 请求 1 次 · 用时 0.3 秒",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("归档缺少 %q\n全文：\n%s", want, text)
		}
	}
	if strings.Contains(text, "思考内容不进档") {
		t.Fatalf("思考条目不应进档：\n%s", text)
	}
	// 三段时序：user → text → 轮结算（按时间戳升序；段头形态「· 轮结算」+ 换行）
	userIdx := strings.Index(text, "第一问")
	replyIdx := strings.Index(text, "第一答")
	sumIdx := strings.Index(text, "· 轮结算\n")
	if !(userIdx < replyIdx && replyIdx < sumIdx) {
		t.Fatalf("条目时序不符: user=%d reply=%d sum=%d", userIdx, replyIdx, sumIdx)
	}
}

// TestArchiveLegacyNoReport 无注入报告时记一行（不阻断出档）。
func TestArchiveLegacyNoReport(t *testing.T) {
	store, _ := newArchiveFixture(t)
	path := store.ArchiveLegacy("majordomo", "主干")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读归档失败: %v", err)
	}
	if !strings.Contains(string(raw), "- 无（旧会话未生成注入报告）") {
		t.Fatalf("无报告文案缺失")
	}
}

// TestArchiveLegacyEmptyStillWrites 四部分全空也照常出档（档案面留痕优先于体积）。
func TestArchiveLegacyEmptyStillWrites(t *testing.T) {
	root := t.TempDir()
	store := NewViewStore(filepath.Join(root, "sessions", "majordomo", "majordomo.view.jsonl"))
	path := store.ArchiveLegacy("majordomo", "主干")
	if path == "" {
		t.Fatalf("空会话也应出档")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "- 块数：user 0 · 回复 0 · 轮结算 0") {
		t.Fatalf("空档块数不符：%s", string(raw))
	}
}

// TestFindLatestArchiveAndReadLast 留档扫描与末条读取（cat-detail 回落数据源）。
func TestFindLatestArchiveAndReadLast(t *testing.T) {
	store, root := newArchiveFixture(t)
	path := store.ArchiveLegacy("majordomo", "主干")
	if path == "" {
		t.Fatalf("归档未生成")
	}
	dir := filepath.Join(root, "sessions_old")
	if got := FindLatestArchive(dir, "majordomo"); got != path {
		t.Fatalf("扫描命中 = %s，预期 %s", got, path)
	}
	if got := FindLatestArchive(dir, "other"); got != "" {
		t.Fatalf("非本猫不应命中，实际 %s", got)
	}
	raw, _ := os.ReadFile(path)
	timeText, content, ok := TryReadLastLegacyEntry(string(raw), "回复")
	if !ok || content != "第一答" || timeText == "" {
		t.Fatalf("末条回复读取不符: ok=%v time=%q content=%q", ok, timeText, content)
	}
	_, content, ok = TryReadLastLegacyEntry(string(raw), "用户")
	if !ok || content != "第一问" {
		t.Fatalf("末条用户读取不符: %q", content)
	}
}
