package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRoot 建临时库根（每次测试独立目录）。
func newRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "db")
	svc, err := New(root)
	if err != nil {
		t.Fatalf("建服务失败: %v", err)
	}
	if svc.Root() == "" {
		t.Fatal("库根为空")
	}
	return root
}

// openService 打开已有库根的服务。
func openService(t *testing.T, root string) *Service {
	t.Helper()
	svc, err := New(root)
	if err != nil {
		t.Fatalf("建服务失败: %v", err)
	}
	return svc
}

// entriesPath 条目文件路径。
func entriesPath(root, lib string) string {
	return filepath.Join(root, lib, entriesFile)
}

// readJSONInt 从载荷里取整数字段（用标准解析，不做顺序假设）。
func readJSONInt(t *testing.T, payload, name string) int {
	t.Helper()
	value := jsonFieldInt(payload, name)
	if value == nil {
		t.Fatalf("载荷缺字段 %s: %s", name, payload)
	}
	return *value
}

// jsonFieldInt 取 JSON 对象的整数字段。
func jsonFieldInt(payload, name string) *int {
	var raw map[string]any
	if err := jsonUnmarshal(payload, &raw); err != nil {
		return nil
	}
	value, ok := raw[name]
	if !ok {
		return nil
	}
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	parsed := int(number)
	return &parsed
}

// TestSixUseCasesFullChain 六用例主链——建库 / 增 / 查 / 读（标题与 ID 同源）/ 改（v+1）/ 删（墓碑保留、标题释放）。
func TestSixUseCasesFullChain(t *testing.T) {
	root := newRoot(t)
	svc := openService(t, root)
	if !svc.Create("liba", "").OK {
		t.Fatal("建库失败")
	}
	put := svc.Put("liba", "时间back 域类型门禁", "正文一", "")
	if !put.OK || readJSONInt(t, put.Data, "id") != 1 {
		t.Fatalf("写入失败: %+v", put)
	}
	query := svc.Query("liba", "门禁", 10, 0, "", "")
	if !query.OK || readJSONInt(t, query.Data, "total") != 1 ||
		readJSONInt(t, query.Data, "matched") != 1 || readJSONInt(t, query.Data, "shown") != 1 {
		t.Fatalf("查询三数不符: %+v", query)
	}
	byTitle := svc.Get("liba", "title", "时间back 域类型门禁", "")
	byID := svc.Get("liba", "id", "1", "")
	if !byTitle.OK || !byID.OK {
		t.Fatalf("读取失败: %+v / %+v", byTitle, byID)
	}
	if jsonFieldString(byTitle.Data, "body") != jsonFieldString(byID.Data, "body") {
		t.Fatal("标题与 ID 读取正文不一致")
	}
	update := svc.Update("liba", "时间back 域类型门禁", "正文二", "")
	if !update.OK || readJSONInt(t, update.Data, "v") != 2 {
		t.Fatalf("更新失败: %+v", update)
	}
	if jsonFieldString(svc.Get("liba", "id", "1", "").Data, "body") != "正文二" {
		t.Fatal("更新后正文不符")
	}
	if del := svc.Delete("liba", "时间back 域类型门禁", ""); !del.OK {
		t.Fatalf("删除失败: %+v", del)
	}
	if total := readJSONInt(t, svc.Query("liba", "", 10, 0, "", "").Data, "total"); total != 0 {
		t.Fatalf("删除后活跃条数 %d", total)
	}
	gone := svc.Get("liba", "id", "1", "")
	if gone.OK || gone.Code != "DB_ENTRY_NOT_FOUND" {
		t.Fatalf("墓碑应视为不存在: %+v", gone)
	}
	lines := readLines(t, entriesPath(root, "liba"))
	if len(lines) != 4 {
		t.Fatalf("条目行数 %d（期望 1 头行 + 3 记录）", len(lines))
	}
}

