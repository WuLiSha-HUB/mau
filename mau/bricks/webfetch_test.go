package bricks

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newTestWebFetch 构造一个指向临时目录的下载服务（不落盘任务表、不起通知泵）。
func newTestWebFetch(t *testing.T) (*WebFetchService, string) {
	t.Helper()
	dir := t.TempDir()
	svc := NewWebFetchService()
	svc.Configure("", dir, nil)
	return svc, dir
}

// waitWebFetch 轮询等待任务进入终态（最多 3 秒）。
func waitWebFetch(t *testing.T, svc *WebFetchService, id int64) *WebFetchTask {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		row := svc.findLocked(id)
		svc.mu.Unlock()
		if row != nil && isWebFetchTerminal(row.State) {
			return row
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务 #%d 未在 3 秒内终结", id)
	return nil
}

// TestWebFetchArgs 覆盖两件下载积木的参数面。
func TestWebFetchArgs(t *testing.T) {
	svc, _ := newTestWebFetch(t)

	if got, ok := WebFetch(nil, `{"url":"http://x/y"}`); ok || !strings.Contains(got, "ERR|DOWNLOAD_NO_SERVICE|宿主未注入 IWebFetchService") {
		t.Fatalf("服务未注入口径不符: ok=%v %s", ok, got)
	}
	if got, ok := WebFetchJobs(nil, `{"action":"list"}`); ok || !strings.Contains(got, "ERR|DOWNLOAD_NO_SERVICE|") {
		t.Fatalf("服务未注入口径不符: ok=%v %s", ok, got)
	}
	if got, ok := WebFetch(svc, `{}`); ok || !strings.Contains(got, "ERR|BAD_ARGS|缺参数 url") {
		t.Fatalf("缺 url 口径不符: ok=%v %s", ok, got)
	}
	if got, ok := WebFetch(svc, `{"url":"http://x/y","bogus":1}`); ok || !strings.Contains(got, "ERR|BAD_ARGS|未知参数: bogus") {
		t.Fatalf("未知键口径不符: ok=%v %s", ok, got)
	}
	// action 枚举非法属参数面失败；缺 action 走 list
	if got, ok := WebFetchJobs(svc, `{"action":"drop"}`); ok || !strings.Contains(got, "ERR|BAD_ARGS|action 非法值: drop（list|status|cancel）") {
		t.Fatalf("枚举口径不符: ok=%v %s", ok, got)
	}
}

// TestWebFetchServiceErrors 覆盖服务层登记期错误面。
func TestWebFetchServiceErrors(t *testing.T) {
	svc, _ := newTestWebFetch(t)
	if got := svc.Fetch("", "http://x/y", ""); got != "ERR|DOWNLOAD_ARGS|缺少来源猫（catKey）" {
		t.Fatalf("缺 catKey 口径不符: %s", got)
	}
	if got := svc.Fetch("cat1", "", ""); got != "ERR|DOWNLOAD_BAD_URL|缺少参数 url" {
		t.Fatalf("缺 url 口径不符: %s", got)
	}
	if got := svc.Fetch("cat1", "ftp://x/y", ""); got != "ERR|DOWNLOAD_BAD_URL|仅支持 http/https: ftp://x/y" {
		t.Fatalf("协议口径不符: %s", got)
	}
	if got := svc.Jobs("status", "99"); !strings.Contains(got, "ERR|DOWNLOAD_NO_TASK|任务不存在: #99") {
		t.Fatalf("任务不存在口径不符: %s", got)
	}
	if got := svc.Jobs("status", ""); !strings.Contains(got, "ERR|DOWNLOAD_ARGS|缺少或非法参数 id") {
		t.Fatalf("缺 id 口径不符: %s", got)
	}
	if got := svc.Jobs("list", ""); got != "（无下载任务）" {
		t.Fatalf("空表口径不符: %s", got)
	}
}

// TestWebFetchDownload 覆盖登记 → 下载 → 落盘 → 终态 → 清单 / 详情。
func TestWebFetchDownload(t *testing.T) {
	payload := strings.Repeat("payload-", 2048)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	svc, dir := newTestWebFetch(t)
	got, ok := WebFetch(svc, `{"url":"`+server.URL+`/file.bin","catId":"cat-x"}`)
	if !ok {
		t.Fatalf("登记期望成功，实际: %s", got)
	}
	if !strings.Contains(got, `{"ok":true,"tool":"web-fetch","target":"`+server.URL+`/file.bin","chars":`) {
		t.Fatalf("A214 头不符: %s", got)
	}
	if !strings.Contains(got, "\n任务 #1 已登记 | file.bin | 下载中（完成或失败时系统提示）→ ") {
		t.Fatalf("登记回执不符: %s", got)
	}

	row := waitWebFetch(t, svc, 1)
	if row.State != "Completed" {
		t.Fatalf("期望 Completed，实际 %s（%s）", row.State, row.Error)
	}
	written, err := os.ReadFile(filepath.Join(dir, "file.bin"))
	if err != nil || string(written) != payload {
		t.Fatalf("产物落盘不符: err=%v 长度=%d", err, len(written))
	}
	if _, err := os.Stat(filepath.Join(dir, "file.bin.part")); !os.IsNotExist(err) {
		t.Fatalf(".part 未清理: %v", err)
	}

	listed, ok := WebFetchJobs(svc, `{"action":"list"}`)
	if !ok || !strings.Contains(listed, " | 16.0 KB / 16.0 KB（100%） | cat=cat-x") {
		t.Fatalf("清单行不符: ok=%v %s", ok, listed)
	}
	detail, ok := WebFetchJobs(svc, `{"action":"status","id":"1"}`)
	if !ok || !strings.Contains(detail, "源: "+server.URL+"/file.bin") || !strings.Contains(detail, "HTTP: 200") {
		t.Fatalf("详情不符: ok=%v %s", ok, detail)
	}
}

// TestWebFetchNameConflict 覆盖同名冲突与同源在途冲突。
func TestWebFetchNameConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer server.Close()

	svc, dir := newTestWebFetch(t)
	if err := os.WriteFile(filepath.Join(dir, "dup.bin"), []byte("old"), 0o644); err != nil {
		t.Fatalf("预置文件失败: %v", err)
	}
	if got := svc.Fetch("cat1", server.URL+"/dup.bin", "dup.bin"); !strings.Contains(got, "ERR|DOWNLOAD_NAME_CONFLICT|目标同名文件已存在: dup.bin（改名或清理后重试）") {
		t.Fatalf("同名冲突口径不符: %s", got)
	}
	first := svc.Fetch("cat1", server.URL+"/slow.bin", "slow.bin")
	if !strings.HasPrefix(first, "任务 #") {
		t.Fatalf("首次登记失败: %s", first)
	}
	second := svc.Fetch("cat1", server.URL+"/slow.bin", "slow.bin")
	if !strings.Contains(second, "ERR|DOWNLOAD_EXISTS|同源同目标任务已在进行: #") {
		t.Fatalf("在途冲突口径不符: %s", second)
	}
	waitWebFetch(t, svc, 1)
}

