package admin

import (
	"encoding/json"
	"net/http"
	"strings"
)

// 本文件复现管理端段 7b「用户态」——当前用户 + QQ 非用户指令开关 + 群内身份清单。
//
// 契约对齐 `mau:CatHome4/CatHome4.Admin/AdminService.UserState.cs` 与
// `CatHome4.Contracts/UserStateKeys.cs`：存储 = 配置群 user.cfg
// （user.current / user.qq_accept_nonuser / user.qq_ids）；
// 写入走 SetChecked（归一 → schema 白名单 + 值域 → 原子写 + 失败回滚）。

// 用户态配置键（跨域单一真相源——对齐 UserStateKeys）。
const (
	// userKeyCurrent 当前用户（值 = 显示名）
	userKeyCurrent = "user.current"
	// userKeyAcceptNonUser 接受非用户的指令（bool 文本）
	userKeyAcceptNonUser = "user.qq_accept_nonuser"
	// userKeyQQIDs 当前用户群内身份清单（逗号分隔）
	userKeyQQIDs = "user.qq_ids"
	// defaultUserName 缺省用户显示名（配置缺失 / 非法时回落）
	defaultUserName = "莎"
)

// userProfile 是当前用户候选档案。
type userProfile struct {
	// Name 显示名（配置值即显示名）
	Name string `json:"name"`
	// Desc 一行说明（弹层选项副标题）
	Desc string `json:"desc"`
}

// userProfiles 是用户档案表（首位 = 缺省用户）。
var userProfiles = []userProfile{
	{Name: defaultUserName, Desc: "源实例——CatHome 4 与 CCBP 知识网络的搭建者"},
	{Name: "Rushiera", Desc: "莎的朋友——本支主理（2026-09-25 起）"},
}

// userStateGetResponse 是 GET /api/v1/user-state 的响应体（五键）。
type userStateGetResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Current 当前用户显示名
	Current string `json:"current"`
	// AcceptNonUser 是否接受非用户指令
	AcceptNonUser bool `json:"acceptNonUser"`
	// QQIDs 群内身份清单（逗号分隔原文）
	QQIDs string `json:"qqIds"`
	// Users 候选档案
	Users []userProfile `json:"users"`
}

// userStateWriteResponse 是 POST /api/v1/user-state 的响应体（四键——不含 users）。
type userStateWriteResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Current 落盘后的当前用户
	Current string `json:"current"`
	// AcceptNonUser 落盘后的开关值
	AcceptNonUser bool `json:"acceptNonUser"`
	// QQIDs 落盘后的身份清单
	QQIDs string `json:"qqIds"`
}

// handleUserStateGet 服务 GET /api/v1/user-state——用户态读取。
func (d *Deps) handleUserStateGet(w http.ResponseWriter, r *http.Request) {
	users := make([]userProfile, 0, len(userProfiles))
	users = append(users, userProfiles...)
	writeJSON(w, userStateGetResponse{
		OK:            true,
		Current:       d.currentUserName(),
		AcceptNonUser: d.acceptNonUserCommands(),
		QQIDs:         strings.Join(d.currentUserQQIDs(), ","),
		Users:         users,
	})
}

// handleUserStatePost 服务 POST /api/v1/user-state——用户态写入（字段级语义：未提交字段逐字保留）。
func (d *Deps) handleUserStatePost(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeErr(w, "配置存储未绑定")
		return
	}
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	current := fieldString(fields, "current")
	ids, idsPresent := fieldStringPresent(fields, "qqIds")
	accept, acceptPresent := fieldBoolPresent(fields, "acceptNonUser")
	if current != "" {
		if !knownUser(current) {
			writeErr(w, "未知用户: "+current)
			return
		}
		if ok, reason := d.Store.SetChecked(userKeyCurrent, current, d.Schema); !ok {
			writeErr(w, reason)
			return
		}
	}
	if acceptPresent {
		flag := "false"
		if accept {
			flag = "true"
		}
		if ok, reason := d.Store.SetChecked(userKeyAcceptNonUser, flag, d.Schema); !ok {
			writeErr(w, reason)
			return
		}
	}
	if idsPresent {
		if ok, reason := d.Store.SetChecked(userKeyQQIDs, strings.TrimSpace(ids), d.Schema); !ok {
			writeErr(w, reason)
			return
		}
	}
	d.log(1, "用户态已写入：当前用户="+d.currentUserName()+
		" | 接受非用户指令="+boolText(d.acceptNonUserCommands())+
		" | 群内身份 "+itoa(len(d.currentUserQQIDs()))+" 项")
	writeJSON(w, userStateWriteResponse{
		OK:            true,
		Current:       d.currentUserName(),
		AcceptNonUser: d.acceptNonUserCommands(),
		QQIDs:         strings.Join(d.currentUserQQIDs(), ","),
	})
}

// currentUserName 当前用户显示名——配置读取（缺失 / 空回落缺省用户）。
func (d *Deps) currentUserName() string {
	if d.Store == nil || d.Schema == nil {
		return defaultUserName
	}
	name := d.Store.Resolve(d.Schema, userKeyCurrent).Value
	if name == "" {
		return defaultUserName
	}
	return name
}

// acceptNonUserCommands 接受非用户的指令——配置读取（缺失 / 非法回落 false）。
func (d *Deps) acceptNonUserCommands() bool {
	if d.Store == nil || d.Schema == nil {
		return false
	}
	value := d.Store.Resolve(d.Schema, userKeyAcceptNonUser).Value
	return value == "true" || value == "1"
}

// currentUserQQIDs 当前用户群内身份清单（逗号分隔 → 去空白去空项）。
func (d *Deps) currentUserQQIDs() []string {
	raw := ""
	if d.Store != nil && d.Schema != nil {
		raw = d.Store.Resolve(d.Schema, userKeyQQIDs).Value
	}
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// knownUser 显示名是否在档案表内（精确匹配）。
func knownUser(name string) bool {
	for _, profile := range userProfiles {
		if profile.Name == name {
			return true
		}
	}
	return false
}

// boolText 布尔转 C# 口径文本。
func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// fieldStringPresent 取字符串字段并保留存在性（缺字段 / 非字符串 = 未提交）。
func fieldStringPresent(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

// fieldBoolPresent 取布尔字段并保留存在性（缺字段 / 非布尔 = 未提交）。
func fieldBoolPresent(fields map[string]json.RawMessage, key string) (bool, bool) {
	raw, ok := fields[key]
	if !ok {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}
