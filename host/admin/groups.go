package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"cathome5/host/config"
	"cathome5/host/group"
)

// 本文件是配队（Group）数据面端点——对齐 C# `AdminService.Groups.cs` 与 `AdminRoutes.cs` 段 13 七条。
//
// 语义：组配置 / 成员 / 消息流归 group.Store（Data/group/<组名>/）；本文件只管 HTTP 装配与成员显示名解析。
// 边界：组轻宿主（独立端口）归 P5——本文件的 port / running 恒 0 / false（未启动宿主）。

// groupMemberRow 是成员行——catKey 是稳定主键，显示名只是附属内容。
type groupMemberRow struct {
	// Key 成员猫 key
	Key string `json:"key"`
	// DisplayName 显示名（cat.cfg）
	DisplayName string `json:"displayName"`
	// Admin 管理员标记
	Admin bool `json:"admin"`
	// Subscriber 订阅标记
	Subscriber bool `json:"subscriber"`
	// ThemeColor 主题色索引（0 = 默认白）
	ThemeColor int `json:"themeColor"`
	// Running 运行态
	Running bool `json:"running"`
	// RunState 细粒度运行态
	RunState string `json:"runState"`
	// Context 已知最新前文长度
	Context int64 `json:"context"`
}

// groupRow 是单组投影——配置 + 统计 + 成员运行面。
type groupRow struct {
	// Name 组名
	Name string `json:"name"`
	// Members 成员行
	Members []groupMemberRow `json:"members"`
	// AdminKeys 管理员 key 名单
	AdminKeys []string `json:"adminKeys"`
	// SubscriberKeys 订阅 key 名单
	SubscriberKeys []string `json:"subscriberKeys"`
	// Project 项目绑定
	Project string `json:"project"`
	// CreatedAt 创建时刻（Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
	// Layer 层级（预留）
	Layer string `json:"layer"`
	// Enabled 启用意图
	Enabled bool `json:"enabled"`
	// LastSeq 最大消息序号
	LastSeq int64 `json:"lastSeq"`
	// MsgCount 消息条数
	MsgCount int `json:"msgCount"`
	// LastAt 最新消息时刻（Unix 毫秒）
	LastAt int64 `json:"lastAt"`
	// SubscribeIdleSec 订阅静默阈值（秒）
	SubscribeIdleSec int `json:"subscribeIdleSec"`
	// Port 组宿主端口（0 = 未启动）
	Port int `json:"port"`
	// Running 组宿主运行态
	Running bool `json:"running"`
}

// groupsResponse 是 GET /api/v1/groups 的响应体。
type groupsResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Groups 组行列表
	Groups []groupRow `json:"groups"`
}

// groupMessagesResponse 是组消息流响应体。
type groupMessagesResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Name 组名
	Name string `json:"name"`
	// LastSeq 组内最大序号
	LastSeq int64 `json:"lastSeq"`
	// HasMore 更早还有未返回条目
	HasMore bool `json:"hasMore"`
	// Messages 消息列表（seq 升序）
	Messages []groupMessageRow `json:"messages"`
}

// groupMessageRow 是组消息行（发送方带显示名）。
type groupMessageRow struct {
	// Seq 组内序号
	Seq int64 `json:"seq"`
	// From 发送方猫 key
	From string `json:"from"`
	// DisplayName 发送方显示名
	DisplayName string `json:"displayName"`
	// To @ 目标猫 key
	To string `json:"to"`
	// ToDisplayName @ 目标显示名
	ToDisplayName string `json:"toDisplayName"`
	// Type 消息类型
	Type string `json:"type"`
	// Text 正文
	Text string `json:"text"`
	// Ts 发送时刻（Unix 毫秒）
	Ts int64 `json:"ts"`
	// Refs 关联锚
	Refs string `json:"refs"`
}

// groupHostRow 是组宿主端口行。
type groupHostRow struct {
	// Name 组名
	Name string `json:"name"`
	// Port 监听端口（0 = 未启动）
	Port int `json:"port"`
	// Running 运行态
	Running bool `json:"running"`
}

// groupHostsResponse 是 GET /api/v1/groups/host 的响应体。
type groupHostsResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Hosts 组宿主行
	Hosts []groupHostRow `json:"hosts"`
}

// groupMutationResponse 是写面通用回执（ok + detail）。
type groupMutationResponse struct {
	// OK 受理标志
	OK bool `json:"ok"`
	// Detail 明细（ok| 前缀原文）
	Detail string `json:"detail"`
}

