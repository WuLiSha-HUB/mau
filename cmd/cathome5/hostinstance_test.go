package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cathome5/host/admin"
	"cathome5/host/config"
	"cathome5/host/group"
	"cathome5/host/session"
)

// freeLoopbackPort 取一个空闲回环端口（试绑后释放——单测内竞态窗口可接受）。
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败：%v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// writeHTMLRoot 建最小静态页根（chat.html / group.html 带 __V__ 占位）。
func writeHTMLRoot(t *testing.T, dir string) string {
	t.Helper()
	root := filepath.Join(dir, "html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{
		"chat.html":  "<title>chat __V__</title>",
		"group.html": "<title>group __V__</title>",
	}
	for name, body := range pages {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// newHostTestSession 构造最小会话（前文 / 视图落临时目录）。
func newHostTestSession(t *testing.T, dir, catID string) *session.Session {
	t.Helper()
	ctx := session.NewContext()
	store := session.NewSessionStore(filepath.Join(dir, "sessions", catID, catID+".jsonl"))
	views := session.NewViewStore(filepath.Join(dir, "sessions", catID, catID+".view.jsonl"))
	return session.NewSession(catID, ctx, store, views)
}

// httpGetBody 取 HTTP 正文（非 2xx 也返回状态码与正文——判据看内容）。
func httpGetBody(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s 失败：%v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("读取 %s 正文失败：%v", url, err)
	}
	return resp.StatusCode, string(body)
}

// readSSEFirstFrame 读 SSE 首帧（带超时——流不会自然 EOF）。
func readSSEFirstFrame(t *testing.T, url string) string {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s 失败：%v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	ch := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		ch <- string(buf[:n])
	}()
	select {
	case text := <-ch:
		return text
	case <-time.After(3 * time.Second):
		t.Fatalf("SSE 首帧超时（%s）", url)
		return ""
	}
}

// TestStartCatHostServesChatPage 每猫端口——chat.html + 视图首帧 + 指令面；cats 列表不注册（C# 每猫 host 同构）。
func TestStartCatHostServesChatPage(t *testing.T) {
	dir := t.TempDir()
	htmlRoot := writeHTMLRoot(t, dir)
	sess := newHostTestSession(t, dir, "cat-1")
	inst := &catInstance{id: "cat-1", displayName: "小测", session: sess}
	deps := &hostInstanceDeps{
		DataRoot:  dir,
		ConfigDir: filepath.Join(dir, "config"),
		HTMLRoot:  htmlRoot,
		Version:   "9.9.9",
		Admin:     &admin.Deps{DataRoot: dir, CatID: "majordomo"},
		DispatchCat: func(catID string, target *session.Session, text string) bool {
			if strings.HasPrefix(text, "Chat ") {
				return target.PostUser(strings.TrimSpace(strings.TrimPrefix(text, "Chat ")), "user", "", "")
			}
			return false
		},
	}
	port := freeLoopbackPort(t)
	host, err := startCatHost(inst, deps, port)
	if err != nil {
		t.Fatalf("起猫端口失败：%v", err)
	}
	defer host.stop()
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	// `/` = chat.html + __V__ 替换
	status, body := httpGetBody(t, base+"/")
	if status != http.StatusOK || !strings.Contains(body, "<title>chat 9.9.9</title>") {
		t.Fatalf("chat.html 未生效：status=%d body=%q", status, body)
	}
	// 每猫实例不注册管理列表（C# CatsBuilder=null 同口径）
	if status, _ := httpGetBody(t, base+"/api/v1/cats"); status != http.StatusNotFound {
		t.Fatalf("每猫实例不应注册 /api/v1/cats：status=%d", status)
	}
	// 对话页条件面——前文视图已挂载
	if status, _ := httpGetBody(t, base+"/api/v1/context?max=5"); status != http.StatusOK {
		t.Fatalf("context 未挂载：status=%d", status)
	}
	// 指令面——Chat 落本猫会话
	resp, err := http.Post(base+"/api/v1/command", "application/json", strings.NewReader(`{"text":"Chat 你好"}`))
	if err != nil {
		t.Fatalf("投递失败：%v", err)
	}
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if !strings.Contains(string(payload), `"ok":true`) {
		t.Fatalf("指令未受理：%s", string(payload))
	}
	if got := valueInt(sess.StateEntry()["pending"]); got != 1 {
		t.Fatalf("会话待处理队列应 1 条：%d", got)
	}
	// 视图首帧（chat 页唯一数据通道）——v:2 全量 + state 段
	frame := readSSEFirstFrame(t, base+"/api/v1/stream?topics=view")
	if !strings.Contains(frame, "event: view") || !strings.Contains(frame, `"v":2`) {
		t.Fatalf("view 首帧不符：%q", frame)
	}
}

