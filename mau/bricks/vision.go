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
func ImageInject(argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "path", "path", "", ""); badArgs != "" {
		return badArgs, false
	}
	path := Get(argsJSON, "path")
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	head := MetaHeadFields(toolImageInject, true, path, -1, nil)
	return head + "\n" + path + " | 已登记（宿主批后段注入）", true
}