// groupDeliverResponse 是派单投递回执（delivered + seq + to / warning 二选一）。
type groupDeliverResponse struct {
	// OK 受理标志
	OK bool `json:"ok"`
	// Detail 明细（ok| 前缀原文）
	Detail string `json:"detail"`
	// Delivered 是否投递到目标会话
	Delivered bool `json:"delivered"`
	// Seq 消息序号
	Seq int64 `json:"seq"`
	// To 投递目标（成功时出现）
	To string `json:"to,omitempty"`
	// Warning 未投递原因（失败时出现）
	Warning string `json:"warning,omitempty"`
}

// handleGroupsGet 服务 GET /api/v1/groups——组列表（配队页数据源）。
func (d *Deps) handleGroupsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, groupsResponse{OK: true, Groups: d.buildGroupRows()})
}

// buildGroupRows 组列表行装配（创建时刻升序，Store 已排序）。
func (d *Deps) buildGroupRows() []groupRow {
	rows := []groupRow{}
	if d.Groups == nil {
		return rows
	}
	for _, summary := range d.Groups.ListGroups() {
		rows = append(rows, d.buildGroupRow(summary))
	}
	return rows
}

// buildGroupRow 单行组投影——配置 + 统计 + 成员显示名与运行面。
func (d *Deps) buildGroupRow(summary group.Summary) groupRow {
	members := make([]groupMemberRow, 0, len(summary.MemberKeys))
	for _, key := range summary.MemberKeys {
		running, runState, contextTokens := d.memberRuntime(key)
		members = append(members, groupMemberRow{
			Key:         key,
			DisplayName: d.catDisplayName(key),
			Admin:       contains(summary.AdminKeys, key),
			Subscriber:  contains(summary.SubscriberKeys, key),
			ThemeColor:  d.catThemeColor(key),
			Running:     running,
			RunState:    runState,
			Context:     contextTokens,
		})
	}
	return groupRow{
		Name:             summary.Name,
		Members:          members,
		AdminKeys:        nonNil(summary.AdminKeys),
		SubscriberKeys:   nonNil(summary.SubscriberKeys),
		Project:          summary.Project,
		CreatedAt:        summary.CreatedAt,
		Layer:            summary.Layer,
		Enabled:          summary.Enabled,
		LastSeq:          summary.LastSeq,
		MsgCount:         summary.MsgCount,
		LastAt:           summary.LastAt,
		SubscribeIdleSec: group.GetSubscribeIdleSec(&summary),
		Port:             d.groupHostPort(summary.Name),
		Running:          d.groupHostRunning(summary.Name),
	}
}

// memberRuntime 成员运行面——单猫形态：仅主干会话有会话态，其余按未运行。
func (d *Deps) memberRuntime(catKey string) (running bool, runState string, contextTokens int64) {
	runState = "idle"
	if d.Session == nil || catKey == "" {
		return false, runState, 0
	}
	if catKey != d.CatID && catKey != d.DisplayName {
		return false, runState, 0
	}
	running = d.Running
	state := d.Session.StateEntry()
	if text, ok := state["runState"].(string); ok && text != "" {
		runState = text
	}
	contextTokens = numberValue(state["context"])
	return running, runState, contextTokens
}

// catDisplayName 猫显示名——cat.cfg 的 displayName（配置缺失 = 空串）。
func (d *Deps) catDisplayName(catKey string) string {
	if catKey == "" {
		return ""
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(catKey))
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.DisplayName
}

// catThemeColor 猫主题色——cat.cfg 的 themeColor（缺失 / 负值 = 0 默认白）。
func (d *Deps) catThemeColor(catKey string) int {
	if catKey == "" {
		return 0
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(catKey))
	if err != nil || cfg == nil || cfg.ThemeColor < 0 {
		return 0
	}
	return cfg.ThemeColor
}

// groupHostPort 组宿主端口——Go 侧组轻宿主归 P5，未启动恒 0。
func (d *Deps) groupHostPort(name string) int { return 0 }

// groupHostRunning 组宿主运行态——Go 侧组轻宿主归 P5，未启动恒 false。
func (d *Deps) groupHostRunning(name string) bool { return false }

