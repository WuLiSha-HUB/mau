package bricks

// VISION 两件积木——VisionCat 工具执行面（R2.2 视觉识别域）。
//
// 语义对齐 C# BRIK-VISION-001/002：
//   - vision.analyze：参数面零容忍 → 视觉服务分析 → A214 头 + 摘要行 + 正文；
//     服务层 ERR| 文本按工具成功返回（失败原因对 Dog 可见），参数面错误按工具失败返回。
//   - image.inject：只做参数校验 + 登记回执；图片由宿主批后段合并注入一条 user 消息
//     （host/session FlushImageInjections），本积木不触前文。
// 域门禁（image-inject / image-analyze 仅 image_vision 域内可用）归宿主派发面判定，积木不重复判定。

import (
	"strconv"
	"strings"
)

// 工具名常量（A214 头的 tool 字段）。
const (
	toolImageAnalyze = "image-analyze"
	toolImageInject  = "image-inject"
)

// VisionService 是视觉识别执行面——由宿主装配注入（nil = 未注入，工具诚实报错）。
type VisionService interface {
	// Analyze 执行一次图像识别——返回分析文本或 ERR| 错误文本
	Analyze(imagePath, question string) string
}

// ImageGate 是图片通道可用性判定——宿主 LLM 面的图片通道降级态（nil = 不判定）。
//
// 语义：主模型请求体里的图像内容块发不出去时（本地网关请求体上限 / 对端断连），
// image-inject 直接拒绝并提示绕行——不让「已登记」的假回执把模型拖进重试死路。
type ImageGate interface {
	// ImageChannelDown 返回（降级原因, 是否降级中）
	ImageChannelDown() (string, bool)
}

// ImageAnalyze 执行 vision.analyze 工具——image-analyze 图像识别。
func ImageAnalyze(vision VisionService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "path question", "path", "", ""); badArgs != "" {
		return badArgs, false
	}
	path := Get(argsJSON, "path")
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	question := Get(argsJSON, "question")
	if vision == nil {
		return "ERR|VISION_NO_SERVICE|宿主未注入 IVisionService", false
	}
	body := vision.Analyze(path, question)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolImageAnalyze, true, path, -1, [][2]any{{"question", question}, {"chars", chars}})
	result := head + "\n" + path + " | " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}

// ImageInject 执行 image.inject 工具——图片插入主干（登记回执；宿主批后段执行实际注入）。
//
// 自动回避：图片通道降级期直接拒绝（ERR|VISION_UNAVAILABLE + 绕行提示），不发登记回执
// ——否则批后段会把图注入前文，下一轮请求又撞同一堵墙（判例 2026-11-14 图片请求超网关上限）。
func ImageInject(gate ImageGate, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "path", "path", "", ""); badArgs != "" {
		return badArgs, false
	}
	path := Get(argsJSON, "path")
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	if gate != nil {
		if reason, down := gate.ImageChannelDown(); down {
			return "ERR|VISION_UNAVAILABLE|当前视觉 API 不可用（" + truncateVisionReason(reason) +
				"）——请绕行：本轮不要再用 image-inject / image-analyze，改用文字线索或让用户确认；恢复后自动可用", false
		}
	}
	head := MetaHeadFields(toolImageInject, true, path, -1, nil)
	return head + "\n" + path + " | 已登记（宿主批后段注入）", true
}

// truncateVisionReason 错误原因截断——按 rune 截 120 字（守卫回执不因长错误膨胀）。
func truncateVisionReason(text string) string {
	const limit = 120
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