// TestHundredPutsAppendOnly 100 条写入——行数 = 101（含头行）；查询三数自证。
func TestHundredPutsAppendOnly(t *testing.T) {
	root := newRoot(t)
	svc := openService(t, root)
	if !svc.Create("bulk", "").OK {
		t.Fatal("建库失败")
	}
	for i := 1; i <= 100; i++ {
		got := svc.Put("bulk", "条目"+itoa(i), "正文"+itoa(i), "")
		if !got.OK {
			t.Fatalf("第 %d 条写入失败: %+v", i, got)
		}
	}
	if lines := readLines(t, entriesPath(root, "bulk")); len(lines) != 101 {
		t.Fatalf("条目行数 %d（期望 101）", len(lines))
	}
	all := svc.Query("bulk", "", 200, 0, "", "")
	if readJSONInt(t, all.Data, "total") != 100 || readJSONInt(t, all.Data, "matched") != 100 ||
		readJSONInt(t, all.Data, "shown") != 100 {
		t.Fatalf("全量三数不符: %+v", all)
	}
	page := svc.Query("bulk", "", 10, 0, "", "")
	if readJSONInt(t, page.Data, "shown") != 10 || readJSONInt(t, page.Data, "matched") != 100 {
		t.Fatalf("分页三数不符: %+v", page)
	}
}

// TestTitleKeyConflicts 唯一键与不存在——同标题重复增 / 改删不存在标题。
func TestTitleKeyConflicts(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("keys", "").OK {
		t.Fatal("建库失败")
	}
	if !svc.Put("keys", "同名", "一", "").OK {
		t.Fatal("首条写入失败")
	}
	if dup := svc.Put("keys", "同名", "二", ""); dup.OK || dup.Code != "DB_TITLE_EXISTS" {
		t.Fatalf("重复标题未拦: %+v", dup)
	}
	if upd := svc.Update("keys", "没有这条", "正文", ""); upd.Code != "DB_TITLE_NOT_FOUND" {
		t.Fatalf("更新未命中码不符: %+v", upd)
	}
	if del := svc.Delete("keys", "没有这条", ""); del.Code != "DB_TITLE_NOT_FOUND" {
		t.Fatalf("删除未命中码不符: %+v", del)
	}
}

// TestTooLongRejected 硬限拒写——标题 51 / 正文 5001 具名出声；50 与 5000 放行。
func TestTooLongRejected(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("limits", "").OK {
		t.Fatal("建库失败")
	}
	if got := svc.Put("limits", strings.Repeat("标", 51), "正文", ""); got.Code != "DB_TITLE_TOO_LONG" || got.OK {
		t.Fatalf("超长标题未拦: %+v", got)
	}
	if got := svc.Put("limits", "标题", strings.Repeat("文", 5001), ""); got.Code != "DB_BODY_TOO_LONG" || got.OK {
		t.Fatalf("超长正文未拦: %+v", got)
	}
	if got := svc.Put("limits", strings.Repeat("标", 50), strings.Repeat("文", 5000), ""); !got.OK {
		t.Fatalf("边界值应放行: %+v", got)
	}
}

// TestBadLineSkippedAndCounted 坏行出声跳过——查询仍可用，坏行计数进结果。
func TestBadLineSkippedAndCounted(t *testing.T) {
	root := newRoot(t)
	svc := openService(t, root)
	if !svc.Create("bad", "").OK {
		t.Fatal("建库失败")
	}
	if !svc.Put("bad", "好条目", "正文", "").OK {
		t.Fatal("写入失败")
	}
	file, err := os.OpenFile(entriesPath(root, "bad"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("打开条目文件失败: %v", err)
	}
	_, _ = file.WriteString("这不是 JSON\n")
	_ = file.Close()
	query := svc.Query("bad", "", 10, 0, "", "")
	if !query.OK || query.BadLines != 1 || readJSONInt(t, query.Data, "total") != 1 {
		t.Fatalf("坏行口径不符: %+v", query)
	}
}

// TestFormatUnsupportedRejected 头行 fmt 非实现版本 → DB_FORMAT_UNSUPPORTED（不回落空库）。
func TestFormatUnsupportedRejected(t *testing.T) {
	root := newRoot(t)
	svc := openService(t, root)
	if !svc.Create("fmtlib", "").OK {
		t.Fatal("建库失败")
	}
	path := entriesPath(root, "fmtlib")
	content := "{\"_\":\"db.entries\",\"fmt\":9,\"lib\":\"fmtlib\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("覆写头行失败: %v", err)
	}
	query := svc.Query("fmtlib", "", 10, 0, "", "")
	if query.OK || query.Code != "DB_FORMAT_UNSUPPORTED" {
		t.Fatalf("格式不符未拒: %+v", query)
	}
}

