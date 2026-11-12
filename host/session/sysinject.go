package session

import "strings"

// 系统注入族名——sys 路径来源值域（systemauto + 延迟族）。
const (
	// SysKindSystemAuto 系统注入类型（现行唯一值）
	SysKindSystemAuto = "systemauto"
	// SysKindSleep 延迟族——sleep 唤醒来源
	SysKindSleep = "sleep"
	// SysKindTimer 延迟族——timer 排程注入来源
	SysKindTimer = "timer"
	// SysKindDelay 延迟族——delay 人工延迟来源
	SysKindDelay = "delay"
	// SysKindRestart 延迟族——restart 宿主重启回执来源
	SysKindRestart = "restart"
	// SysInjectPrefix 系统注入行头前缀（判据：user 消息内容以该前缀开头 = 系统注入）
	SysInjectPrefix = "[systemauto]"
	// SourceUser 来源标记——人工输入
	SourceUser = "user"
)

// SysKindFamily 是 sys 路径来源值域集合。
var SysKindFamily = []string{SysKindSystemAuto, SysKindSleep, SysKindTimer, SysKindDelay, SysKindRestart}

// IsKnownSysKind 系统注入来源值域校验（参数面零容忍：未知来源拒绝并出声）。
func IsKnownSysKind(kind string) bool {
	for _, item := range SysKindFamily {
		if item == kind {
			return true
		}
	}
	return false
}

// IsSysSource 路径判定——user / 空 = 人工路径；其余族名 = sys 路径。
func IsSysSource(source string) bool {
	if source == "" || source == SourceUser {
		return false
	}
	return true
}

// BuildSysInjectText 系统注入文本加工——幂等套 [systemauto] 行头前缀。
func BuildSysInjectText(content string) string {
	if strings.HasPrefix(content, SysInjectPrefix) {
		return content
	}
	return SysInjectPrefix + content
}

// IsSysInjectText 行头判定——内容以固定前缀开头即系统注入（前文/视图侧判据）。
func IsSysInjectText(content string) bool {
	return strings.HasPrefix(content, SysInjectPrefix)
}

// MessageBlockKind 视图块类型分派——sys 来源 → systemautomessage；人工 → user。
func MessageBlockKind(source string) string {
	if IsSysSource(source) {
		return TypeSystemAutoMessage
	}
	return TypeUser
}
