package session

// 图片注入分部——移植 C# ChatSession.ImageInject（design-ch4-chat-images §八 · A105 图片真实插入主干）。
//
// 形态：工具（image-inject）批后段把本批登记的图片引用合并为**一条 user 注入消息**
// （占位 + 接力——真图由请求构造面展开为内容块）。
// 约束：只追加不改写（C1 缓存纪律）；注入消息落在 timeback 作用域区间内
// （back 回收时与查证过程一并删除，图不常驻主干）。
// 门禁：工具侧仅作用域内可用（批门禁拦截）——本分部只负责批后段的注入动作。

import (
	"encoding/json"
	"strconv"
	"strings"
)

// imageInjectCategory 图片注入日志类别（C# 同值）。
const imageInjectCategory = "IMAGE"

// imageInjectNotice 注入消息文本头（C# 逐字）。
const imageInjectNotice = "（系统自动 · 图片插入）以下图片已插入主干，可直接查看："

// SetImagePathResolver 设置图片路径规范化器——`<rootId>:<rel>` 形态解析为绝对路径
// （装配层注入 fs.Service.Resolve；nil = 原样透传）。
func (s *Session) SetImagePathResolver(resolver func(path string) string) {
	s.mu.Lock()
	s.imagePathResolver = resolver
	s.mu.Unlock()
}

// FlushImageInjections 图片注入——批后段收集本批 image-inject 的成功调用，合并为一条 user 注入消息。
//
// 无成功调用 = 零动作；同轮多次调用合并为一条（避免连续 user 噪声）。
// 未受理（停机态 / 队列未就绪）写具名 WARN，不静默丢弃。
func (s *Session) FlushImageInjections() {
	r := s.roundState()
	r.mu.Lock()
	dogs := make([]*toolDog, len(r.dogs))
	copy(dogs, r.dogs)
	resolver := s.imagePathResolver
	r.mu.Unlock()

	paths := make([]string, 0, len(dogs))
	for _, dog := range dogs {
		if dog == nil || dog.Name != "image-inject" {
			continue
		}
		if dog.Result == "" || strings.HasPrefix(dog.Result, "ERR|") {
			continue
		}
		path := extractImageInjectPath(dog.ArgsJSON, resolver)
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return
	}

	// 引用数组 + 注入文本——引用形态 = 图片绝对路径（请求构造面展开为内容块）
	var notice strings.Builder
	notice.WriteString(imageInjectNotice)
	for _, path := range paths {
		notice.WriteString("\n")
		notice.WriteString(path)
	}
	imagesJSON := buildImagesJSON(paths)

	// 经系统注入口入队（等价于系统输入一行；附件随条目携带，批后段同帧消费）
	var posted string
	if s.Pending == nil {
		posted = "ERR|BAD_ARGS|待处理队列未就绪"
	} else {
		posted = s.Pending.PostSystem(SysKindSystemAuto, notice.String(), imagesJSON)
	}
	if strings.HasPrefix(posted, "ERR|") {
		logAt(LogModuleHost, 2, "图片注入未受理: "+posted, imageInjectCategory)
		return
	}
	logAt(LogModuleHost, 1, "图片注入：本批 "+strconv.Itoa(len(paths))+" 张（首张 "+paths[0]+"）", imageInjectCategory)
	s.Timeback.NoteEvent()
}

// extractImageInjectPath 提取 image-inject 参数中的图片路径——参数面仅 path；解析失败 / 缺失 = 空串。
//
// 提取后经根寻址规范化（A108）——`<rootId>:<rel>` → 绝对路径：引用数组落绝对路径，
// 展开面（请求构造）/ 渲染面 / 归档面全链同源。
func extractImageInjectPath(argsJSON string, resolver func(path string) string) string {
	if argsJSON == "" {
		return ""
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argsJSON), &root); err != nil {
		return ""
	}
	pathRaw, ok := root["path"]
	if !ok {
		return ""
	}
	var path string
	if err := json.Unmarshal(pathRaw, &path); err != nil {
		return ""
	}
	return normalizeImagePath(strings.TrimSpace(path), resolver)
}

// normalizeImagePath 根寻址规范化——非根寻址（无冒号 / http(s) URL）原样返回；
// 解析失败（越界 / 未知根 id / 无可用根表）同样原样透传（失败可见性归下游展开面）。
func normalizeImagePath(path string, resolver func(path string) string) string {
	if path == "" {
		return path
	}
	lower := strings.ToLower(path)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return path
	}
	// 冒号位置 > 0 才可能是命名空间寻址（盘符 `C:\…` 由 Resolve 内部按「无匹配 id」原样回落）
	if strings.Index(path, ":") <= 0 {
		return path
	}
	if resolver == nil {
		return path
	}
	full := resolver(path)
	if full != "" {
		return full
	}
	logAt(LogModuleSession, 2, "图片路径解析失败，原样透传: "+path, LogCategorySys)
	return path
}

// buildImagesJSON 构造附件引用数组 JSON——逐项 JSONString 转义（C# JsonUtil.Array 同口径）。
func buildImagesJSON(paths []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, path := range paths {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(JSONString(path))
	}
	b.WriteByte(']')
	return b.String()
}
