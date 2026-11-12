package toolpool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeToolsBrick 造一个工具定义积木源码（形态照 Bricks/TOOLS）。
func writeToolsBrick(t *testing.T, dir, name string, literals ...string) {
	t.Helper()
	quoted := make([]string, 0, len(literals))
	for _, lit := range literals {
		quoted = append(quoted, `"`+strings.ReplaceAll(lit, `"`, `\"`)+`"`)
	}
	src := "namespace Mau.Bricks\n{\n    public static class X\n    {\n        public static string GetToolsJson()\n        {\n            return " +
		strings.Join(quoted, " +\n                ") + ";\n        }\n    }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("写积木源码失败: %v", err)
	}
}

// writeIndex 造一个 Bricks/index.json。
func writeIndex(t *testing.T, dir string, entries []string) string {
	t.Helper()
	body := `{"version":3,"bricks":[` + strings.Join(entries, ",") + `]}`
	path := filepath.Join(dir, "index.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写 index.json 失败: %v", err)
	}
	return path
}

// TestLoadFromBricks 聚合两个工具组并按组名字母序排序。
func TestLoadFromBricks(t *testing.T) {
	dir := t.TempDir()
	writeToolsBrick(t, dir, "BRIK-TOOLS-001_tools.textcat.cs",
		`{"group":"TextCat","tools":[`,
		`{"name":"text-read","description":"读取文件","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}},`,
		`{"name":"text-write","description":"写入文件","parameters":{"type":"object","properties":{}}}`,
		`]}`)
	writeToolsBrick(t, dir, "BRIK-TOOLS-009_tools.majordomocat.cs",
		`{"group":"Majordomo","privileged":true,"tools":[`,
		`{"name":"majordomo-cmd","description":"宿主管理指令","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}`,
		`]}`)
	index := writeIndex(t, dir, []string{
		`{"id":"1","name":"tools.textcat","category":"TOOLS","path":"BRIK-TOOLS-001_tools.textcat.cs"}`,
		`{"id":"2","name":"tools.majordomocat","category":"TOOLS","path":"BRIK-TOOLS-009_tools.majordomocat.cs"}`,
		`{"id":"3","name":"text.read","category":"TEXT","path":"x.cs"}`,
	})
	groups, err := LoadFromBricks(index)
	if err != nil {
		t.Fatalf("聚合失败: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("组数 = %d，期望 2（非 TOOLS 类别不计）", len(groups))
	}
	if groups[0].Name != "Majordomo" || groups[1].Name != "TextCat" {
		t.Fatalf("组序 = %s/%s，期望字母序 Majordomo/TextCat", groups[0].Name, groups[1].Name)
	}
	if !strings.Contains(groups[1].JSON, `"text-read"`) {
		t.Fatalf("TextCat 定义缺失 text-read: %s", groups[1].JSON)
	}
}

// TestPoolRebuildWithBuiltin 池装配：组 + 内置、order 注记、特权标记。
func TestPoolRebuildWithBuiltin(t *testing.T) {
	groups := []GroupSource{
		{Name: "TextCat", JSON: `{"group":"TextCat","tools":[{"name":"text-read","description":"读取文件","parameters":{"type":"object","properties":{}}}]}`},
		{Name: "Majordomo", JSON: `{"group":"Majordomo","privileged":true,"tools":[{"name":"majordomo-cmd","description":"宿主管理指令","parameters":{"type":"object","properties":{}}}]}`},
	}
	RebuildAll(groups, BuiltinToolsJSON())
	pool := Current()
	if pool.MemberCount() != 13 {
		t.Fatalf("池内工具 = %d，期望 13（11 内置 + 2 组）", pool.MemberCount())
	}
	if got := pool.AllNames()[0]; got != "text-read" {
		t.Fatalf("首条 = %s，期望 text-read（组先于内置）", got)
	}
	read, _ := pool.byName["text-read"]
	if !strings.HasSuffix(read.Description, "order: -1") {
		t.Fatalf("text-read 描述尾注 = %q，期望 order: -1", read.Description)
	}
	cmd, _ := pool.byName["majordomo-cmd"]
	if !cmd.Privileged {
		t.Fatalf("majordomo-cmd 应为特权（组级声明）")
	}
	if !strings.HasSuffix(cmd.Description, "order: 3") {
		t.Fatalf("majordomo-cmd 描述尾注 = %q，期望 order: 3", cmd.Description)
	}
	defects := pool.Defects()
	if len(defects) != 0 {
		t.Fatalf("缺陷 = %v，期望空", defects)
	}
}

// TestPoolDefects 四条缺陷路径全部出声。
func TestPoolDefects(t *testing.T) {
	RebuildAll([]GroupSource{
		{Name: "Empty", JSON: ""},
		{Name: "Parse", JSON: `{"group":"Parse",`},
		{Name: "NoTools", JSON: `{"group":"NoTools"}`},
		{Name: "BadItems", JSON: `{"group":"BadItems","tools":[{"description":"x"},1]}`},
		{Name: "Silent", JSON: `{"group":"Silent"}`},
	}, `{"group":"","tools":[]}`)
	stages := make(map[string]string, 4)
	for _, d := range Current().Defects() {
		stages[d.Group] = d.Stage
	}
	if stages["Empty"] != "shape" || stages["Parse"] != "parse" || stages["NoTools"] != "shape" || stages["BadItems"] != "shape" {
		t.Fatalf("缺陷阶段 = %v", stages)
	}
}

// TestOrderTable 执行序档位与分批。
func TestOrderTable(t *testing.T) {
	cases := map[string]int{
		"timeback-start": -100, "timeback-back": 100, "text-read": -1,
		"text-write": 1, "cs-build": 2, "mau-proj": 3, "sleep": 0, "unknown-tool": 0,
	}
	for name, want := range cases {
		if got := ResolveOrder(name); got != want {
			t.Fatalf("%s order = %d，期望 %d", name, got, want)
		}
	}
	names := []string{"text-read", "cs-build", "text-read_lines", "powershell"}
	orders := []int{ResolveOrder(names[0]), ResolveOrder(names[1]), ResolveOrder(names[2]), ResolveOrder(names[3])}
	plan := PlanBatches(names, orders)
	if len(plan) != 3 {
		t.Fatalf("批数 = %d，期望 3（只读同批 / 独占 / 构建）: %v", len(plan), plan)
	}
	if len(plan[0]) != 2 || len(plan[1]) != 1 || len(plan[2]) != 1 {
		t.Fatalf("批内下标 = %v", plan)
	}
	if missing := FindUnregistered([]string{"text-read", "nope"}); len(missing) != 1 || missing[0] != "nope" {
		t.Fatalf("未登记 = %v，期望 [nope]", missing)
	}
}