// handleGroupsPost 服务 POST /api/v1/groups——新建 / 改组（同名覆盖成员与项目）。
func (d *Deps) handleGroupsPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	name := strings.TrimSpace(fieldString(fields, "name"))
	project := strings.TrimSpace(fieldString(fields, "project"))
	memberKeys := fieldStringArray(fields, "memberKeys")
	adminKeys, _ := fieldStringArrayPresent(fields, "adminKeys")
	subscriberKeys, _ := fieldStringArrayPresent(fields, "subscriberKeys")
	subscribeIdleSec := -1
	if raw, exists := fields["subscribeIdleSec"]; exists {
		var value int
		if err := json.Unmarshal(raw, &value); err == nil {
			subscribeIdleSec = value
		}
	}
	if d.Groups == nil {
		writeErr(w, "ERR|GROUP_FAIL|组数据面未接线")
		return
	}
	result := d.Groups.CreateOrUpdate(name, memberKeys, project, adminKeys, subscriberKeys, subscribeIdleSec)
	if strings.HasPrefix(result, "ok|") {
		if created := d.Groups.GetGroup(name); created != nil && created.Enabled {
			d.Groups.StartGroup(created.Name)
		}
	}
	writeMutation(w, result)
}

// handleGroupsEnabled 服务 POST /api/v1/groups/enabled——组启停。
func (d *Deps) handleGroupsEnabled(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	name := strings.TrimSpace(fieldString(fields, "name"))
	enabled := true
	if raw, exists := fields["enabled"]; exists {
		var value bool
		if err := json.Unmarshal(raw, &value); err == nil && !value {
			enabled = false
		}
	}
	if d.Groups == nil {
		writeErr(w, "ERR|GROUP_FAIL|组数据面未接线")
		return
	}
	result := d.Groups.SetEnabled(name, enabled)
	if strings.HasPrefix(result, "ok|") {
		if enabled {
			d.Groups.StartGroup(name)
		} else {
			d.Groups.StopGroup(name)
		}
	}
	writeMutation(w, result)
}

// handleGroupsDelete 服务 POST /api/v1/groups/delete——删组（整目录进 _trash）。
func (d *Deps) handleGroupsDelete(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	name := strings.TrimSpace(fieldString(fields, "name"))
	if d.Groups == nil {
		writeErr(w, "ERR|GROUP_FAIL|组数据面未接线")
		return
	}
	writeMutation(w, d.Groups.Delete(name))
}

// handleGroupsMessagesGet 服务 GET /api/v1/groups/{name}/messages——组消息流。
func (d *Deps) handleGroupsMessagesGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, "ERR|BAD_ARGS|缺少组名")
		return
	}
	d.buildGroupMessagesResult(w, r, name)
}

// buildGroupMessagesResult 组消息流共用实现——after/limit 非法一律报错，不静默回落。
func (d *Deps) buildGroupMessagesResult(w http.ResponseWriter, r *http.Request, name string) {
	afterText := r.URL.Query().Get("after")
	after := int64(0)
	if afterText != "" {
		parsed, err := strconv.ParseInt(afterText, 10, 64)
		if err != nil {
			writeErr(w, "ERR|BAD_ARGS|after 非整数: "+afterText)
			return
		}
		after = parsed
	}
	limitText := r.URL.Query().Get("limit")
	limit := 0
	if limitText != "" {
		parsed, err := strconv.Atoi(limitText)
		if err != nil {
			writeErr(w, "ERR|BAD_ARGS|limit 非整数: "+limitText)
			return
		}
		limit = parsed
	}
	if d.Groups == nil {
		writeErr(w, "ERR|GROUP_FAIL|组数据面未接线")
		return
	}
	summary := d.Groups.GetGroup(name)
	if summary == nil {
		writeErr(w, "ERR|GROUP_NOT_FOUND|组不存在: "+name)
		return
	}
	var messages []group.Message
	hasMore := false
	if afterText != "" {
		messages = d.Groups.ReadMessages(name, after, limit)
	} else {
		messages, hasMore = d.Groups.ReadTailMessages(name, limit)
	}
	rows := make([]groupMessageRow, 0, len(messages))
	for _, message := range messages {
		display := ""
		if message.From != "" {
			display = d.catDisplayName(message.From)
		}
		toDisplay := ""
		if message.To != "" {
			toDisplay = d.catDisplayName(message.To)
		}
		rows = append(rows, groupMessageRow{
			Seq:           message.Seq,
			From:          message.From,
			DisplayName:   display,
			To:            message.To,
			ToDisplayName: toDisplay,
			Type:          message.Type,
			Text:          message.Text,
			Ts:            message.Ts,
			Refs:          message.Refs,
		})
	}
	writeJSON(w, groupMessagesResponse{OK: true, Name: summary.Name, LastSeq: summary.LastSeq, HasMore: hasMore, Messages: rows})
}

