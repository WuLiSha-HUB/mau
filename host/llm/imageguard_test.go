package llm

import (
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/session"
)

// writeSizedPNG 写一张指定尺寸的随机噪点 PNG——字节大、难压缩（降分辨率用例）。
func writeSizedPNG(t *testing.T, dir, name string, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	random := rand.New(rand.NewSource(42))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(random.Intn(256)), G: uint8(random.Intn(256)), B: uint8(random.Intn(256)), A: 255})
		}
	}
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBuildChatRequestBodyBudget_ShrinkInsteadOfDrop 超预算——优先降分辨率保住图片，不摘图。
func TestBuildChatRequestBodyBudget_ShrinkInsteadOfDrop(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeSizedPNG(t, dir, "old.png", 900, 600)
	newPath := writeSizedPNG(t, dir, "new.png", 900, 600)
	messages := []session.Message{
		{Role: session.RoleUser, Content: "旧图", ImagesJSON: "[" + session.JSONString(oldPath) + "]"},
		{Role: session.RoleAssistant, Content: "看到了"},
		{Role: session.RoleUser, Content: "新图", ImagesJSON: "[" + session.JSONString(newPath) + "]"},
	}
	body, omitted, shrunk := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m", MaxTokens: 8}, "")
	if omitted != 0 {
		t.Fatalf("应优先降分辨率而不是摘图：omitted=%d", omitted)
	}
	if shrunk != 2 {
		t.Fatalf("两张图都应降分辨率：shrunk=%d", shrunk)
	}
	if len(body) > maxRequestBodyBytes {
		t.Fatalf("请求体仍超上限：%d", len(body))
	}
	if count := strings.Count(body, `"type":"image_url"`); count != 2 {
		t.Fatalf("两张图都应保留，实际 %d 张", count)
	}
	if !strings.Contains(body, imageShrinkNoticePrefix) {
		t.Fatalf("应有降分辨率提示（模型可见）: %s", truncateVisionText(body, 400))
	}
}

// TestBuildChatRequestBodyBudget_KeepAllInSameMessage 同一条消息多张图——全部降级保留，不摘。
func TestBuildChatRequestBodyBudget_KeepAllInSameMessage(t *testing.T) {
	dir := t.TempDir()
	first := writeSizedPNG(t, dir, "1.png", 900, 600)
	second := writeSizedPNG(t, dir, "2.png", 900, 600)
	third := writeSizedPNG(t, dir, "3.png", 900, 600)
	imagesJSON := "[" + session.JSONString(first) + "," + session.JSONString(second) + "," + session.JSONString(third) + "]"
	messages := []session.Message{{Role: session.RoleUser, Content: "三张图", ImagesJSON: imagesJSON}}
	body, omitted, shrunk := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m", MaxTokens: 8}, "")
	if omitted != 0 || shrunk != 3 {
		t.Fatalf("同消息三张图应全部降级保留：omitted=%d shrunk=%d", omitted, shrunk)
	}
	if count := strings.Count(body, `"type":"image_url"`); count != 3 {
		t.Fatalf("三张图都应保留，实际 %d 张", count)
	}
	if !strings.Contains(body, imageShrinkNoticePrefix) {
		t.Fatalf("应有降分辨率提示: %s", truncateVisionText(body, 300))
	}
}

// TestBuildChatRequestBodyBudget_DropLastResort 降到底仍超（纯文本自身超预算）——摘图兜底仍执行。
func TestBuildChatRequestBodyBudget_DropLastResort(t *testing.T) {
	dir := t.TempDir()
	path := writeSizedPNG(t, dir, "a.png", 900, 600)
	huge := strings.Repeat("x", maxRequestBodyBytes+64*1024)
	messages := []session.Message{{Role: session.RoleUser, Content: huge, ImagesJSON: "[" + session.JSONString(path) + "]"}}
	body, omitted, _ := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m", MaxTokens: 8}, "")
	if omitted != 1 {
		t.Fatalf("纯文本自身超预算时降级救不了，应摘图：omitted=%d", omitted)
	}
	if count := strings.Count(body, `"type":"image_url"`); count != 0 {
		t.Fatalf("应全摘，实际 %d 张", count)
	}
}

// TestBuildChatRequestBodyBudget_ChannelDown 通道降级——全摘（含最新）。
func TestBuildChatRequestBodyBudget_ChannelDown(t *testing.T) {
	dir := t.TempDir()
	path := writeSizedPNG(t, dir, "a.png", 20, 20)
	messages := []session.Message{{Role: session.RoleUser, Content: "图", ImagesJSON: "[" + session.JSONString(path) + "]"}}
	ImageChannelState.Mark("ERR|TRANSPORT|测试", 0)
	defer ImageChannelState.Reset()
	body, omitted, _ := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m"}, "")
	if omitted != 1 {
		t.Fatalf("降级期应全摘：omitted=%d", omitted)
	}
	if !strings.Contains(body, imageDropNoticePrefix) {
		t.Fatalf("应留占位说明: %s", truncateVisionText(body, 300))
	}
}

