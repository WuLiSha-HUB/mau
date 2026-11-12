package group

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore 建临时组根并加载（空库）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	store := New(root, nil)
	store.Load()
	return store
}

// TestCreateAndList 组新建 / 更新与列表排序。
func TestCreateAndList(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"majordomo", " 猫乙 ", "majordomo"}, "项目A", nil, nil, -1); result != "ok|组甲" {
		t.Fatalf("新建组回执不符: %s", result)
	}
	// 同名覆盖：成员重设，创建时刻与启用态保留
	if result := store.CreateOrUpdate("组甲", []string{"猫丙"}, "", nil, nil, -1); result != "ok|组甲" {
		t.Fatalf("更新组回执不符: %s", result)
	}
	if result := store.CreateOrUpdate("组 乙", []string{"a"}, "", nil, nil, -1); !strings.HasPrefix(result, "ERR|GROUP_NAME_INVALID|") {
		t.Fatalf("非法组名应拒绝: %s", result)
	}
	if result := store.CreateOrUpdate("组丙", []string{" "}, "", nil, nil, -1); result != "ERR|GROUP_MEMBERS_EMPTY|组至少需要一名成员" {
		t.Fatalf("空成员应拒绝: %s", result)
	}
	list := store.ListGroups()
	if len(list) != 1 {
		t.Fatalf("组数量不符: %d", len(list))
	}
	if got := list[0].MemberKeys; len(got) != 1 || got[0] != "猫丙" {
		t.Fatalf("成员覆盖不符: %v", got)
	}
}

// TestAdminSubscriberSubset 管理员 / 订阅者恒为成员子集。
func TestAdminSubscriberSubset(t *testing.T) {
	store := newTestStore(t)
	result := store.CreateOrUpdate("组甲", []string{"a", "b"}, "", []string{"a", "c"}, []string{"b", "c"}, 60)
	if result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	info := store.GetGroup("组甲")
	if info == nil {
		t.Fatal("组不存在")
	}
	if len(info.AdminKeys) != 1 || info.AdminKeys[0] != "a" {
		t.Fatalf("管理员收敛不符: %v", info.AdminKeys)
	}
	if len(info.SubscriberKeys) != 1 || info.SubscriberKeys[0] != "b" {
		t.Fatalf("订阅者收敛不符: %v", info.SubscriberKeys)
	}
	if info.SubscribeIdleSec != 60 {
		t.Fatalf("订阅阈值不符: %d", info.SubscribeIdleSec)
	}
	if got := GetSubscribeIdleSec(info); got != 60 {
		t.Fatalf("GetSubscribeIdleSec 不符: %d", got)
	}
	if got := GetSubscribeIdleSec(nil); got != DefaultSubscribeIdleSec {
		t.Fatalf("默认阈值不符: %d", got)
	}
}

