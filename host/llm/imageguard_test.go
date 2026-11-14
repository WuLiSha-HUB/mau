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

// TestBuildChatRequestBodyBudget_DropOldest 预算超限——摘最旧的图，保留最新的。
func TestBuildChatRequestBodyBudget_DropOldest(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeSizedPNG(t, dir, "old.png", 900, 600)
	newPath := writeSizedPNG(t, dir, "new.png", 900, 600)
	messages := []session.Message{
		{Role: session.RoleUser, Content: "旧图", ImagesJSON: "[" + session.JSONString(oldPath) + "]"},
		{Role: session.RoleAssistant, Content: "看到了"},
		{Role: session.RoleUser, Content: "新图", ImagesJSON: "[" + session.JSONString(newPath) + "]"},
	}
	body, omitted := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m", MaxTokens: 8}, "")
	if omitted == 0 {
		t.Fatalf("应触发预算摘图（两张 900x600 噪点 PNG 估算已超 3 MB）")
	}
	if len(body) > maxRequestBodyBytes {
		t.Fatalf("请求体仍超上限：%d", len(body))
	}
	// 最旧的先摘：新图仍在（base64 片段），旧图只剩占位说明
	if !strings.Contains(body, "旧图") || !strings.Contains(body, imageDropNoticePrefix) {
		t.Fatalf("旧图应被摘成占位: %s", truncateVisionText(body, 400))
	}
	if count := strings.Count(body, `"type":"image_url"`); count != 1 {
		t.Fatalf("应只保留新图（1 张），实际 %d 张", count)
	}
}

// TestBuildChatRequestBodyBudget_KeepNewestInSameMessage 同一条消息多张图——按张摘，保留最新的。
func TestBuildChatRequestBodyBudget_KeepNewestInSameMessage(t *testing.T) {
	dir := t.TempDir()
	first := writeSizedPNG(t, dir, "1.png", 900, 600)
	second := writeSizedPNG(t, dir, "2.png", 900, 600)
	third := writeSizedPNG(t, dir, "3.png", 900, 600)
	imagesJSON := "[" + session.JSONString(first) + "," + session.JSONString(second) + "," + session.JSONString(third) + "]"
	messages := []session.Message{{Role: session.RoleUser, Content: "三张图", ImagesJSON: imagesJSON}}
	body, omitted := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m", MaxTokens: 8}, "")
	if omitted == 0 {
		t.Fatalf("应触发摘图（三张 900x600 噪点 PNG 估算已超 3 MB）")
	}
	if len(body) > maxRequestBodyBytes {
		t.Fatalf("请求体仍超上限：%d", len(body))
	}
	// 按张摘：至少保留最新一张，其余在 text 里计数说明
	if count := strings.Count(body, `"type":"image_url"`); count == 0 {
		t.Fatalf("同一消息不应全摘——应保留最新一张")
	}
	if !strings.Contains(body, imageDropNoticePrefix) {
		t.Fatalf("部分省略应有计数占位: %s", truncateVisionText(body, 300))
	}
}

// TestBuildChatRequestBodyBudget_ChannelDown 通道降级——全摘（含最新）。
func TestBuildChatRequestBodyBudget_ChannelDown(t *testing.T) {
	dir := t.TempDir()
	path := writeSizedPNG(t, dir, "a.png", 20, 20)
	messages := []session.Message{{Role: session.RoleUser, Content: "图", ImagesJSON: "[" + session.JSONString(path) + "]"}}
	ImageChannelState.Mark("ERR|TRANSPORT|测试", 0)
	defer ImageChannelState.Reset()
	body, omitted := BuildChatRequestBodyBudget(messages, nil, Params{Model: "m"}, "")
	if omitted != 1 {
		t.Fatalf("降级期应全摘：omitted=%d", omitted)
	}
	if !strings.Contains(body, imageDropNoticePrefix) {
		t.Fatalf("应留占位说明: %s", truncateVisionText(body, 300))
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