// TestShrinkImageAreaRatio 预算降级——按像素面积比缩到约 1/10（线性 1/√10）。
func TestShrinkImageAreaRatio(t *testing.T) {
	dir := t.TempDir()
	path := writeSizedPNG(t, dir, "big.png", 900, 600)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	shrunk, mime, note, changed := shrinkImageAreaRatio(raw, "image/png", 0.1)
	if !changed || mime != "image/jpeg" {
		t.Fatalf("应降级为 JPEG：changed=%v mime=%s", changed, mime)
	}
	if len(shrunk) >= len(raw) {
		t.Fatalf("降级后字节应变小：%d → %d", len(raw), len(shrunk))
	}
	if !strings.Contains(note, "→") {
		t.Fatalf("说明应含尺寸变化: %s", note)
	}
	rect := decodeImageConfig(shrunk)
	if rect.Dx() < 250 || rect.Dx() > 320 || rect.Dy() < 160 || rect.Dy() > 220 {
		t.Fatalf("尺寸应约 284x189，实际 %dx%d", rect.Dx(), rect.Dy())
	}
	// 已到最小尺寸——原样不动
	tinyPath := writeSizedPNG(t, dir, "tiny.png", 1, 1)
	tinyRaw, err := os.ReadFile(tinyPath)
	if err != nil {
		t.Fatal(err)
	}
	kept, _, _, keptChanged := shrinkImageAreaRatio(tinyRaw, "image/png", 0.1)
	if keptChanged || len(kept) != len(tinyRaw) {
		t.Fatalf("1x1 图不应改动：changed=%v %d → %d", keptChanged, len(tinyRaw), len(kept))
	}
}

// TestResolveImagePayloadForBudget 预算降级展开——本地图片出 JPEG data URL + 说明；公网 URL 直传。
func TestResolveImagePayloadForBudget(t *testing.T) {
	dir := t.TempDir()
	path := writeSizedPNG(t, dir, "shot.png", 900, 600)
	url, errText, note, changed := resolveImagePayloadForBudget(path, 0.1)
	if errText != "" || !changed {
		t.Fatalf("应降级成功：err=%s changed=%v", errText, changed)
	}
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("应为 JPEG data URL：%s", truncateVisionText(url, 60))
	}
	if !strings.Contains(note, "shot.png") || !strings.Contains(note, "→") {
		t.Fatalf("说明应含文件名与尺寸变化: %s", note)
	}
	// 缓存命中——同图同档重跑结果一致
	cachedURL, errText2, cachedNote, cachedChanged := resolveImagePayloadForBudget(path, 0.1)
	if errText2 != "" || cachedURL != url || cachedNote != note || cachedChanged != changed {
		t.Fatalf("缓存命中应返回同一结果")
	}
	// 公网 URL 直传（不降级）
	direct, errText3, _, changed3 := resolveImagePayloadForBudget("https://example.com/a.png", 0.1)
	if errText3 != "" || changed3 || direct != "https://example.com/a.png" {
		t.Fatalf("公网 URL 应直传：%s / %s", direct, errText3)
	}
}

// TestShrinkVisionImage 降分辨率——大图缩到软上限内，返回 JPEG。
func TestShrinkVisionImage(t *testing.T) {
	dir := t.TempDir()
	path := writeSizedPNG(t, dir, "big.png", 1000, 800)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= visionSoftImageBytes {
		t.Fatalf("测试图未超软上限（%d 字节）——用例失效", len(raw))
	}
	shrunk, mime, note := shrinkVisionImage(raw, "image/png")
	if mime != "image/jpeg" {
		t.Fatalf("应重编码为 JPEG: %s", mime)
	}
	if len(shrunk) > visionSoftImageBytes {
		t.Fatalf("缩放后仍超软上限：%d", len(shrunk))
	}
	if note == "" || !strings.Contains(note, "降分辨率") {
		t.Fatalf("说明缺失: %s", note)
	}
	// 小图原样返回（零回归）
	small := writeSizedPNG(t, dir, "small.png", 64, 64)
	smallRaw, _ := os.ReadFile(small)
	kept, keptMIME, keptNote := shrinkVisionImage(smallRaw, "image/png")
	if len(kept) != len(smallRaw) || keptMIME != "image/png" || keptNote != "" {
		t.Fatalf("小图不应改动：%d/%s/%s", len(kept), keptMIME, keptNote)
	}
}

// TestVisionCooldownBlocked 熔断——不可用期直接绕行回执；配置变更即复位。
func TestVisionCooldownBlocked(t *testing.T) {
	svc, logs := newTestVisionService(t, "http://127.0.0.1:1/v1", "m", "k", nil)
	svc.markDown("ERR|API_NOT_CONFIGURED|视觉 API 未配置（用例）")
	blocked := svc.Analyze("D:/x.png", "")
	if !strings.HasPrefix(blocked, "ERR|VISION_UNAVAILABLE|") || !strings.Contains(blocked, "请绕行") {
		t.Fatalf("熔断期应返回绕行回执: %s", blocked)
	}
	if len(*logs) == 0 || !strings.Contains((*logs)[0], "熔断") {
		t.Fatalf("熔断应出声: %v", *logs)
	}
	// 配置指纹变化 → 复位放行（返回真实配置错误，而不是熔断回执）
	svc.store.Set("vision.api_config_id", "not-a-guid")
	afterConfigChange := svc.Analyze("D:/x.png", "")
	if !strings.HasPrefix(afterConfigChange, "ERR|API_NOT_CONFIGURED|") {
		t.Fatalf("配置变更后应复位放行: %s", afterConfigChange)
	}
	// 本地图片类错误不熔断（先把配置恢复为有效值）
	svc.store.Set("vision.api_config_id", visionTestID)
	svc.clearDown()
	if got := svc.Analyze("D:/no/such.png", ""); !strings.HasPrefix(got, "ERR|IMAGE_NOT_FOUND|") {
		t.Fatalf("本地图片错误: %s", got)
	}
	if blocked, _ := svc.cooldownBlocked(); blocked {
		t.Fatalf("IMAGE_* 类错误不应熔断")
	}
}
