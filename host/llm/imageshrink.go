package llm

// 视觉请求的图片降分辨率保护——image-analyze 单图载荷超过软上限时先缩再发。
//
// 为什么：本地网关 WorkBuddyShell（127.0.0.1:18100）对请求体有硬上限（实测 3 MB 通过 /
// 4 MB 直接断连）。识图请求常常是「截图 2000×1300 / 插画原图」这类 1 MB+ 的 PNG，
// 直接内联很容易顶线；与其让整条请求失败，不如按软上限自动缩图重编码。
//
// 边界：只作用于 image-analyze 的本地图片载荷；image-inject（原图直插主干）不走这里，
// 由请求体预算（request.go）统一兜底。解码失败 / 非支持格式 → 原字节发送并出声，不阻断。

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
)

// 视觉图片软上限——单图超过即降分辨率重编码（本地网关 3 MB 通过 / 4 MB 断连，留足余量）。
const visionSoftImageBytes = 1 * 1024 * 1024

// 视觉图片长边软上限——即使字节不大，超大画布也先缩（上游视觉模型通常 2048 内）。
const visionMaxImageEdge = 4096

// 视觉图片长边目标——缩放后的最长边（2048 是主流视觉模型的有效输入档）。
const visionTargetImageEdge = 2048

// visionJPEGQualities 降级质量序列——从高到低试，命中软上限即停。
var visionJPEGQualities = []int{85, 70, 55, 40}

// visionShrinkEdges 降级长边序列——质量降到底仍超上限时继续缩。
var visionShrinkEdges = []int{visionTargetImageEdge, 1600, 1280, 1024}

// decodeImageAny 按 MIME / 魔数解码图片——jpeg / png / gif（标准库三件）。
func decodeImageAny(raw []byte, mime string) (image.Image, bool) {
	reader := bytes.NewReader(raw)
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		if img, err := jpeg.Decode(reader); err == nil {
			return img, true
		}
	case "image/png":
		if img, err := png.Decode(reader); err == nil {
			return img, true
		}
	case "image/gif":
		if img, err := gif.Decode(reader); err == nil {
			return img, true
		}
	}
	// MIME 不可信 / 缺失——按魔数逐个试（顺序按常见度）
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err == nil {
		return img, true
	}
	if img, err := png.Decode(bytes.NewReader(raw)); err == nil {
		return img, true
	}
	if img, err := gif.Decode(bytes.NewReader(raw)); err == nil {
		return img, true
	}
	return nil, false
}

// decodeImageConfig 只读尺寸（不解码全图）——失败 = 零矩形。
func decodeImageConfig(raw []byte) image.Rectangle {
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return image.Rectangle{}
	}
	return image.Rect(0, 0, config.Width, config.Height)
}

// scaleImageNearest 等比缩放到长边 ≤ maxEdge（最近邻——识图/OCR 场景够用，零外部依赖）。
func scaleImageNearest(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || maxEdge <= 0 {
		return src
	}
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxEdge {
		return src
	}
	scale := float64(maxEdge) / float64(longest)
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)
	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		sourceY := bounds.Min.Y + y*height/newHeight
		for x := 0; x < newWidth; x++ {
			sourceX := bounds.Min.X + x*width/newWidth
			dst.Set(x, y, src.At(sourceX, sourceY))
		}
	}
	return dst
}

// flattenOnWhite 透明底压白——JPEG 不支持 alpha，直接编码会把透明变黑。
func flattenOnWhite(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Over)
	return dst
}

// encodeJPEG 编码 JPEG——失败返回 nil。
func encodeJPEG(src image.Image, quality int) []byte {
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, flattenOnWhite(src), &jpeg.Options{Quality: quality}); err != nil {
		return nil
	}
	return buffer.Bytes()
}

// shrinkVisionImage 视觉图片降分辨率——返回（新字节, 新 MIME, 说明；说明空 = 未改动）。
//
// 判定顺序：字节 ≤ 软上限且长边 ≤ 长边上限 → 原样；否则解码 → 逐档缩放/质量重编码，
// 命中软上限即停。解码失败 → 原样发送并出声（失败可见，不静默）。
func shrinkVisionImage(raw []byte, mime string) ([]byte, string, string) {
	if len(raw) == 0 {
		return raw, mime, ""
	}
	rect := decodeImageConfig(raw)
	longEdge := rect.Dx()
	if rect.Dy() > longEdge {
		longEdge = rect.Dy()
	}
	if len(raw) <= visionSoftImageBytes && longEdge <= visionMaxImageEdge {
		return raw, mime, ""
	}
	source, ok := decodeImageAny(raw, mime)
	if !ok {
		return raw, mime, "图片解码失败（格式不受支持：标准库仅 jpeg/png/gif）——按原字节发送"
	}
	var latest []byte
	for _, edge := range visionShrinkEdges {
		scaled := scaleImageNearest(source, edge)
		for _, quality := range visionJPEGQualities {
			encoded := encodeJPEG(scaled, quality)
			if len(encoded) == 0 {
				continue
			}
			latest = encoded
			if len(encoded) <= visionSoftImageBytes {
				bounds := scaled.Bounds()
				note := "视觉图片已降分辨率：" + itoa(rect.Dx()) + "x" + itoa(rect.Dy()) + " / " + itoa(len(raw)) +
					" 字节 → " + itoa(bounds.Dx()) + "x" + itoa(bounds.Dy()) + " / " + itoa(len(encoded)) +
					" 字节（JPEG q" + itoa(quality) + "）"
				return encoded, "image/jpeg", note
			}
		}
	}
	if len(latest) == 0 {
		return raw, mime, "图片重编码失败——按原字节发送"
	}
	note := "视觉图片已降到最小档仍超软上限：" + itoa(len(raw)) + " 字节 → " + itoa(len(latest)) + " 字节（按最小档发送）"
	return latest, "image/jpeg", note
}

// ResolveImagePayloadForVision 视觉请求图片载荷——与 ResolveImagePayload 同口径，
// 本地图片先过降分辨率保护；外部 URL 保持直传（由上游自行抓取）。
//
// 返回（image_url 串, 错误文本, 说明——说明空 = 未降级）。
func ResolveImagePayloadForVision(imagePath string) (string, string, string) {
	trimmed := strings.TrimSpace(imagePath)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		url, errText := ResolveImagePayload(trimmed)
		return url, errText, ""
	}
	info, err := os.Stat(trimmed)
	if err != nil || info.IsDir() {
		return "", "ERR|IMAGE_NOT_FOUND|图片文件不存在: " + trimmed, ""
	}
	if info.Size() > maxInlineImageBytes {
		return "", "ERR|IMAGE_TOO_LARGE|图片超过 32MiB 内联上限（" + itoa(int(info.Size())) + " 字节——请压缩或改用 Files API）", ""
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return "", "ERR|IMAGE_READ|图片读取失败: " + err.Error(), ""
	}
	mime := SniffImageMIME(raw)
	if mime == "" {
		mime = GuessImageMIME(trimmed)
	}
	shrunk, shrunkMIME, note := shrinkVisionImage(raw, mime)
	if shrunkMIME != "" {
		mime = shrunkMIME
	}
	if mime == "" {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(shrunk), "", note
}
