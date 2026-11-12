package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceRootsPayload 造一份齐全的 roots 提交体（七固定根齐备；overrides 覆盖指定 id 的 path）。
func workspaceRootsPayload(t *testing.T, root string, overrides map[string]string) string {
	t.Helper()
	order := []string{"workspace", "mau", "mauout", "ccbp", "localgit", "data", "db"}
	paths := make(map[string]string, len(order))
	for _, id := range order {
		if id == "data" {
			paths[id] = root
			continue
		}
		paths[id] = filepath.Join(root, "roots", id)
	}
	for id, path := range overrides {
		paths[id] = path
	}
	items := make([]map[string]any, 0, len(order))
	for _, id := range order {
		items = append(items, map[string]any{"id": id, "path": paths[id], "writable": true, "note": "提交注释"})
	}
	body, err := json.Marshal(map[string]any{"roots": items})
	if err != nil {
		t.Fatalf("构造提交体失败: %v", err)
	}
	return string(body)
}

// TestWorkspaceGetShape 读面字段与顺序（无 workspace.json 时回落启动快照；固定根三列取固定表）。
func TestWorkspaceGetShape(t *testing.T) {
	deps, root := newTestDeps(t)
	rec := do(t, deps, http.MethodGet, "/api/v1/workspace", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK    bool `json:"ok"`
		Roots []struct {
			ID        string `json:"id"`
			Path      string `json:"path"`
			Writable  bool   `json:"writable"`
			Note      string `json:"note"`
			FixedRoot bool   `json:"fixedRoot"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, rec.Body.String())
	}
	if !payload.OK || len(payload.Roots) != 2 {
		t.Fatalf("读面条数不符: %s", rec.Body.String())
	}
	if payload.Roots[0].ID != "workspace" || payload.Roots[0].Path != root {
		t.Fatalf("首根不符: %+v", payload.Roots[0])
	}
	if !payload.Roots[0].FixedRoot || payload.Roots[0].Note != "系统工作区（必选）" {
		t.Fatalf("固定根标记 / 注释不符: %+v", payload.Roots[0])
	}
	if payload.Roots[1].ID != "ccbp" || payload.Roots[1].Note != "CCBP 知识网络根" {
		t.Fatalf("第二根不符: %+v", payload.Roots[1])
	}
	keys := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(keys, ",") != "ok,roots" {
		t.Fatalf("顶层字段序不符: %v", keys)
	}
	if !strings.Contains(rec.Body.String(), `{"id":"workspace","path":`) ||
		!strings.Contains(rec.Body.String(), `,"writable":true,"note":"系统工作区（必选）","fixedRoot":true}`) {
		t.Fatalf("条目字段序不符: %s", rec.Body.String())
	}
}

// TestWorkspaceGetFollowsDisk 读面磁盘优先——含启动快照里不存在的根（不可达根也要能看见，否则保存即删）。
func TestWorkspaceGetFollowsDisk(t *testing.T) {
	deps, root := newTestDeps(t)
	wsPath := filepath.Join(root, "config", "workspace.json")
	body := `{"roots":[{"id":"workspace","path":"` + strings.ReplaceAll(root, `\`, "/") + `","writable":true},` +
		`{"id":"ghost","path":"Z:/not-mounted","writable":true,"note":"离线根"}],"inject":[]}`
	if err := os.WriteFile(wsPath, []byte(body), 0o644); err != nil {
		t.Fatalf("写 workspace.json 失败: %v", err)
	}
	rec := do(t, deps, http.MethodGet, "/api/v1/workspace", "")
	if !strings.Contains(rec.Body.String(), `"id":"ghost"`) {
		t.Fatalf("磁盘根未回显: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"id":"ccbp"`) {
		t.Fatalf("读面应取磁盘态而非启动快照: %s", rec.Body.String())
	}
}

// TestWorkspacePostWritesAndPreservesInject 写面落盘——固定根三列归一、正斜杠、inject 段保留、读面跟随。
func TestWorkspacePostWritesAndPreservesInject(t *testing.T) {
	deps, root := newTestDeps(t)
	wsPath := filepath.Join(root, "config", "workspace.json")
	old := `{"roots":[{"id":"workspace","path":"D:/old","writable":true}],"inject":[{"file":"ccbp:AGENTS.md","optional":false,"label":"前文面"}]}`
	if err := os.WriteFile(wsPath, []byte(old), 0o644); err != nil {
		t.Fatalf("写旧 workspace.json 失败: %v", err)
	}
	newPath := filepath.Join(root, "moved", "workspace")
	rec := do(t, deps, http.MethodPost, "/api/v1/workspace", workspaceRootsPayload(t, root, map[string]string{"workspace": newPath}))
	if got := rec.Body.String(); got != `{"ok":true,"roots":7}` {
		t.Fatalf("写面回执 = %s", got)
	}
	raw, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"inject":[{"file":"ccbp:AGENTS.md","optional":false,"label":"前文面"}]`) {
		t.Fatalf("inject 段未保留: %s", text)
	}
	if !strings.Contains(text, `"path":"`+strings.ReplaceAll(newPath, `\`, "/")+`"`) {
		t.Fatalf("新路径未落盘: %s", text)
	}
	if strings.Contains(text, `\`) {
		t.Fatalf("落盘路径应为正斜杠: %s", text)
	}
	// 固定根 note / writable 由系统定义覆盖（提交值「提交注释」不落盘）
	if strings.Contains(text, `"提交注释"`) {
		t.Fatalf("固定根注释应取系统定义: %s", text)
	}
	if !strings.Contains(text, `"id":"data","path":"`+strings.ReplaceAll(root, `\`, "/")+`","writable":false,"note":"CH4 应用数据（appdata）"`) {
		t.Fatalf("固定根三列不符: %s", text)
	}
	// 读面跟随（磁盘优先）
	get := do(t, deps, http.MethodGet, "/api/v1/workspace", "")
	var readBack struct {
		Roots []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &readBack); err != nil {
		t.Fatalf("读面解析失败: %v", err)
	}
	if len(readBack.Roots) == 0 || readBack.Roots[0].ID != "workspace" || readBack.Roots[0].Path != newPath {
		t.Fatalf("读面未跟随保存: %s", get.Body.String())
	}
}

// TestWorkspacePostCreatesMissingDir 保存面放宽——目录不存在按需创建（A306）。
func TestWorkspacePostCreatesMissingDir(t *testing.T) {
	deps, root := newTestDeps(t)
	missing := filepath.Join(root, "auto", "ccbp")
	rec := do(t, deps, http.MethodPost, "/api/v1/workspace", workspaceRootsPayload(t, root, map[string]string{"ccbp": missing}))
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("应允许创建不存在的目录: %s", rec.Body.String())
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("目录未创建: %v", err)
	}
}

// TestWorkspacePostRejects 校验面（空 roots / 缺固定根 / id 非法 / 重名 / 路径非法 / data 路径锁定 / 非 JSON）。
func TestWorkspacePostRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"非 JSON", `not-json`, "body 非 JSON"},
		{"空 roots", `{"roots":[]}`, "roots 为空（固定命名根不可移除）"},
		{"缺固定根", `{"roots":[{"id":"workspace","path":"` + strings.ReplaceAll(t.TempDir(), `\`, "/") + `"}]}`, "缺少系统根"},
		{"id 非法", `{"roots":[{"id":"a_b","path":"C:/x"}]}`, "id 非法（仅小写字母/数字）"},
		{"PS 保留名", `{"roots":[{"id":"env","path":"C:/x"}]}`, "与 PowerShell 内置驱动器同名"},
		{"重名", `{"roots":[{"id":"workspace","path":"C:/x"},{"id":"WORKSPACE","path":"C:/y"}]}`, "roots id 重复（大小写不敏感）"},
		{"path 空", `{"roots":[{"id":"workspace","path":"  "}]}`, "path 为空"},
		{"path 相对", `{"roots":[{"id":"workspace","path":"relative/dir"}]}`, "path 非绝对路径"},
	}
	for _, item := range cases {
		deps, _ := newTestDeps(t)
		rec := do(t, deps, http.MethodPost, "/api/v1/workspace", item.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: 状态码 %d（失败应为 200 + ok:false）", item.name, rec.Code)
		}
		var payload struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s: 解析回执失败: %v", item.name, err)
		}
		if payload.OK || !strings.Contains(payload.Error, item.want) {
			t.Fatalf("%s: 期望拒绝（%s），实得 ok=%v error=%q", item.name, item.want, payload.OK, payload.Error)
		}
	}
	// data 根路径锁定——提交别的路径即拒（不静默覆盖）
	deps, root := newTestDeps(t)
	rec := do(t, deps, http.MethodPost, "/api/v1/workspace", workspaceRootsPayload(t, root, map[string]string{"data": filepath.Join(root, "elsewhere")}))
	if !strings.Contains(rec.Body.String(), "系统根 data 的路径固定为") {
		t.Fatalf("data 路径锁定未生效: %s", rec.Body.String())
	}
}

// TestWorkspacePostRefusesCorruptExisting 旧档读不出 inject 时拒绝覆盖（不静默丢注入清单）。
func TestWorkspacePostRefusesCorruptExisting(t *testing.T) {
	deps, root := newTestDeps(t)
	if err := os.WriteFile(filepath.Join(root, "config", "workspace.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatalf("写坏档失败: %v", err)
	}
	rec := do(t, deps, http.MethodPost, "/api/v1/workspace", workspaceRootsPayload(t, root, nil))
	if !strings.Contains(rec.Body.String(), "注入清单读取失败（拒绝覆盖，避免丢 inject）") {
		t.Fatalf("应拒绝覆盖坏档: %s", rec.Body.String())
	}
	raw, _ := os.ReadFile(filepath.Join(root, "config", "workspace.json"))
	if string(raw) != `{broken` {
		t.Fatalf("坏档被改写: %s", string(raw))
	}
}

// TestWorkspacePostKeepsUnreachableRoot 存量容留——同 id 同 path 的不可达根允许原样提交（改根不卡死）。
func TestWorkspacePostKeepsUnreachableRoot(t *testing.T) {
	deps, root := newTestDeps(t)
	wsPath := filepath.Join(root, "config", "workspace.json")
	offline := "Z:/offline-root"
	old := `{"roots":[{"id":"workspace","path":"` + strings.ReplaceAll(root, `\`, "/") + `"},` +
		`{"id":"mau","path":"` + offline + `"}],"inject":[]}`
	if err := os.WriteFile(wsPath, []byte(old), 0o644); err != nil {
		t.Fatalf("写旧档失败: %v", err)
	}
	rec := do(t, deps, http.MethodPost, "/api/v1/workspace", workspaceRootsPayload(t, root, map[string]string{"mau": offline}))
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("存量不可达根应允许原样提交: %s", rec.Body.String())
	}
}