// handleGroupsMessagesPost 服务 POST /api/v1/groups/messages——组投递。
func (d *Deps) handleGroupsMessagesPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	if d.Groups == nil {
		writeErr(w, "ERR|GROUP_FAIL|组数据面未接线")
		return
	}
	d.appendGroupMessage(w,
		strings.TrimSpace(fieldString(fields, "group")),
		strings.TrimSpace(fieldString(fields, "from")),
		strings.TrimSpace(fieldString(fields, "type")),
		fieldString(fields, "text"),
		strings.TrimSpace(fieldString(fields, "refs")),
		strings.TrimSpace(fieldString(fields, "to")))
}

// appendGroupMessage 投递共用实现——to 非空按派单处理（类型强制 delegation，回执链自动生效）。
func (d *Deps) appendGroupMessage(w http.ResponseWriter, groupName, from, msgType, text, refs, to string) {
	useTo := strings.TrimSpace(to)
	useType := msgType
	if useTo != "" {
		useType = "delegation"
	}
	result := d.Groups.AppendMessage(groupName, from, useType, text, refs, useTo)
	if !strings.HasPrefix(result, "ok|") {
		writeMutation(w, result)
		return
	}
	if useTo == "" {
		writeJSON(w, groupMutationResponse{OK: true, Detail: result})
		return
	}
	// 派单——投递目标（未运行 / 未受理：消息已入组，带提示不吞）
	seq := parseGroupSeq(result)
	name := groupName
	if current := d.Groups.GetGroup(groupName); current != nil {
		name = current.Name
	}
	content := "[来自组:" + name + "]\n" + text
	origin := "group:" + name + ":" + strconv.FormatInt(seq, 10) + ":delegation"
	if !d.isKnownCat(useTo) {
		writeJSON(w, groupDeliverResponse{OK: true, Detail: result, Delivered: false, Seq: seq,
			Warning: "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + useTo + "（消息已入组 #" + strconv.FormatInt(seq, 10) + "）"})
		return
	}
	if d.Session == nil || !d.Session.PostUser(content, "user", "", origin) {
		writeJSON(w, groupDeliverResponse{OK: true, Detail: result, Delivered: false, Seq: seq,
			Warning: "ERR|GROUP_DELIVER_FAIL|投递未受理: " + useTo + "（消息已入组 #" + strconv.FormatInt(seq, 10) + "）"})
		return
	}
	writeJSON(w, groupDeliverResponse{OK: true, Detail: result, Delivered: true, Seq: seq, To: useTo})
}

// isKnownCat 投递目标可达判定——单猫形态：仅主干 key / 显示名。
func (d *Deps) isKnownCat(catKey string) bool {
	if catKey == "" {
		return false
	}
	return catKey == d.CatID || catKey == d.DisplayName
}

// handleGroupsHostGet 服务 GET /api/v1/groups/host——组轻宿主端口（前端「打开」寻址）。
func (d *Deps) handleGroupsHostGet(w http.ResponseWriter, r *http.Request) {
	hosts := []groupHostRow{}
	if d.Groups != nil {
		for _, summary := range d.Groups.ListGroups() {
			hosts = append(hosts, groupHostRow{Name: summary.Name, Port: d.groupHostPort(summary.Name), Running: d.groupHostRunning(summary.Name)})
		}
	}
	writeJSON(w, groupHostsResponse{OK: true, Hosts: hosts})
}

// writeMutation 写面回执——ok| 前缀 = 成功；其余原样作 error；空结果具名失败。
func writeMutation(w http.ResponseWriter, result string) {
	if result == "" {
		writeErr(w, "ERR|GROUP_FAIL|未返回结果")
		return
	}
	if strings.HasPrefix(result, "ok|") {
		writeJSON(w, groupMutationResponse{OK: true, Detail: result})
		return
	}
	writeErr(w, result)
}

// parseGroupSeq 组消息回执解析——ok|组名|seq=N → N；解析失败 0。
func parseGroupSeq(result string) int64 {
	index := strings.LastIndex(result, "seq=")
	if index < 0 {
		return 0
	}
	seq, err := strconv.ParseInt(result[index+4:], 10, 64)
	if err != nil {
		return 0
	}
	return seq
}

// fieldStringArrayPresent 取字符串数组字段（带存在性）——缺字段 / 非数组 = nil, false。
func fieldStringArrayPresent(fields map[string]json.RawMessage, key string) ([]string, bool) {
	raw, exists := fields[key]
	if !exists {
		return nil, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		var value string
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(value))
	}
	return out, true
}

// numberValue 状态字段取值——数值断言（int / int64 / float64 三态）。
func numberValue(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	}
	return 0
}

// nonNil 空数组兜底（响应恒为数组，不写 null）。
func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}
