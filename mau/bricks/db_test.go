package bricks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/db"
)

// newDBService 建临时库根的服务。
func newDBService(t *testing.T) *db.Service {
	t.Helper()
	svc, err := db.New(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("建库服务失败: %v", err)
	}
	return svc
}

// TestDbNoService 服务未注入——八件全返 DB_NO_SERVICE（不静默）。
func TestDbNoService(t *testing.T) {
	cases := []struct {
		name string
		call func(*db.Service, string) (string, bool)
		args string
	}{
		{"db.put", DbPut, `{"lib":"liba","title":"标题","body":"正文"}`},
		{"db.query", DbQuery, `{"lib":"liba"}`},
		{"db.get", DbGet, `{"lib":"liba","key":"键"}`},
		{"db.update", DbUpdate, `{"lib":"liba","title":"标题","body":"正文"}`},
		{"db.delete", DbDelete, `{"lib":"liba","title":"标题"}`},
		{"dbAdmin.libs", DbAdminLibs, `{}`},
		{"dbAdmin.create", DbAdminCreate, `{"lib":"liba"}`},
		{"dbAdmin.drop", DbAdminDrop, `{"lib":"liba","confirm":"liba"}`},
	}
	for _, item := range cases {
		got, ok := item.call(nil, item.args)
		if ok {
			t.Fatalf("%s 未注入服务时应失败: %q", item.name, got)
		}
		if got != "ERR|DB_NO_SERVICE|宿主未注入 DbService" {
			t.Fatalf("%s 未注入服务回执不符: %q", item.name, got)
		}
	}
}

// TestDbParamFace 参数面零容忍——未知参数 / 缺必填 / 枚举值。
func TestDbParamFace(t *testing.T) {
	svc := newDBService(t)
	cases := []struct {
		name string
		call func(*db.Service, string) (string, bool)
		args string
		want string
	}{
		{"db.put 未知参数", DbPut, `{"lib":"liba","title":"t","body":"b","extra":1}`, "ERR|BAD_ARGS|未知参数: extra（支持 lib title body）"},
		{"db.put 缺 body", DbPut, `{"lib":"liba","title":"t"}`, "ERR|BAD_ARGS|缺参数 body（必填：lib title body）"},
		{"db.query limit 非整数", DbQuery, `{"lib":"liba","limit":"abc"}`, "ERR|BAD_ARGS|limit 非整数: abc"},
		{"db.query limit 越界", DbQuery, `{"lib":"liba","limit":201}`, "ERR|BAD_ARGS|limit 越界（1–200）: 201"},
		{"db.query offset 负数", DbQuery, `{"lib":"liba","offset":-1}`, "ERR|BAD_ARGS|offset 不能为负: -1"},
		{"db.get mode 缺 key", DbGet, `{"lib":"liba"}`, "ERR|BAD_ARGS|缺参数 key（必填：lib key）"},
		{"db.delete 未知参数", DbDelete, `{"lib":"liba","title":"t","body":"b"}`, "ERR|BAD_ARGS|未知参数: body（支持 lib title）"},
		{"dbAdmin.libs 未知参数", DbAdminLibs, `{"lib":"liba"}`, "ERR|BAD_ARGS|未知参数: lib（本工具无参数）"},
		{"dbAdmin.create 缺 lib", DbAdminCreate, `{}`, "ERR|BAD_ARGS|缺参数 lib（必填：lib）"},
		{"dbAdmin.drop 缺 confirm", DbAdminDrop, `{"lib":"liba"}`, "ERR|BAD_ARGS|缺参数 confirm（必填：lib confirm）"},
	}
	for _, item := range cases {
		got, ok := item.call(svc, item.args)
		if ok {
			t.Fatalf("%s 应失败: %q", item.name, got)
		}
		if got != item.want {
			t.Fatalf("%s 回执不符:\n实得 %q\n期望 %q", item.name, got, item.want)
		}
	}
}