// TestLibGuards 库存在性——未登记库拒、库名非法拒、重复建库出声。
func TestLibGuards(t *testing.T) {
	root := newRoot(t)
	svc := openService(t, root)
	if got := svc.Query("nope", "", 10, 0, "", ""); got.Code != "DB_LIB_NOT_FOUND" {
		t.Fatalf("未登记库未拒: %+v", got)
	}
	if got := svc.Create("A", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("非法库名未拒: %+v", got)
	}
	if !svc.Create("ok-lib", "").OK {
		t.Fatal("建库失败")
	}
	if got := svc.Create("ok-lib", ""); got.Code != "DB_LIB_EXISTS" {
		t.Fatalf("重复建库未出声: %+v", got)
	}
	if err := os.MkdirAll(filepath.Join(root, "ghost"), 0o755); err != nil {
		t.Fatalf("造目录失败: %v", err)
	}
	if got := svc.Query("ghost", "", 10, 0, "", ""); got.Code != "DB_LIB_NOT_FOUND" {
		t.Fatalf("目录存在但未登记应拒: %+v", got)
	}
}

// TestTitleReleasedAfterDelete 删除后同标题可再建，得到新 ID。
func TestTitleReleasedAfterDelete(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("release", "").OK {
		t.Fatal("建库失败")
	}
	first := svc.Put("release", "可复用标题", "一", "")
	if !first.OK {
		t.Fatalf("首次写入失败: %+v", first)
	}
	if !svc.Delete("release", "可复用标题", "").OK {
		t.Fatal("删除失败")
	}
	second := svc.Put("release", "可复用标题", "二", "")
	if !second.OK || readJSONInt(t, second.Data, "id") != 2 {
		t.Fatalf("新 ID 不符: %+v", second)
	}
	if total := readJSONInt(t, svc.Query("release", "", 10, 0, "", "").Data, "total"); total != 1 {
		t.Fatalf("活跃条数 %d", total)
	}
}

// TestContractTitleNormalization 标题归一（Trim + 忽略大小写）与控制字符拒绝。
func TestContractTitleNormalization(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("norm", "").OK {
		t.Fatal("建库失败")
	}
	if !svc.Put("norm", "  标题甲  ", "正文一", "").OK {
		t.Fatal("写入失败")
	}
	if got := svc.Put("norm", "标题甲", "正文二", ""); got.Code != "DB_TITLE_EXISTS" {
		t.Fatalf("Trim 后重复未拦: %+v", got)
	}
	if got := svc.Get("norm", "title", "  标题甲 ", ""); !got.OK {
		t.Fatalf("带空白标题应命中: %+v", got)
	}
	if !svc.Put("norm", "Chapter", "正文三", "").OK {
		t.Fatal("写入失败")
	}
	if got := svc.Put("norm", "chapter", "正文四", ""); got.Code != "DB_TITLE_EXISTS" {
		t.Fatalf("忽略大小写重复未拦: %+v", got)
	}
	if got := svc.Get("norm", "title", "CHAPTER", ""); !got.OK {
		t.Fatalf("忽略大小写命中失败: %+v", got)
	}
	if got := svc.Put("norm", "标题\n乙", "正文五", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("控制字符未拦: %+v", got)
	}
}

// TestContractArgumentBounds 参数边界——正文纯空白 / keyword 超长 / id 非正整数 / limit 上界。
func TestContractArgumentBounds(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("bounds", "").OK {
		t.Fatal("建库失败")
	}
	if !svc.Put("bounds", "条目一", "正文", "").OK {
		t.Fatal("写入失败")
	}
	if got := svc.Put("bounds", "条目二", "   ", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("空白正文未拦: %+v", got)
	}
	if got := svc.Query("bounds", strings.Repeat("k", 201), 10, 0, "", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("超长 keyword 未拦: %+v", got)
	}
	if got := svc.Get("bounds", "id", "abc", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("非法 id 未拦: %+v", got)
	}
	if got := svc.Get("bounds", "id", "0", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("零 id 未拦: %+v", got)
	}
	if got := svc.Query("bounds", "", 201, 0, "", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("limit 上界未拦: %+v", got)
	}
	zero := svc.Query("bounds", "", 0, 0, "", "")
	if !zero.OK || readJSONInt(t, zero.Data, "shown") != 1 {
		t.Fatalf("limit 0 应回落默认: %+v", zero)
	}
}

