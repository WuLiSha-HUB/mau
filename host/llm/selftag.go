package llm

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"cathome5/host/session"
)

// 自报头名——与 C# mau:Mau/Mau.Runtime/LlmSelfTag.cs 逐字一致。
const (
	// headerCatID 猫 key（恒发）
	headerCatID = "x-cat-id"
	// headerCatName 猫显示名（恒发）
	headerCatName = "x-cat-name"
	// headerCatSession 会话世代 ID（恒发）
	headerCatSession = "x-cat-session"
	// headerProjectMount 项目挂载名（未挂载不发）
	headerProjectMount = "x-project-mount"
	// headerProjectMountMode 挂载模式（未挂载不发）
	headerProjectMountMode = "x-project-mount-mode"
	// headerProjectJobs 项目 Job 清单（未挂载不发）
	headerProjectJobs = "x-project-jobs"
	// headerChannel 通道标识（恒发）
	headerChannel = "x-cat-channel"
	// headerClientID 客户端标识（恒发）
	headerClientID = "x-client-id"
)

// selfAPIsPort 自家 apis 端口——回环地址 + 该端口即判定为自家中转站。
const selfAPIsPort = "18100"

// selfTagValueMax 单值上限——头值只承载标识，不承载正文。
const selfTagValueMax = 120

// isSelfAPIs 判定目标端点是否自家 apis——回环地址 + 18100 端口。
//
// 解析失败一律判「不是」：宁可不加头，也不给第三方站点添未知头（C# 同口径）。
func isSelfAPIs(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Port() != selfAPIsPort {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ApplySelfTag 附加自报头——非自家 apis 直接返回 false（不触碰请求）。
//
// 幂等：同名头已存在时不覆盖（C# TryAddWithoutValidation 前先判 Contains 同口径）。
// 导出面：mau/bricks 的一次性 LLM 请求执行体（tag 为 nil 的积木出口）经组合根注入调用——
// 与 C# LlmSelfTag.Apply 同签名语义（tag 为 null 时仍发 client-id，走默认产品标识）。
func ApplySelfTag(request *http.Request, endpoint string, tag *session.SelfTag, channel string) bool {
	if request == nil || isSelfAPIs(endpoint) == false {
		return false
	}
	// [段1] 通道与客户端标识——任何出口都发，保证「哪条链路」可归因
	addSelfTagHeader(request, headerChannel, channel)
	clientID := ""
	if tag != nil {
		clientID = tag.ClientID
	}
	addSelfTagHeader(request, headerClientID, session.ClientIDOrDefault(clientID))
	if tag == nil {
		return true
	}
	// [段2] 猫身份——恒发（猫 key 是硬身份）
	addSelfTagHeader(request, headerCatID, tag.CatID)
	addSelfTagHeader(request, headerCatName, tag.CatName)
	addSelfTagHeader(request, headerCatSession, tag.SessionID)
	// [段3] 项目挂载——未挂载（空项目名）时整组不发，不发空值
	if tag.Project != "" {
		addSelfTagHeader(request, headerProjectMount, tag.Project)
		addSelfTagHeader(request, headerProjectMountMode, tag.MountMode)
		addSelfTagHeader(request, headerProjectJobs, tag.Jobs)
	}
	return true
}

// addSelfTagHeader 写单个头——空值跳过、重名跳过、控制字符净化、超长截断、非 ASCII 百分号编码。
func addSelfTagHeader(request *http.Request, name, value string) {
	clean := cleanSelfTagValue(value)
	if clean == "" {
		return
	}
	if request.Header.Get(name) != "" {
		return
	}
	request.Header.Set(name, clean)
}

// cleanSelfTagValue 头值净化——控制字符剔除、首尾空白、120 截断、非 ASCII 百分号编码。
//
// HTTP 头只允许 ASCII：中文猫名 / 中文项目名走百分号编码，接收侧解码还原（C# 同口径）。
func cleanSelfTagValue(value string) string {
	if value == "" {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(value))
	for _, c := range value {
		if c == '\r' || c == '\n' || c == '\t' || c == 0 {
			sb.WriteByte(' ')
			continue
		}
		if c < 32 {
			continue
		}
		sb.WriteRune(c)
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return ""
	}
	text = truncateSelfTagValue(text)
	for i := 0; i < len(text); i++ {
		if text[i] < 32 || text[i] > 126 {
			text = escapeSelfTagValue(text)
			break
		}
	}
	return truncateSelfTagValue(text)
}

// truncateSelfTagValue 截断到上限（按字符计，不切断多字节字符）。
func truncateSelfTagValue(text string) string {
	runes := []rune(text)
	if len(runes) <= selfTagValueMax {
		return text
	}
	return string(runes[:selfTagValueMax])
}

// escapeSelfTagValue 百分号编码——逐字节 UTF-8，保留 RFC 3986 unreserved
// （对齐 C# Uri.EscapeDataString：空格编 %20，不是 +）。
func escapeSelfTagValue(text string) string {
	const hexUpper = "0123456789ABCDEF"
	var sb strings.Builder
	sb.Grow(len(text) * 3)
	for i := 0; i < len(text); i++ {
		b := text[i]
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') ||
			b == '-' || b == '.' || b == '_' || b == '~' {
			sb.WriteByte(b)
			continue
		}
		sb.WriteByte('%')
		sb.WriteByte(hexUpper[b>>4])
		sb.WriteByte(hexUpper[b&0x0F])
	}
	return sb.String()
}