// TestStartGroupHostServesGroupPage 组轻宿主——group.html + 本组四条运行时路由（info / messages / clear）。
func TestStartGroupHostServesGroupPage(t *testing.T) {
	dir := t.TempDir()
	htmlRoot := writeHTMLRoot(t, dir)
	store := group.New(filepath.Join(dir, "group"), nil)
	store.Load()
	if result := store.CreateOrUpdate("qa", []string{"majordomo"}, "", nil, nil, 0); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("建组失败：%q", result)
	}
	if result := store.StartGroup("qa"); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("组启动失败：%q", result)
	}
	deps := &hostInstanceDeps{
		DataRoot: dir,
		HTMLRoot: htmlRoot,
		Version:  "9.9.9",
		Admin:    &admin.Deps{DataRoot: dir, Groups: store},
	}
	port := freeLoopbackPort(t)
	host, err := startGroupHost("qa", deps, port)
	if err != nil {
		t.Fatalf("起组端口失败：%v", err)
	}
	defer host.stop()
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	// `/` = group.html
	status, body := httpGetBody(t, base+"/")
	if status != http.StatusOK || !strings.Contains(body, "<title>group 9.9.9</title>") {
		t.Fatalf("group.html 未生效：status=%d body=%q", status, body)
	}
	// 组信息（端口即组身份——不接受跨组参数）
	status, info := httpGetBody(t, base+"/api/v1/group/info")
	if status != http.StatusOK || !strings.Contains(info, `"ok":true`) || !strings.Contains(info, `"name":"qa"`) {
		t.Fatalf("group/info 不符：status=%d body=%q", status, info)
	}
	if !strings.Contains(info, `"userName":"默认用户"`) {
		t.Fatalf("group/info 应带使用者显示名：%q", info)
	}
	// 投递（body 不含 group——组名由端口绑定）
	resp, err := http.Post(base+"/api/v1/group/messages", "application/json", strings.NewReader(`{"from":"majordomo","type":"note","text":"组页首条"}`))
	if err != nil {
		t.Fatalf("组投递失败：%v", err)
	}
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if !strings.Contains(string(payload), `"ok":true`) || !strings.Contains(string(payload), `seq=1`) {
		t.Fatalf("组投递回执不符：%s", string(payload))
	}
	// 消息流
	status, messages := httpGetBody(t, base+"/api/v1/group/messages?limit=10")
	if status != http.StatusOK || !strings.Contains(messages, "组页首条") || !strings.Contains(messages, `"lastSeq":1`) {
		t.Fatalf("group/messages 不符：status=%d body=%q", status, messages)
	}
	// 清空（原地归档）
	resp, err = http.Post(base+"/api/v1/group/clear", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	payload, _ = io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if !strings.Contains(string(payload), `"ok":true`) {
		t.Fatalf("清空回执不符：%s", string(payload))
	}
}

// TestCatHostManagerAllocate 猫端口族分配——跳过已占端口 / 段内全占返回 -1。
func TestCatHostManagerAllocate(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占位监听失败：%v", err)
	}
	defer func() { _ = holder.Close() }()
	start := holder.Addr().(*net.TCPAddr).Port
	deps := &hostInstanceDeps{Band: config.PortBand{Area: "test", MainPort: start + 10, FamilyFrom: start, FamilyTo: start + 3}}
	manager := newCatHostManager(deps)
	// start 被外部占用 → 分配应跳过它
	if got := manager.allocateLocked(); got == start || got <= 0 {
		t.Fatalf("端口分配未跳过占用端口：%d", got)
	}
	// 段内只有一个端口且被占 → 全占
	single := &hostInstanceDeps{Band: config.PortBand{Area: "test", FamilyFrom: start, FamilyTo: start}}
	if got := newCatHostManager(single).allocateLocked(); got != -1 {
		t.Fatalf("单端口段被占应返回 -1：%d", got)
	}
}