// TestQueryPagingOffsetAndOrder 分页与排序——三页自洽、超界空页、order 非枚举 / offset 负数拒。
func TestQueryPagingOffsetAndOrder(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("page", "").OK {
		t.Fatal("建库失败")
	}
	for i := 1; i <= 5; i++ {
		if !svc.Put("page", "分页"+itoa(i), "正文"+itoa(i), "").OK {
			t.Fatal("写入失败")
		}
	}
	p1 := svc.Query("page", "", 2, 0, "id", "")
	if readJSONInt(t, p1.Data, "shown") != 2 || readJSONInt(t, p1.Data, "offset") != 0 ||
		readJSONInt(t, p1.Data, "nextOffset") != 2 {
		t.Fatalf("第一页不符: %+v", p1)
	}
	p2 := svc.Query("page", "", 2, 2, "id", "")
	if readJSONInt(t, p2.Data, "shown") != 2 || readJSONInt(t, p2.Data, "nextOffset") != 4 {
		t.Fatalf("第二页不符: %+v", p2)
	}
	p3 := svc.Query("page", "", 2, 4, "id", "")
	if readJSONInt(t, p3.Data, "shown") != 1 || readJSONInt(t, p3.Data, "nextOffset") != 5 {
		t.Fatalf("第三页不符: %+v", p3)
	}
	if over := svc.Query("page", "", 2, 99, "id", ""); !over.OK || readJSONInt(t, over.Data, "shown") != 0 {
		t.Fatalf("超界页不符: %+v", over)
	}
	if bad := svc.Query("page", "", 2, 0, "kkk", ""); bad.Code != "BAD_ARGS" {
		t.Fatalf("order 非枚举未拦: %+v", bad)
	}
	if neg := svc.Query("page", "", 2, -1, "updated", ""); neg.Code != "BAD_ARGS" {
		t.Fatalf("负 offset 未拦: %+v", neg)
	}
	// 默认排序 = updated 倒序（后写在前）
	defaultOrder := svc.Query("page", "", 2, 0, "", "")
	if !strings.Contains(defaultOrder.Data, `"title":"分页5"`) || !strings.Contains(defaultOrder.Data, `"title":"分页4"`) {
		t.Fatalf("默认排序不符: %s", defaultOrder.Data)
	}
}

// TestAdminListDetailAndDrop 管理面——库级明细 / 删库 confirm 精确比对 / 删后查询 NOT_FOUND。
func TestAdminListDetailAndDrop(t *testing.T) {
	svc := openService(t, newRoot(t))
	if !svc.Create("admina", "").OK {
		t.Fatal("建库失败")
	}
	if !svc.Put("admina", "甲", "正文", "").OK || !svc.Put("admina", "乙", "正文", "").OK {
		t.Fatal("写入失败")
	}
	if !svc.Delete("admina", "乙", "").OK {
		t.Fatal("删除失败")
	}
	libs := svc.ListLibsDetail("")
	if !libs.OK || !strings.Contains(libs.Data, "admina") {
		t.Fatalf("库级明细不符: %+v", libs)
	}
	if !strings.Contains(libs.Data, `"tombstones":1`) || !strings.Contains(libs.Data, `"entries":1`) {
		t.Fatalf("明细计数不符: %s", libs.Data)
	}
	if got := svc.DropLib("admina", "other", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("错误 confirm 未拦: %+v", got)
	}
	if got := svc.DropLib("admina", "AdminA", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("大小写不同的 confirm 不应构成确认: %+v", got)
	}
	if got := svc.DropLib("admina", " admina ", ""); got.Code != "BAD_ARGS" {
		t.Fatalf("带空白 confirm 不应构成确认: %+v", got)
	}
	if got := svc.DropLib("nolibx", "nolibx", ""); got.Code != "DB_LIB_NOT_FOUND" {
		t.Fatalf("未登记库未拒: %+v", got)
	}
	drop := svc.DropLib("admina", "admina", "")
	if !drop.OK || !strings.Contains(drop.Data, `"trash"`) {
		t.Fatalf("删库失败: %+v", drop)
	}
	if after := svc.ListLibsDetail(""); strings.Contains(after.Data, "admina") {
		t.Fatalf("删后库仍出现: %s", after.Data)
	}
	if gone := svc.Query("admina", "", 10, 0, "", ""); gone.Code != "DB_LIB_NOT_FOUND" {
		t.Fatalf("删后查询码不符: %+v", gone)
	}
}

