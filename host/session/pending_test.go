package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withPendingDir 临时接管 pending 落盘目录与停机态钩子（测试收尾恢复）。
func withPendingDir(t *testing.T, dir string) {
	t.Helper()
	prevDir := PendingStoreDir()
	prevRestart := HostRestarting
	ConfigurePendingStore(dir)
	HostRestarting = func() bool { return false }
	t.Cleanup(func() {
		ConfigurePendingStore(prevDir)
		HostRestarting = prevRestart
	})
}

// TestPendingPostRejectsEmpty 空内容 + 无附件才拒收（图片注入可无正文）。
func TestPendingPostRejectsEmpty(t *testing.T) {
	withPendingDir(t, t.TempDir())
	q := NewPendingQueue("t1")
	if q.Post("", SourceUser, "", "") {
		t.Fatal("空内容 + 无附件应拒收")
	}
	if q.Post("  ", SourceUser, "", "") != true {
		t.Fatal("空白正文非空串——应受理（C# 同口径：只判长度零）")
	}
	q2 := NewPendingQueue("t2")
	if !q2.Post("", SourceUser, `["D:/a.png"]`, "") {
		t.Fatal("有附件无正文应受理")
	}
}

// TestPendingMaterializeOnEnqueue 入队即物化——落盘格式与字段序。
func TestPendingMaterializeOnEnqueue(t *testing.T) {
	dir := t.TempDir()
	withPendingDir(t, dir)
	q := NewPendingQueue("cat-a")
	if !q.Post("你好", SourceUser, "", "group:g:1:user") {
		t.Fatal("应受理")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pending_cat-a.json"))
	if err != nil {
		t.Fatalf("入队后应已落盘: %v", err)
	}
	want := `{"entries":[{"content":"你好","source":"user","imagesJson":"","origin":"group:g:1:user","seq":0}]}`
	if string(raw) != want {
		t.Fatalf("落盘 = %s，期望 %s", string(raw), want)
	}
}

// TestPendingTakeAllOrderAndRemoval 两队列按到达序合并取尽 + 消费即移除。
func TestPendingTakeAllOrderAndRemoval(t *testing.T) {
	dir := t.TempDir()
	withPendingDir(t, dir)
	q := NewPendingQueue("cat-b")
	q.Post("u1", SourceUser, "", "")
	if got := q.PostSystem(SysKindTimer, "s1", ""); got != "OK" {
		t.Fatalf("sys 入队应成功: %s", got)
	}
	q.Post("u2", SourceUser, "", "")
	batch := q.TakeAll(true)
	if len(batch) != 3 {
		t.Fatalf("取尽 = %d 条，期望 3", len(batch))
	}
	order := []string{batch[0].Content, batch[1].Content, batch[2].Content}
	want := []string{"u1", "s1", "u2"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("到达序不符: %v，期望 %v", order, want)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pending_cat-b.json"))
	if string(raw) != `{"entries":[]}` {
		t.Fatalf("消费后应重写为空队列: %s", string(raw))
	}
}

// TestPendingTakeAllSysOnly sys-only 取尽不动人工队列（空闲注入开关语义）。
func TestPendingTakeAllSysOnly(t *testing.T) {
	withPendingDir(t, t.TempDir())
	q := NewPendingQueue("cat-c")
	q.Post("u1", SourceUser, "", "")
	q.PostSystem(SysKindSleep, "s1", "")
	batch := q.TakeAll(false)
	if len(batch) != 1 || batch[0].Content != "s1" {
		t.Fatalf("只应取 sys 队列: %+v", batch)
	}
	user, sys := q.Len()
	if user != 1 || sys != 0 {
		t.Fatalf("人工队列应保留: user=%d sys=%d", user, sys)
	}
}

// TestPendingReloadKeepsUserDropsSys 人工队列跨重启保留（seq 续接）· sys 队列重启丢弃。
func TestPendingReloadKeepsUserDropsSys(t *testing.T) {
	dir := t.TempDir()
	withPendingDir(t, dir)
	q := NewPendingQueue("cat-d")
	q.Post("u1", SourceUser, "", "")
	q.Post("u2", SourceUser, "", "")
	q.PostSystem(SysKindDelay, "s1", "")
	q.TakeAll(false) // sys 取出，人工保留

	q2 := NewPendingQueue("cat-d")
	user, sys := q2.Len()
	if user != 2 {
		t.Fatalf("人工队列应恢复 2 条，实得 %d", user)
	}
	if sys != 0 {
		t.Fatalf("sys 队列不应跨重启保留，实得 %d", sys)
	}
	q2.Post("u3", SourceUser, "", "")
	batch := q2.TakeAll(true)
	if batch[len(batch)-1].Seq != 2 {
		t.Fatalf("序号应续接为 2，实得 %d", batch[len(batch)-1].Seq)
	}
}

// TestPendingCorruptFile 损坏文件按空队列处理（不抛）。
func TestPendingCorruptFile(t *testing.T) {
	dir := t.TempDir()
	withPendingDir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "pending_cat-e.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := NewPendingQueue("cat-e")
	if user, _ := q.Len(); user != 0 {
		t.Fatalf("损坏文件应按空队列处理，实得 %d", user)
	}
}

// TestPendingSystemValidation 系统注入参数面零容忍。
func TestPendingSystemValidation(t *testing.T) {
	withPendingDir(t, t.TempDir())
	q := NewPendingQueue("cat-f")
	if got := q.PostSystem("bogus", "x", ""); got != "ERR|BAD_ARGS|未知系统注入类型: bogus" {
		t.Fatalf("未知类型回执不符: %s", got)
	}
	if got := q.PostSystem(SysKindSystemAuto, "   ", ""); got != "ERR|BAD_ARGS|系统注入内容为空" {
		t.Fatalf("空内容回执不符: %s", got)
	}
}

// TestPendingRestartingRejects 停机态拒收新输入；在途 sys 丢弃、人工保留。
func TestPendingRestartingRejects(t *testing.T) {
	withPendingDir(t, t.TempDir())
	q := NewPendingQueue("cat-g")
	q.Post("u1", SourceUser, "", "")
	q.PostSystem(SysKindRestart, "s1", "")
	HostRestarting = func() bool { return true }
	if q.Post("u2", SourceUser, "", "") {
		t.Fatal("停机态应拒收人工输入")
	}
	if got := q.PostSystem(SysKindSystemAuto, "x", ""); !strings.HasPrefix(got, "ERR|HOST_RESTARTING|") {
		t.Fatalf("停机态应拒收 sys 注入: %s", got)
	}
	if dropped := q.DropSysIfRestarting(); dropped != 1 {
		t.Fatalf("应丢弃 1 条在途 sys，实得 %d", dropped)
	}
	user, sys := q.Len()
	if user != 1 || sys != 0 {
		t.Fatalf("人工队列应保留: user=%d sys=%d", user, sys)
	}
}

// TestPendingFragmentProjection pending 段三字段投影（不投影附件面）。
func TestPendingFragmentProjection(t *testing.T) {
	withPendingDir(t, t.TempDir())
	q := NewPendingQueue("cat-h")
	q.Post("hi", SourceUser, `["D:/a.png"]`, "")
	got := q.BuildFragment()
	want := `{"entries":[{"content":"hi","source":"user","origin":""}]}`
	if got != want {
		t.Fatalf("投影 = %s，期望 %s", got, want)
	}
}