// TestGroupHostManagerAllocate 组端口段——跳过表内已占 / 9000-9099 全占返回 -1。
func TestGroupHostManagerAllocate(t *testing.T) {
	manager := newGroupHostManager(&hostInstanceDeps{}, nil)
	if got := manager.allocateLocked(); got < groupPortFrom || got > groupPortTo {
		t.Fatalf("组端口分配越界：%d", got)
	}
	manager.ports["taken"] = groupPortFrom
	if got := manager.allocateLocked(); got == groupPortFrom {
		t.Fatalf("应跳过表内已占端口：%d", got)
	}
	full := newGroupHostManager(&hostInstanceDeps{}, nil)
	for port := groupPortFrom; port <= groupPortTo; port++ {
		full.ports[strconv.Itoa(port)] = port
	}
	if got := full.allocateLocked(); got != -1 {
		t.Fatalf("端口段全占应返回 -1：%d", got)
	}
}

// TestCatRegistryStartHostWiring 端口面钩子——cat.start 回真端口 / 运行态落盘 / cat.stop 停实例。
func TestCatRegistryStartHostWiring(t *testing.T) {
	reg, dir := newTestCatRegistry(t)
	inst, errText := reg.createCat("带端口")
	if errText != "" {
		t.Fatalf("建猫失败：%q", errText)
	}
	started := 0
	stopped := []string{}
	reg.startHost = func(target *catInstance, preferred int) (int, error) {
		started++
		return 8123, nil
	}
	reg.stopHost = func(catKey string) { stopped = append(stopped, catKey) }
	got := reg.startCat(inst.id)
	if !strings.Contains(got, "http://127.0.0.1:8123") {
		t.Fatalf("cat.start 应回真端口：%q", got)
	}
	if started != 1 {
		t.Fatalf("startHost 应调用一次：%d", started)
	}
	entry := loadCatRuntime(dir, nil)[inst.id]
	if !entry.Running || entry.Port != 8123 {
		t.Fatalf("运行态应带端口：%+v", entry)
	}
	// 重复 cat.start——已运行且带端口
	if got := reg.startCat(inst.id); !strings.Contains(got, "已在运行: 带端口 :8123") {
		t.Fatalf("重复 start 文案不符：%q", got)
	}
	if got := reg.stopCat(inst.id); !strings.Contains(got, "已停止") {
		t.Fatalf("stop 回执不符：%q", got)
	}
	if len(stopped) != 1 || stopped[0] != inst.id {
		t.Fatalf("stopHost 应按猫 key 调用一次：%v", stopped)
	}
	if entry := loadCatRuntime(dir, nil)[inst.id]; entry.Running || entry.Port != 0 {
		t.Fatalf("停止后运行态应归零：%+v", entry)
	}
}

// TestCatRegistryStartHostsOnBoot 启动扫描端口面——running 猫按上次端口拉起，失败按静默态保留条目。
func TestCatRegistryStartHostsOnBoot(t *testing.T) {
	reg, dir := newTestCatRegistry(t)
	cfg := &config.CatCfg{ID: "100000000000000009", DisplayName: "运行猫"}
	if err := config.SaveCatCfg(filepath.Join(dir, "sessions", cfg.ID, "cat.cfg"), cfg); err != nil {
		t.Fatal(err)
	}
	saveCatRuntime(dir, map[string]catRuntimeEntry{cfg.ID: {Running: true, Port: 8765}}, nil)
	reg.loadOnBoot()
	preferred := -1
	reg.startHost = func(target *catInstance, want int) (int, error) {
		preferred = want
		return want, nil
	}
	reg.startHostsOnBoot()
	if preferred != 8765 {
		t.Fatalf("启动扫描应沿用上次端口：%d", preferred)
	}
	inst := reg.findCat(cfg.ID)
	if inst == nil || inst.port != 8765 || !inst.running {
		t.Fatalf("端口面未写入注册表：%+v", inst)
	}
}