// TestDbRoundTrip 读写往返——建库 / 增 / 查 / 读 / 改 / 删 / 明细 / 删库全链回执形态。
func TestDbRoundTrip(t *testing.T) {
	svc := newDBService(t)
	db.ClearPolicy()
	defer db.ClearPolicy()

	got, ok := DbAdminCreate(svc, `{"lib":"probe"}`)
	if !ok || !strings.HasPrefix(got, `{"ok":true,"tool":"dbAdmin-create","target":"probe"}`+"\n库已建立: probe\n{\"id\":\"probe\",\"createdAt\":") {
		t.Fatalf("建库回执不符: %q", got)
	}
	got, ok = DbPut(svc, `{"lib":"probe","title":"标题甲","body":"正文一"}`)
	if !ok || !strings.HasPrefix(got, "{\"ok\":true,\"tool\":\"db-put\",\"target\":\"probe\"}\n条目已写入: #1\n{\"id\":1,\"v\":1}") {
		t.Fatalf("写入回执不符: %q", got)
	}
	got, ok = DbQuery(svc, `{"lib":"probe","keyword":"标题","limit":10}`)
	if !ok || !strings.Contains(got, "命中 1 / 活跃 1（本页 1 · 偏移 0）") ||
		!strings.Contains(got, `"items":[{"id":1,"title":"标题甲","updatedAt":"`) {
		t.Fatalf("查询回执不符: %q", got)
	}
	got, ok = DbGet(svc, `{"lib":"probe","key":"标题甲"}`)
	if !ok || !strings.Contains(got, `"body":"正文一"`) {
		t.Fatalf("读取回执不符: %q", got)
	}
	got, ok = DbUpdate(svc, `{"lib":"probe","title":"标题甲","body":"正文二"}`)
	if !ok || !strings.Contains(got, "条目已更新: #1 v2") {
		t.Fatalf("更新回执不符: %q", got)
	}
	got, ok = DbDelete(svc, `{"lib":"probe","title":"标题甲"}`)
	if !ok || !strings.Contains(got, "条目已删除（墓碑保留）: #1") {
		t.Fatalf("删除回执不符: %q", got)
	}
	if got, ok := DbGet(svc, `{"lib":"probe","key":"标题甲"}`); ok || !strings.Contains(got, "ERR|DB_ENTRY_NOT_FOUND|条目未命中（title=标题甲）") {
		t.Fatalf("墓碑应视为不存在: %q", got)
	}
	got, ok = DbAdminLibs(svc, `{}`)
	if !ok || !strings.Contains(got, `"tombstones":1`) || !strings.Contains(got, `"entries":0`) {
		t.Fatalf("库级明细不符: %q", got)
	}
	got, ok = DbAdminDrop(svc, `{"lib":"probe","confirm":"probe"}`)
	if !ok || !strings.Contains(got, `"trash":`) {
		t.Fatalf("删库回执不符: %q", got)
	}
	if got, ok := DbQuery(svc, `{"lib":"probe"}`); ok || !strings.Contains(got, "ERR|DB_LIB_NOT_FOUND|库未登记: probe") {
		t.Fatalf("删库后查询应报未登记: %q", got)
	}
}

// TestDbWhitelistDenied 白名单拒绝——未授权库五件全返 DB_LIB_DENIED。
func TestDbWhitelistDenied(t *testing.T) {
	svc := newDBService(t)
	db.ClearPolicy()
	defer db.ClearPolicy()
	if _, ok := DbAdminCreate(svc, `{"lib":"lib-a"}`); !ok {
		t.Fatal("建库失败")
	}
	if _, ok := DbAdminCreate(svc, `{"lib":"lib-b"}`); !ok {
		t.Fatal("建库失败")
	}
	db.RegisterCat("cat-x", false, []string{"lib-b"})
	cases := []struct {
		name string
		call func(*db.Service, string) (string, bool)
		args string
	}{
		{"db.put", DbPut, `{"lib":"lib-a","title":"t","body":"b","catId":"cat-x"}`},
		{"db.query", DbQuery, `{"lib":"lib-a","catId":"cat-x"}`},
		{"db.get", DbGet, `{"lib":"lib-a","key":"t","catId":"cat-x"}`},
		{"db.update", DbUpdate, `{"lib":"lib-a","title":"t","body":"b","catId":"cat-x"}`},
		{"db.delete", DbDelete, `{"lib":"lib-a","title":"t","catId":"cat-x"}`},
	}
	for _, item := range cases {
		got, ok := item.call(svc, item.args)
		if ok || !strings.Contains(got, "ERR|DB_LIB_DENIED|库未授权（lib-a 不在猫 cat-x 的 dbLibs 白名单）") {
			t.Fatalf("%s 未授权库未拦: %q", item.name, got)
		}
	}
	// 授权库可写；catId 保留键放行
	if got, ok := DbPut(svc, `{"lib":"lib-b","title":"t","body":"b","catId":"cat-x"}`); !ok {
		t.Fatalf("授权库写入失败: %q", got)
	}
	// 库池按授权过滤——摘要自证全集
	got, ok := DbAdminLibs(svc, `{"catId":"cat-x"}`)
	if !ok || !strings.Contains(got, "库 1（授权内 1 / 共 2——1 个未授权已隐藏）") {
		t.Fatalf("库池过滤不符: %q", got)
	}
}