// TestFileFormats 落盘字节形态——meta / 头行 / 注册表 / 条目行逐字对齐 C# 键序。
func TestFileFormats(t *testing.T) {
	root := newRoot(t)
	svc := openService(t, root)
	if !svc.Create("shape", "").OK {
		t.Fatal("建库失败")
	}
	meta := readFile(t, filepath.Join(root, "shape", metaFile))
	if !strings.HasPrefix(meta, `{"lib":"shape","fmt":1,"createdAt":"`) || !strings.HasSuffix(meta, `"}`) {
		t.Fatalf("meta.json 形态不符: %s", meta)
	}
	head := readLines(t, entriesPath(root, "shape"))[0]
	if head != `{"_":"db.entries","fmt":1,"lib":"shape"}` {
		t.Fatalf("头行形态不符: %s", head)
	}
	index := readFile(t, filepath.Join(root, indexFile))
	if !strings.HasPrefix(index, `{"fmt":1,"libs":[{"id":"shape","createdAt":"`) {
		t.Fatalf("注册表形态不符: %s", index)
	}
	if !svc.Put("shape", "标题", "正文", "").OK {
		t.Fatal("写入失败")
	}
	lines := readLines(t, entriesPath(root, "shape"))
	if len(lines) != 2 {
		t.Fatalf("条目行数 %d", len(lines))
	}
	if !strings.Contains(lines[1], `"id":1,"title":"标题","body":"正文","v":1,"del":0,"createdAt":`) {
		t.Fatalf("条目行键序不符: %s", lines[1])
	}
}

// TestToolText 回执文本——成功三段式 / 失败 ERR|CODE|msg。
func TestToolText(t *testing.T) {
	ok := SuccessBad("条目已写入: #1", `{"id":1,"v":1}`, 0)
	if got := ok.ToolText("db-put", "liba"); got != "{\"ok\":true,\"tool\":\"db-put\",\"target\":\"liba\"}\n条目已写入: #1\n{\"id\":1,\"v\":1}" {
		t.Fatalf("成功回执不符: %q", got)
	}
	withBad := SuccessBad("命中 0 / 活跃 0（本页 0 · 偏移 0）", `{"total":0}`, 2)
	if !strings.Contains(withBad.ToolText("db-query", "liba"), `"badLines":2`) {
		t.Fatalf("坏行头缺失: %q", withBad.ToolText("db-query", "liba"))
	}
	noTarget := Success("库 0", `{"libs":[]}`)
	if got := noTarget.ToolText("dbAdmin-libs", ""); got != "{\"ok\":true,\"tool\":\"dbAdmin-libs\"}\n库 0\n{\"libs\":[]}" {
		t.Fatalf("无 target 回执不符: %q", got)
	}
	fail := Fail("DB_LIB_NOT_FOUND", "库未登记: demo")
	if got := fail.ToolText("db-get", "demo"); got != "ERR|DB_LIB_NOT_FOUND|库未登记: demo" {
		t.Fatalf("失败回执不符: %q", got)
	}
}

// TestPolicyEmptyCatAllowed 空 catID——无猫上下文放行。
func TestPolicyEmptyCatAllowed(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	if !IsAllowed("", "any-lib") {
		t.Fatal("空 catID 应放行")
	}
	if DenyReason("", "any-lib") != "" {
		t.Fatal("空 catID 拒绝原因应为空")
	}
}

// TestPolicyDefaultCatAllLibs 默认猫全放——登记 allLibs 后任意库放行。
func TestPolicyDefaultCatAllLibs(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	RegisterCat("majordomo", true, nil)
	if !IsAllowed("majordomo", "whatever") || !IsAllowed("majordomo", "another-lib") {
		t.Fatal("全放行猫被拒")
	}
}

// TestPolicyRegisteredEmptyAllDenied 登记空集合——fail-closed。
func TestPolicyRegisteredEmptyAllDenied(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	RegisterCat("cat-a", false, nil)
	reason := DenyReason("cat-a", "any-lib")
	if !strings.Contains(reason, "dbLibs 为空") {
		t.Fatalf("拒绝文案不符: %q", reason)
	}
}

