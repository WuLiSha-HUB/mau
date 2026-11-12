package admin

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 测试用图片字节（内容不参与校验——只做寻址命名）。
var testPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02}

// uploadImage 发一次上传（显式 Content-Type）。
func uploadImage(t *testing.T, deps *Deps, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/chat-images", bytes.NewReader(data))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	deps.handleChatImagesUpload(rec, req)
	return rec
}

// TestChatImagesUploadAndDedup 内容寻址命名 + 同图去重。
func TestChatImagesUploadAndDedup(t *testing.T) {
	deps, root := newTestDeps(t)
	rec := uploadImage(t, deps, "image/png", testPNG)
	var got struct {
		OK    bool   `json:"ok"`
		Name  string `json:"name"`
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
		Dedup bool   `json:"dedup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("上传回执解析失败: %v（原始 %s）", err, rec.Body.String())
	}
	if !got.OK || got.Bytes != len(testPNG) || got.Dedup {
		t.Fatalf("首传回执异常: %s", rec.Body.String())
	}
	if !strings.HasSuffix(got.Name, ".png") || len(got.Name) != 20 {
		t.Fatalf("内容寻址名不符（16 位十六进制 + .png）: %s", got.Name)
	}
	if got.Path != filepath.Join(root, "chat-images", got.Name) {
		t.Fatalf("落点不符: %s", got.Path)
	}
	// 同图二传——去重命中，不重复落盘
	rec = uploadImage(t, deps, "image/png", testPNG)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("二传回执解析失败: %v", err)
	}
	if !got.Dedup {
		t.Fatalf("同图未去重: %s", rec.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "chat-images"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("落盘文件数不符: %v / %v", entries, err)
	}
}

// TestChatImagesRejects 类型白名单 / 空体拒绝（入口面零容忍）。
func TestChatImagesRejects(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := uploadImage(t, deps, "text/plain", []byte("x"))
	if got := rec.Body.String(); got != `{"ok":false,"error":"不支持的图片类型：text/plain"}` {
		t.Fatalf("类型拒绝回执 = %s", got)
	}
	rec = uploadImage(t, deps, "image/png", nil)
	if got := rec.Body.String(); got != `{"ok":false,"error":"空图片内容"}` {
		t.Fatalf("空体拒绝回执 = %s", got)
	}
}

// TestCacheImageByName 缓存目录裸名分支——命中即长缓存（内容寻址不可变）。
func TestCacheImageByName(t *testing.T) {
	deps, root := newTestDeps(t)
	if err := os.MkdirAll(filepath.Join(root, "chat-images"), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	name := "a1b2c3d4e5f60718.png"
	if err := os.WriteFile(filepath.Join(root, "chat-images", name), testPNG, 0o644); err != nil {
		t.Fatalf("写图失败: %v", err)
	}
	rec := do(t, deps, "GET", "/api/v1/cache-image/"+name, "")
	if rec.Code != 200 {
		t.Fatalf("取图状态码 = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %s", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("Cache-Control = %s", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), testPNG) {
		t.Fatal("取图字节不符")
	}
	// 未命中 / 穿越名判定
	rec = do(t, deps, "GET", "/api/v1/cache-image/none.png", "")
	if rec.Code != 404 {
		t.Fatalf("未命中状态码 = %d，预期 404", rec.Code)
	}
	for _, name := range []string{"..\\x.png", "a/b.png", "", "..png"} {
		if isSafeImageName(name) {
			t.Fatalf("%q 被判为安全文件名", name)
		}
	}
}

// TestCacheImageRootAddress 受控根寻址分支——根内图片可服务，非图片扩展名一律 404。
func TestCacheImageRootAddress(t *testing.T) {
	deps, root := newTestDeps(t)
	if err := os.MkdirAll(filepath.Join(root, "pic"), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pic", "shot.png"), testPNG, 0o644); err != nil {
		t.Fatalf("写图失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pic", "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("写文本失败: %v", err)
	}
	rec := do(t, deps, "GET", "/api/v1/cache-image/workspace:pic/shot.png", "")
	if rec.Code != 200 {
		t.Fatalf("根寻址取图状态码 = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Fatalf("根内产物应禁缓存，实为 %s", got)
	}
	rec = do(t, deps, "GET", "/api/v1/cache-image/workspace:pic/note.txt", "")
	if rec.Code != 404 {
		t.Fatalf("非图片扩展名状态码 = %d，预期 404", rec.Code)
	}
	rec = do(t, deps, "GET", "/api/v1/cache-image/nope:pic/shot.png", "")
	if rec.Code != 404 {
		t.Fatalf("未知根状态码 = %d，预期 404", rec.Code)
	}
}

// TestAudioGet 音频端点——未配置产物目录 404 / 裸名 .wav 命中 200。
func TestAudioGet(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := do(t, deps, "GET", "/api/v1/audio/summary_1_majordomo.wav", "")
	if rec.Code != 404 {
		t.Fatalf("未配置目录状态码 = %d，预期 404", rec.Code)
	}
	dir := t.TempDir()
	deps.Store.Set(kokoroOutDirKey, dir)
	if err := os.WriteFile(filepath.Join(dir, "summary_1_majordomo.wav"), []byte("RIFF"), 0o644); err != nil {
		t.Fatalf("写音频失败: %v", err)
	}
	rec = do(t, deps, "GET", "/api/v1/audio/summary_1_majordomo.wav", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("音频取回 = %d / %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	// 非 .wav 与未命中
	if rec = do(t, deps, "GET", "/api/v1/audio/summary_1_majordomo.mp3", ""); rec.Code != 404 {
		t.Fatalf("非 wav 状态码 = %d", rec.Code)
	}
	if rec = do(t, deps, "GET", "/api/v1/audio/none.wav", ""); rec.Code != 404 {
		t.Fatalf("未命中状态码 = %d", rec.Code)
	}
}

// TestOpenDataDir 打开数据目录——回执带数据根路径（OpenDir 替身不弹窗）。
func TestOpenDataDir(t *testing.T) {
	deps, root := newTestDeps(t)
	called := ""
	deps.OpenDir = func(path string) error {
		called = path
		return nil
	}
	rec := do(t, deps, "POST", "/api/v1/open-data-dir", "")
	var got struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("回执解析失败: %v（原始 %s）", err, rec.Body.String())
	}
	if !got.OK || got.Path != root {
		t.Fatalf("打开数据目录回执 = %s（预期 path=%s）", rec.Body.String(), root)
	}
	if called != root {
		t.Fatalf("打开目标 = %s，预期 %s", called, root)
	}
}