// TestWebFetchCancel 覆盖取消路径与终态不可取消。
func TestWebFetchCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", 10)))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	svc, _ := newTestWebFetch(t)
	if got := svc.Fetch("cat1", server.URL+"/slow.bin", "slow.bin"); !strings.HasPrefix(got, "任务 #") {
		t.Fatalf("登记失败: %s", got)
	}
	time.Sleep(30 * time.Millisecond)
	canceled := svc.Jobs("cancel", "1")
	if !strings.Contains(canceled, "已取消 #1 | slow.bin") {
		t.Fatalf("取消回执不符: %s", canceled)
	}
	row := waitWebFetch(t, svc, 1)
	if row.State != "Canceled" {
		t.Fatalf("期望 Canceled，实际 %s", row.State)
	}
	if again := svc.Jobs("cancel", "1"); !strings.Contains(again, "ERR|DOWNLOAD_NO_TASK|任务已终结，不可取消: #1（Canceled）") {
		t.Fatalf("终态取消口径不符: %s", again)
	}
}

// TestDeriveFileName 覆盖文件名派生与非法字符清理。
func TestDeriveFileName(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a/b/file.zip":     "file.zip",
		"https://example.com/a/b/my%20doc.pdf": "my doc.pdf",
		"https://example.com/":                 "download",
		"https://example.com/a/bad%3Aname.txt": "badname.txt",
	}
	for input, want := range cases {
		if got := deriveFileName(input); got != want {
			t.Fatalf("deriveFileName(%q) 期望 %q，实际 %q", input, want, got)
		}
	}
}

// TestFormatBytesDuration 覆盖规模 / 时长渲染。
func TestFormatBytesDuration(t *testing.T) {
	byteCases := map[int64]string{
		0:           "0 B",
		1023:        "1023 B",
		1024:        "1.0 KB",
		1048576:     "1.0 MB",
		1073741824:  "1.00 GB",
		19 * 1024:   "19.0 KB",
		1536 * 1024: "1.5 MB",
	}
	for input, want := range byteCases {
		if got := formatBytes(input); got != want {
			t.Fatalf("formatBytes(%d) 期望 %q，实际 %q", input, want, got)
		}
	}
	durationCases := map[int64]string{500: "500ms", 1500: "1.5s", 90000: "1.5min"}
	for input, want := range durationCases {
		if got := formatDuration(input); got != want {
			t.Fatalf("formatDuration(%d) 期望 %q，实际 %q", input, want, got)
		}
	}
}