// TestPolicyRegisteredSubset 白名单子集——只放登记库；库名大小写不敏感。
func TestPolicyRegisteredSubset(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	RegisterCat("cat-b", false, []string{"lib-a"})
	if !IsAllowed("cat-b", "lib-a") || !IsAllowed("cat-b", "LIB-A") {
		t.Fatal("白名单库被拒")
	}
	if reason := DenyReason("cat-b", "lib-b"); !strings.Contains(reason, "未授权") {
		t.Fatalf("未授权文案不符: %q", reason)
	}
}

// TestPolicyUnregisteredDenied 未登记猫——拒（fail-closed）。
func TestPolicyUnregisteredDenied(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	if reason := DenyReason("cat-x", "lib-a"); !strings.Contains(reason, "未登记") {
		t.Fatalf("未登记文案不符: %q", reason)
	}
}

// TestPolicyUnregisterThenDenied 注销后回到未登记态。
func TestPolicyUnregisterThenDenied(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	RegisterCat("cat-c", true, nil)
	if !IsAllowed("cat-c", "lib-a") {
		t.Fatal("注销前应放行")
	}
	UnregisterCat("cat-c")
	if IsAllowed("cat-c", "lib-a") {
		t.Fatal("注销后应拒")
	}
}

// TestServiceDeniedLib 服务层拦截——未授权库五件全返 DB_LIB_DENIED。
func TestServiceDeniedLib(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	svc := openService(t, newRoot(t))
	if !svc.Create("liba", "").OK || !svc.Create("libb", "").OK {
		t.Fatal("建库失败")
	}
	RegisterCat("cat-d", false, []string{"libb"})
	if got := svc.Put("liba", "标题", "正文", "cat-d"); got.Code != "DB_LIB_DENIED" {
		t.Fatalf("写入未拦: %+v", got)
	}
	if got := svc.Query("liba", "", 10, 0, "", "cat-d"); got.Code != "DB_LIB_DENIED" {
		t.Fatalf("查询未拦: %+v", got)
	}
	if got := svc.Get("liba", "id", "1", "cat-d"); got.Code != "DB_LIB_DENIED" {
		t.Fatalf("读取未拦: %+v", got)
	}
	if got := svc.Update("liba", "标题", "正文", "cat-d"); got.Code != "DB_LIB_DENIED" {
		t.Fatalf("更新未拦: %+v", got)
	}
	if got := svc.Delete("liba", "标题", "cat-d"); got.Code != "DB_LIB_DENIED" {
		t.Fatalf("删除未拦: %+v", got)
	}
	if got := svc.Put("libb", "标题", "正文", "cat-d"); !got.OK {
		t.Fatalf("授权库写入失败: %+v", got)
	}
}

// TestListLibsFiltered 清单过滤——ListLibs 只返回授权库；摘要自证全集。
func TestListLibsFiltered(t *testing.T) {
	ClearPolicy()
	defer ClearPolicy()
	svc := openService(t, newRoot(t))
	if !svc.Create("liba", "").OK || !svc.Create("libb", "").OK {
		t.Fatal("建库失败")
	}
	RegisterCat("cat-e", false, []string{"libb"})
	filtered := svc.ListLibs("cat-e")
	if !filtered.OK || !strings.Contains(filtered.Data, "libb") || strings.Contains(filtered.Data, `"liba"`) {
		t.Fatalf("过滤清单不符: %+v", filtered)
	}
	if !strings.Contains(filtered.Message, "1 个未授权已隐藏") {
		t.Fatalf("摘要未自证全集: %q", filtered.Message)
	}
	all := svc.ListLibs("")
	if !strings.Contains(all.Data, `"liba"`) || !strings.Contains(all.Data, `"libb"`) {
		t.Fatalf("全放清单不符: %s", all.Data)
	}
}

// jsonUnmarshal 标准 JSON 解析（测试用）。
func jsonUnmarshal(text string, out any) error {
	return json.Unmarshal([]byte(text), out)
}

// jsonFieldString 取 JSON 对象的字符串字段（缺失返回空串）。
func jsonFieldString(payload, name string) string {
	var raw map[string]any
	if err := jsonUnmarshal(payload, &raw); err != nil {
		return ""
	}
	value, _ := raw[name].(string)
	return value
}

// readLines 读文件行。
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件失败 %s: %v", path, err)
	}
	return splitLines(data)
}

// readFile 读整个文件。
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件失败 %s: %v", path, err)
	}
	return string(data)
}

// itoa 十进制渲染。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