// TestDbServiceErrorCodes 错误码面——库存在 / 标题冲突 / 未命中 / 参数非法 / confirm 不符。
func TestDbServiceErrorCodes(t *testing.T) {
	svc := newDBService(t)
	db.ClearPolicy()
	defer db.ClearPolicy()
	if _, ok := DbAdminCreate(svc, `{"lib":"codes"}`); !ok {
		t.Fatal("建库失败")
	}
	cases := []struct {
		name string
		call func(*db.Service, string) (string, bool)
		args string
		want string
	}{
		{"重复建库", DbAdminCreate, `{"lib":"codes"}`, "ERR|DB_LIB_EXISTS|库已存在: codes"},
		{"非法库名", DbAdminCreate, `{"lib":"A"}`, "ERR|BAD_ARGS|库名不合法（小写字母 / 数字 / 短横线，2–32 位）: A"},
		{"未登记库", DbQuery, `{"lib":"nope"}`, "ERR|DB_LIB_NOT_FOUND|库未登记: nope"},
		{"confirm 不符", DbAdminDrop, `{"lib":"codes","confirm":"other"}`, "ERR|BAD_ARGS|confirm 须与库名完全一致（确认删库）: other"},
		{"改不存在标题", DbUpdate, `{"lib":"codes","title":"没有","body":"b"}`, "ERR|DB_TITLE_NOT_FOUND|标题不存在: 没有"},
		{"读不存在条目", DbGet, `{"lib":"codes","key":"1","mode":"id"}`, "ERR|DB_ENTRY_NOT_FOUND|条目未命中（id=1）"},
		{"标题超长", DbPut, `{"lib":"codes","title":"` + strings.Repeat("标", 51) + `","body":"b"}`, "ERR|DB_TITLE_TOO_LONG|标题超长（上限 50 字符，实际 51）"},
	}
	for _, item := range cases {
		got, ok := item.call(svc, item.args)
		if ok {
			t.Fatalf("%s 应失败: %q", item.name, got)
		}
		if got != item.want {
			t.Fatalf("%s 回执不符:\n实得 %q\n期望 %q", item.name, got, item.want)
		}
	}
}

// openAppend 打开文件追加写。
func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
}

// TestDbBadLineHead 坏行——查询头带 badLines 字段（>0 才出现）。
func TestDbBadLineHead(t *testing.T) {
	svc := newDBService(t)
	db.ClearPolicy()
	defer db.ClearPolicy()
	if _, ok := DbAdminCreate(svc, `{"lib":"bad"}`); !ok {
		t.Fatal("建库失败")
	}
	// 人工塞坏行：直接拿服务根路径拼文件
	path := filepath.Join(svc.Root(), "bad", "entries.jsonl")
	file, err := openAppend(path)
	if err != nil {
		t.Fatalf("打开条目文件失败: %v", err)
	}
	if _, err := file.WriteString("这不是 JSON\n"); err != nil {
		t.Fatalf("写坏行失败: %v", err)
	}
	_ = file.Close()
	got, ok := DbQuery(svc, `{"lib":"bad"}`)
	if !ok || !strings.HasPrefix(got, `{"ok":true,"tool":"db-query","target":"bad","badLines":1}`+"\n命中 0 / 活跃 0（本页 0 · 偏移 0）") {
		t.Fatalf("坏行头不符: %q", got)
	}
}