// TestAppendAndRead 消息追加 / 校验 / 读取三形态。
func TestAppendAndRead(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"a", "b"}, "", nil, nil, -1); result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "", "第一条", "", ""); result != "ok|组甲|seq=1" {
		t.Fatalf("首条消息失败: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "note", "第二条", "ref-1", "b"); result != "ok|组甲|seq=2" {
		t.Fatalf("第二条消息失败: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "bad", "x", "", ""); !strings.HasPrefix(result, "ERR|GROUP_TYPE_INVALID|") {
		t.Fatalf("非法类型应拒绝: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "note", "   ", "", ""); result != "ERR|GROUP_TEXT_EMPTY|消息正文为空" {
		t.Fatalf("空正文应拒绝: %s", result)
	}
	if result := store.AppendMessage("组甲", "x", "note", "x", "", ""); !strings.HasPrefix(result, "ERR|GROUP_FROM_NOT_MEMBER|") {
		t.Fatalf("非成员发送方应拒绝: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "note", "x", "", "x"); !strings.HasPrefix(result, "ERR|GROUP_TARGET_NOT_MEMBER|") {
		t.Fatalf("非成员 @ 应拒绝: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "note", "x", "", "a"); !strings.HasPrefix(result, "ERR|GROUP_TARGET_SELF|") {
		t.Fatalf("自 @ 应拒绝: %s", result)
	}
	if list := store.ReadMessages("组甲", 1, 10); len(list) != 1 || list[0].Seq != 2 || list[0].To != "b" {
		t.Fatalf("after 读取不符: %+v", list)
	}
	tail, hasMore := store.ReadTailMessages("组甲", 1)
	if len(tail) != 1 || tail[0].Seq != 2 || !hasMore {
		t.Fatalf("尾部读取不符: %+v hasMore=%v", tail, hasMore)
	}
	info := store.GetGroup("组甲")
	if info.LastSeq != 2 || info.MsgCount != 2 || info.LastAt == 0 {
		t.Fatalf("统计不符: %+v", info)
	}
}

// TestEnabledGate 停用组拒绝追加；启用幂等。
func TestEnabledGate(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"a"}, "", nil, nil, -1); result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	if result := store.SetEnabled("组甲", false); result != "ok|组甲" {
		t.Fatalf("停用失败: %s", result)
	}
	if result := store.StopGroup("组甲"); result != "ok|组甲" {
		t.Fatalf("停组失败: %s", result)
	}
	if store.IsRunning("组甲") {
		t.Fatal("停用后仍判运行中")
	}
	if result := store.AppendMessage("组甲", "", "note", "x", "", ""); result != "ERR|GROUP_NOT_RUNNING|组未启用: 组甲" {
		t.Fatalf("停用组应拒绝追加: %s", result)
	}
	if result := store.StartGroup("组甲"); result != "ok|组甲" {
		t.Fatalf("启动失败: %s", result)
	}
	if !store.IsRunning("组甲") {
		t.Fatal("启动后仍判未运行")
	}
	if result := store.StartGroup("组甲"); result != "ok|组甲" {
		t.Fatalf("重复启动应幂等: %s", result)
	}
}

// TestClearMessages 清空归档 + 统计与游标归零。
func TestClearMessages(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"a"}, "", nil, []string{"a"}, -1); result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "note", "x", "", ""); result != "ok|组甲|seq=1" {
		t.Fatalf("追加失败: %s", result)
	}
	if result := store.SetSubscriberCursor("组甲", "a", 1); result != "ok|组甲|a|1" {
		t.Fatalf("游标写入失败: %s", result)
	}
	result := store.ClearMessages("组甲")
	parts := strings.Split(result, "|")
	if len(parts) != 3 || parts[0] != "ok" || parts[1] != "组甲" || !strings.HasSuffix(parts[2], ".jsonl.bak") {
		t.Fatalf("清空回执不符: %s", result)
	}
	info := store.GetGroup("组甲")
	if info.LastSeq != 0 || info.MsgCount != 0 || len(info.SubscribeCursors) != 0 {
		t.Fatalf("清空后统计未归零: %+v", info)
	}
	if result := store.AppendMessage("组甲", "a", "note", "y", "", ""); result != "ok|组甲|seq=1" {
		t.Fatalf("清空后序号应重新从 1 起: %s", result)
	}
	if _, err := os.Stat(filepath.Join(store.root, "组甲", parts[2])); err != nil {
		t.Fatalf("归档文件不存在: %v", err)
	}
}

// TestLoadRoundTrip 重启续接——落盘后重载得到同统计。
func TestLoadRoundTrip(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"a"}, "项目A", []string{"a"}, []string{"a"}, 120); result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	if result := store.AppendMessage("组甲", "a", "delegation", "派单", "r1", ""); result != "ok|组甲|seq=1" {
		t.Fatalf("追加失败: %s", result)
	}
	reloaded := New(store.root, nil)
	reloaded.Load()
	info := reloaded.GetGroup("组甲")
	if info == nil {
		t.Fatal("重载后组不存在")
	}
	if info.LastSeq != 1 || info.MsgCount != 1 || info.Project != "项目A" || info.SubscribeIdleSec != 120 || !info.Running {
		t.Fatalf("重载统计不符: %+v", info)
	}
	if len(info.MemberKeys) != 1 || info.MemberKeys[0] != "a" {
		t.Fatalf("重载成员不符: %v", info.MemberKeys)
	}
}

// TestDeleteToTrash 删组——整目录移入 _trash，缓存移除。
func TestDeleteToTrash(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"a"}, "", nil, nil, -1); result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	if result := store.Delete("组甲"); result != "ok|组甲" {
		t.Fatalf("删组失败: %s", result)
	}
	if store.GetGroup("组甲") != nil {
		t.Fatal("删组后缓存未移除")
	}
	entries, err := os.ReadDir(filepath.Join(store.root, "_trash"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("回收目录不符: %v / %v", entries, err)
	}
	if result := store.Delete("组甲"); !strings.HasPrefix(result, "ERR|GROUP_NOT_FOUND|") {
		t.Fatalf("重复删组应报不存在: %s", result)
	}
}

// TestBadMessageLine 坏行跳过且不污染统计。
func TestBadMessageLine(t *testing.T) {
	store := newTestStore(t)
	if result := store.CreateOrUpdate("组甲", []string{"a"}, "", nil, nil, -1); result != "ok|组甲" {
		t.Fatalf("新建组失败: %s", result)
	}
	path := filepath.Join(store.root, "组甲", "messages.jsonl")
	body := "{\"seq\":1,\"from\":\"a\",\"type\":\"note\",\"text\":\"ok\",\"ts\":10}\nnot-json\n{\"from\":\"a\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写样本失败: %v", err)
	}
	reloaded := New(store.root, nil)
	reloaded.Load()
	info := reloaded.GetGroup("组甲")
	if info.MsgCount != 1 || info.LastSeq != 1 || info.LastAt != 10 {
		t.Fatalf("坏行统计不符: %+v", info)
	}
}
