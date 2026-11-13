package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"cathome5/host/config"
	"cathome5/host/group"
	"cathome5/host/session"
	"cathome5/mau/bricks"
)

// 本文件复现 C# CatHome4.Admin.GroupService（IGroupService 实现）——GroupCat 八件积木的宿主服务面。
//
// 语义逐条对齐 mau:CatHome4/CatHome4.Admin/GroupService.cs（839 行）：
//   - 权限：调用者须为组成员（Info / Inbox / Say / CatInfo / CatCfgGet）；Send / NewSession / CatCfgSet 另须组管理员；
//   - 写面拒改自己（CatCfgSet 拒 self）；NewSession 管理员不得重建另一名管理员（单向数据流）；
//   - 返回体统一 A214：首行元数据头（ok / tool / target / items + 专有字段）+ 摘要行 + 正文行；失败走 ERR| 前缀；
//   - 头行用 C# JsonUtil.Serialize 口径（UnsafeRelaxedJsonEscaping：中文直出）——bricks.JSONRelaxedString。
//
// 单猫形态（d-1 多猫面未实现）：投递 / 重建只服务 majordomo 会话；其余寻址按「未找到猫」明示。

// hostGroupService 是 bricks.GroupService 的宿主实现（对齐 C# IGroupService 八方法）。
type hostGroupService struct {
	// Store 组数据面（组配置 / 消息流 / 订阅游标）
	Store *group.Store
	// Router 会话指令路由（cat.info / cat.cfg.get / cat.cfg.set / 会话重建的执行体；晚绑定——[段12a] 才构造）
	Router func() *commandRouter
	// Logf 日志出口（nil = 静默）
	Logf func(level int, message, category string)
}

// router 取会话指令路由（未就绪 = nil）。
func (s *hostGroupService) router() *commandRouter {
	if s.Router == nil {
		return nil
	}
	return s.Router()
}

// log 出声（nil 安全）。
func (s *hostGroupService) log(level int, message string) {
	if s.Logf != nil {
		s.Logf(level, message, "GROUP")
	}
}

// Info 组信息——组行（成员 / 管理员 / 统计）。
func (s *hostGroupService) Info(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	g := s.Store.GetGroup(groupName)
	if g == nil {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + groupName
	}
	if !groupIsMember(g, catID) {
		return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catID
	}
	if !g.Running {
		return "ERR|GROUP_NOT_RUNNING|组未启用: " + groupName
	}
	// [段1] 成员行——显示名 + key + 角色标记
	lines := make([]string, 0, len(g.MemberKeys))
	for _, key := range g.MemberKeys {
		line := "· " + s.resolveCatDisplayName(key) + "（" + key + "）"
		roles := make([]string, 0, 2)
		if groupIsAdmin(g, key) {
			roles = append(roles, "管理员")
		}
		if groupIsSubscriber(g, key) {
			roles = append(roles, "订阅")
		}
		if len(roles) > 0 {
			line += " — " + strings.Join(roles, " / ")
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "（无成员）")
	}
	// [段2] 专有字段 + 摘要行
	extras := [][2]any{
		{"project", g.Project},
		{"enabled", g.Enabled},
		{"msgCount", g.MsgCount},
		{"lastSeq", g.LastSeq},
		{"lastAt", g.LastAt},
	}
	summary := g.Name + " | " + strconv.Itoa(len(g.MemberKeys)) + " 名成员 · 消息 " + strconv.Itoa(g.MsgCount) + " 条 · 最新 #" + strconv.FormatInt(g.LastSeq, 10)
	return groupBuildReply("group-info", g.Name, len(g.MemberKeys), extras, summary, lines)
}

// Inbox 拉取消息——按 after / limit 取组内消息流（seq 升序）。
func (s *hostGroupService) Inbox(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	after := int64(0)
	limit := groupDefaultLimit
	if argsJSON != "" {
		var anyValue any
		if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
			return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if obj, isObj := anyValue.(map[string]any); isObj {
			if value, ok := groupJSONInt64(obj, "after"); ok {
				after = value
			}
			if value, ok := groupJSONInt64(obj, "limit"); ok {
				limit = int(value)
			}
		}
	}
	g := s.Store.GetGroup(groupName)
	if g == nil {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + groupName
	}
	if !groupIsMember(g, catID) {
		return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catID
	}
	if !g.Running {
		return "ERR|GROUP_NOT_RUNNING|组未启用: " + groupName
	}
	messages := s.Store.ReadMessages(g.Name, after, limit)
	// [段1] 消息行——#seq [类型] 发方 → 收方 · 正文（单行化，不截断）
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		from := ""
		if msg.From != "" {
			from = s.resolveCatDisplayName(msg.From)
		}
		to := ""
		if msg.To != "" {
			to = s.resolveCatDisplayName(msg.To)
		}
		route := from
		if to != "" {
			route = from + " → " + to
		}
		line := "#" + strconv.FormatInt(msg.Seq, 10) + " [" + groupLabelOf(msg.Type) + "] " + route + " · " + groupFlatten(msg.Text)
		if msg.Refs != "" {
			line += "（关联 " + msg.Refs + "）"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "（无消息）")
	}
	// [段2] 专有字段 + 摘要行
	extras := [][2]any{{"lastSeq", g.LastSeq}}
	summary := g.Name + " | " + strconv.Itoa(len(messages)) + " 条 · 最新 #" + strconv.FormatInt(g.LastSeq, 10)
	return groupBuildReply("group-inbox", g.Name, len(messages), extras, summary, lines)
}

// Say 发讨论消息（工具名 group-post）——只写组消息流，不投递、不唤醒；无 @ 目标参数。
func (s *hostGroupService) Say(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	text := ""
	if argsJSON != "" {
		var anyValue any
		if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
			return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if obj, isObj := anyValue.(map[string]any); isObj {
			text = groupJSONString(obj, "text")
		}
	}
	g := s.Store.GetGroup(groupName)
	if g == nil {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + groupName
	}
	if !groupIsMember(g, catID) {
		return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catID
	}
	if !g.Running {
		return "ERR|GROUP_NOT_RUNNING|组未启用: " + groupName
	}
	result := s.Store.AppendMessage(g.Name, catID, "note", text, "", "")
	if !strings.HasPrefix(result, "ok|") {
		return result
	}
	seq := groupParseSeq(result)
	extras := [][2]any{{"seq", seq}}
	summary := g.Name + " | 已记 #" + strconv.FormatInt(seq, 10)
	return groupBuildReply("group-post", g.Name, -1, extras, summary, nil)
}

// Send 派单（工具名 group-delegate）——写组消息 + 唤醒目标猫（同一方法内顺序完成）。
func (s *hostGroupService) Send(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	text := ""
	to := ""
	refs := ""
	notify := false
	if argsJSON != "" {
		var anyValue any
		if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
			return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if obj, isObj := anyValue.(map[string]any); isObj {
			text = groupJSONString(obj, "text")
			to = groupJSONString(obj, "to")
			refs = groupJSONString(obj, "refs")
			notify = groupJSONBool(obj, "notify")
		}
	}
	g := s.Store.GetGroup(groupName)
	if g == nil {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + groupName
	}
	if !g.Running {
		return "ERR|GROUP_NOT_RUNNING|组未启用: " + groupName
	}
	if !groupIsAdmin(g, catID) {
		return "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: " + catID
	}
	if to == "" {
		return "ERR|BAD_ARGS|派单必须指定 to（@ 目标猫）"
	}
	if !groupIsMember(g, to) {
		return "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: " + to
	}
	result := s.Store.AppendMessage(g.Name, catID, "delegation", text, refs, to)
	if !strings.HasPrefix(result, "ok|") {
		return result
	}
	seq := groupParseSeq(result)
	target := s.findDeliveryCat(to)
	if target == nil {
		return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + to + "（消息已入组 #" + strconv.FormatInt(seq, 10) + "）"
	}
	content := "[来自组:" + g.Name + "]\n" + text
	origin := "group:" + g.Name + ":" + strconv.FormatInt(seq, 10) + ":delegation"
	if notify {
		// 显式通知（方案丙）——origin 第五段 = 派单方猫 key；目标轮末回执产生后由宿主投递回它
		origin += ":" + catID
	}
	if !target.PostUser(content, "user", "", origin) {
		return "ERR|GROUP_DELIVER_FAIL|投递未受理: " + to + "（消息已入组 #" + strconv.FormatInt(seq, 10) + "）"
	}
	toName := s.resolveCatDisplayName(to)
	extras := [][2]any{{"seq", seq}, {"to", to}}
	summary := g.Name + " | 已派单 → " + toName + " · #" + strconv.FormatInt(seq, 10)
	return groupBuildReply("group-delegate", g.Name, -1, extras, summary, nil)
}

// NewSession 重建会话（group-newsession）——管理员发起；目标须成员且运行中；
// 管理员不得重建另一名管理员（单向数据流）；目标是调用者自己时挂起到轮末。
func (s *hostGroupService) NewSession(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	to := ""
	reason := ""
	first := ""
	if argsJSON != "" {
		var anyValue any
		if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
			return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if obj, isObj := anyValue.(map[string]any); isObj {
			to = strings.TrimSpace(groupJSONString(obj, "to"))
			reason = strings.TrimSpace(groupJSONString(obj, "reason"))
			first = groupJSONString(obj, "firstMessage")
		}
	}
	g := s.Store.GetGroup(groupName)
	if g == nil {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + groupName
	}
	if !g.Running {
		return "ERR|GROUP_NOT_RUNNING|组未启用: " + groupName
	}
	if !groupIsAdmin(g, catID) {
		return "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: " + catID
	}
	if to == "" {
		return "ERR|BAD_ARGS|重建会话必须指定 to（@ 目标猫）"
	}
	if !groupIsMember(g, to) {
		return "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: " + to
	}
	// 单向数据流：管理员不得重建另一名管理员——只能重建自己或非管理成员
	if to != catID && groupIsAdmin(g, to) {
		return "ERR|GROUP_TARGET_IS_ADMIN|管理员不得重建另一名管理员（仅可重建自己或非管理成员）: " + to
	}
	if reason == "" {
		return "ERR|BAD_ARGS|重建会话必须给出 reason（新会话唯一的上下文说明）"
	}
	if s.findDeliveryCat(to) == nil {
		return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + to
	}
	noteText := "[系统] 请求重建会话 → " + to + " | 原因 " + reason
	if first != "" {
		noteText += " | 初始指令 " + strconv.Itoa(utf16Length(first)) + " 字"
	}
	appended := s.Store.AppendMessage(g.Name, catID, "note", noteText, "", "")
	if !strings.HasPrefix(appended, "ok|") {
		return appended
	}
	seq := groupParseSeq(appended)
	origin := "group:" + g.Name + ":" + strconv.FormatInt(seq, 10) + ":delegation"
	if first == "" {
		origin = ""
	}
	self := to == catID
	if self {
		router := s.router()
		if router == nil {
			return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪"
		}
		if !router.requestSessionRebuild(first, origin) {
			return "ERR|GROUP_REBUILD_PENDING|本会话已有挂起的重建请求"
		}
	} else {
		result := s.rebuildCatSession(to, first, origin)
		if !strings.HasPrefix(result, "ok|") {
			return result
		}
	}
	mode := "rebuilt"
	if self {
		mode = "queued"
	}
	toName := s.resolveCatDisplayName(to)
	extras := [][2]any{{"seq", seq}, {"to", to}, {"mode", mode}}
	summary := g.Name + " | 已重建 → " + toName + " · #" + strconv.FormatInt(seq, 10)
	if self {
		summary = g.Name + " | 已排队 → " + toName + " · #" + strconv.FormatInt(seq, 10)
	}
	return groupBuildReply("group-newsession", g.Name, -1, extras, summary, nil)
}

// CatInfo 单猫运行态统计（group-catinfo）——组成员可读任一成员；与 cat.info 单条同源，不另造投影。
func (s *hostGroupService) CatInfo(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	target, targetErr := s.groupReadTarget(argsJSON, groupName, catID, false, false)
	if targetErr != "" {
		return targetErr
	}
	text := s.handleCatInfoOne(target)
	if strings.HasPrefix(text, "ERR|") {
		return text
	}
	extras := [][2]any{{"cat", target}}
	body := []string{groupFlatten(text)}
	return groupBuildReply("group-catinfo", groupName, -1, extras, "运行态："+s.resolveCatDisplayName(target)+"（"+target+"）", body)
}

// CatCfgGet 每猫配置读取（group-catcfg-get）——组成员可读任一成员；与 cat.cfg.get 同源（cat.cfg 无密钥字段，不设掩码）。
func (s *hostGroupService) CatCfgGet(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	target, targetErr := s.groupReadTarget(argsJSON, groupName, catID, false, false)
	if targetErr != "" {
		return targetErr
	}
	router := s.router()
	if router == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪"
	}
	text := router.catCfgGet(target)
	if !strings.HasPrefix(text, "cat.cfg | ") {
		return "ERR|GROUP_CATCFG_GET_FAIL|" + groupFlatten(text)
	}
	body := make([]string, 0, 16)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			body = append(body, line)
		}
	}
	return groupBuildReply("group-catcfg-get", groupName, -1, nil, "配置全量："+target, body)
}

// CatCfgSet 每猫配置写入（group-catcfg-set）——仅组管理员；目标不含调用者自己。
//
// 字段白名单与校验由 cat.cfg.set 单点承载（13 字段全开，等价默认会话权利）。
func (s *hostGroupService) CatCfgSet(argsJSON string) string {
	groupName, catID, errText := groupReadCommon(argsJSON)
	if errText != "" {
		return errText
	}
	target, targetErr := s.groupReadTarget(argsJSON, groupName, catID, true, true)
	if targetErr != "" {
		return targetErr
	}
	field, value, parseErr := groupReadFieldValue(argsJSON)
	if parseErr != "" {
		return parseErr
	}
	router := s.router()
	if router == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪"
	}
	// [段1] 写入——与 cat.cfg.set 单点同源（字段校验 / 全量写回 / catcfg.apply 入队 / 写后读回）
	text := router.catCfgSet(target + " " + field + " " + value)
	// [段2] 成败判据——成功路径以「已更新」收尾（cat.cfg.set 段3），失败路径不带该词
	if !strings.Contains(text, " 已更新") {
		return "ERR|GROUP_CATCFG_SET_FAIL|" + groupFlatten(text)
	}
	extras := [][2]any{{"cat", target}, {"field", field}}
	body := []string{groupFlatten(text)}
	return groupBuildReply("group-catcfg-set", groupName, -1, extras, "配置已写入："+target+" / "+field, body)
}

// groupReadTarget 目标猫解析——组内成员精确匹配（不把组 key 直接喂给 cat.cfg 寻址，防 id 前缀误配）。
//
// requireAdmin = 须组管理员（写面）；denySelf = 拒绝目标是调用者自己（改自己请由人操作）。
func (s *hostGroupService) groupReadTarget(argsJSON, groupName, catID string, requireAdmin, denySelf bool) (string, string) {
	g := s.Store.GetGroup(groupName)
	if g == nil {
		return "", "ERR|GROUP_NOT_FOUND|组不存在: " + groupName
	}
	if !groupIsMember(g, catID) {
		return "", "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catID
	}
	if !g.Running {
		return "", "ERR|GROUP_NOT_RUNNING|组未启用: " + groupName
	}
	if requireAdmin && !groupIsAdmin(g, catID) {
		return "", "ERR|GROUP_NOT_ADMIN|调用者不是组管理员: " + catID
	}
	wanted := ""
	if argsJSON != "" {
		var anyValue any
		if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
			return "", "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if obj, isObj := anyValue.(map[string]any); isObj {
			wanted = strings.TrimSpace(groupJSONString(obj, "cat"))
		}
	}
	if wanted == "" {
		return "", "ERR|BAD_ARGS|缺少参数 cat"
	}
	// [段1] 成员名单即授权边界——精确匹配，不走前缀
	if !groupContainsKey(g.MemberKeys, wanted) {
		return "", "ERR|GROUP_TARGET_NOT_MEMBER|目标不在组内: " + wanted
	}
	if denySelf && wanted == catID {
		return "", "ERR|GROUP_TARGET_SELF|不能修改自己的配置——改自己请由人操作"
	}
	return wanted, ""
}

// groupReadFieldValue 字段与值读取——field / value 必填（value 可为空串 = 清空该字段，透传给 cat.cfg.set）。
func groupReadFieldValue(argsJSON string) (string, string, string) {
	field := ""
	value := ""
	if argsJSON != "" {
		var anyValue any
		if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
			return "", "", "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if obj, isObj := anyValue.(map[string]any); isObj {
			field = strings.TrimSpace(groupJSONString(obj, "field"))
			value = groupJSONString(obj, "value")
		}
	}
	if field == "" {
		return "", "", "ERR|BAD_ARGS|缺少参数 field"
	}
	return field, value, ""
}

// resolveCatDisplayName 猫显示名现值——cat.cfg 的 displayName（缺失 / 读失败 = 空串）。
func (s *hostGroupService) resolveCatDisplayName(catKey string) string {
	router := s.router()
	if router == nil || catKey == "" {
		return ""
	}
	cfg, err := config.LoadCatCfg(filepath.Join(router.DataRoot, "sessions", catKey, "cat.cfg"))
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.DisplayName
}

// findDeliveryCat 投递寻址——单猫形态：majordomo / 会话 id / 显示名命中即默认会话；其余 nil。
//
// 对齐 C# CatHostService.FindDeliveryCat 的特殊会话薄壳分支（多猫注册表归 d-1）。
func (s *hostGroupService) findDeliveryCat(key string) *session.Session {
	router := s.router()
	if router == nil || router.Session == nil || key == "" {
		return nil
	}
	if key == "majordomo" || key == router.CatID || key == router.Session.DisplayNameSnapshot() {
		return router.Session
	}
	return nil
}

// handleCatInfoOne 单猫运行态统计——与 cat.info 单条同源（只把「全猫」范围收到「一只」）。
//
// 回执自带 A214 头（ok / tool / cat / running / port）+ 摘要行 + 条目 JSON；调用方（group.catinfo）整段压平进正文。
func (s *hostGroupService) handleCatInfoOne(key string) string {
	router := s.router()
	if router == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪"
	}
	if key == "" {
		return "ERR|BAD_ARGS|缺少猫寻址键"
	}
	if key != "majordomo" && key != router.CatID {
		return "ERR|CAT_NOT_FOUND|未找到猫: " + key
	}
	// [段1] 头行——运行面字段（会话观测字段随正文条目 JSON 全量给出）
	id := "majordomo"
	name := "majordomo"
	running := router.Running
	port := router.Port
	var head strings.Builder
	head.WriteString(`{"ok":true,"tool":"group-catinfo","cat":`)
	head.WriteString(bricks.JSONRelaxedString(id))
	head.WriteString(`,"running":`)
	if running {
		head.WriteString("true")
	} else {
		head.WriteString("false")
	}
	head.WriteString(`,"port":`)
	head.WriteString(strconv.Itoa(port))
	head.WriteString("}")
	runningText := "未运行"
	if running {
		runningText = "运行中"
	}
	// [段2] 正文——单猫条目（与 cat.info 的 cats 元素同构）
	entry := router.buildCatInfoEntry(name)
	var sb strings.Builder
	sb.WriteString(head.String())
	sb.WriteString("\n")
	sb.WriteString("运行态：" + name + "（" + id + "）| " + runningText + " | 端口 " + strconv.Itoa(port))
	sb.WriteString("\n")
	sb.WriteString(groupCompactJSON(entry))
	return sb.String()
}

// rebuildCatSession 跨猫会话重建——目标须空闲；供 Group 工具面与指令面共用（单猫形态目标恒 majordomo）。
func (s *hostGroupService) rebuildCatSession(key, firstMessage, origin string) string {
	router := s.router()
	if router == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪"
	}
	return router.rebuildCatSession(key, firstMessage, origin)
}

// groupDefaultLimit 单次拉取默认条数（对齐 C# GroupService.DefaultLimit）。
const groupDefaultLimit = 100

// groupReadCommon 公共参数读取——group（必填）+ catId（宿主注入保留键，缺则无法判权限）。
func groupReadCommon(argsJSON string) (string, string, string) {
	if argsJSON == "" {
		return "", "", "ERR|BAD_ARGS|工具参数 JSON 为空"
	}
	var anyValue any
	if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
		return "", "", "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
	}
	obj, isObj := anyValue.(map[string]any)
	if !isObj {
		return "", "", "ERR|BAD_ARGS|参数必须是 JSON 对象"
	}
	groupName := strings.TrimSpace(groupJSONString(obj, "group"))
	catID := strings.TrimSpace(groupJSONString(obj, "catId"))
	if groupName == "" {
		return "", "", "ERR|BAD_ARGS|缺少参数 group"
	}
	if catID == "" {
		return "", "", "ERR|GROUP_NO_CALLER|缺少调用者标识（catId）——无法判定权限"
	}
	return groupName, catID, ""
}

// groupBuildReply A214 回执构造——首行元数据头（ok / tool / target / items + 专有字段）+ 摘要行 + 正文行。
//
// 头行转义口径 = C# JsonUtil.Serialize（中文直出）；target 空省略、items 负值省略。
func groupBuildReply(tool, target string, items int, extras [][2]any, summary string, body []string) string {
	var b strings.Builder
	b.WriteString(`{"ok":true,"tool":`)
	b.WriteString(bricks.JSONRelaxedString(tool))
	if target != "" {
		b.WriteString(`,"target":`)
		b.WriteString(bricks.JSONRelaxedString(target))
	}
	if items >= 0 {
		b.WriteString(`,"items":`)
		b.WriteString(strconv.Itoa(items))
	}
	for _, kv := range extras {
		b.WriteString(",")
		b.WriteString(bricks.JSONRelaxedString(kv[0].(string)))
		b.WriteString(":")
		b.WriteString(groupJSONValue(kv[1]))
	}
	b.WriteString("}")
	b.WriteString("\n")
	b.WriteString(summary)
	for _, line := range body {
		b.WriteString("\n")
		b.WriteString(line)
	}
	return b.String()
}

// groupJSONValue 头字段值编码——bool / int / int64 / string 四态（其它按空串）。
func groupJSONValue(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return bricks.JSONRelaxedString(v)
	}
	return bricks.JSONRelaxedString("")
}

// groupCompactJSON 紧凑单行 JSON 序列化——中文直出（对齐 C# JsonUtil.Serialize）。
func groupCompactJSON(value any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "null"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// groupFlatten 正文单行化——换行压平为空格（正文行式定界不被破坏）。
func groupFlatten(text string) string {
	if text == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
}

// groupLabelOf 消息类型中文标签——派单 / 回执 / 发言。
func groupLabelOf(msgType string) string {
	if msgType == "delegation" {
		return "派单"
	}
	if msgType == "receipt" {
		return "回执"
	}
	return "发言"
}

// groupIsMember 成员判定（Ordinal 精确匹配）。
func groupIsMember(g *group.Summary, key string) bool {
	if g == nil || key == "" {
		return false
	}
	return groupContainsKey(g.MemberKeys, key)
}

// groupIsAdmin 管理员判定——显式 adminKeys 名单（空 = 无人可派单，失败可见）。
func groupIsAdmin(g *group.Summary, key string) bool {
	if g == nil || key == "" {
		return false
	}
	return groupContainsKey(g.AdminKeys, key)
}

// groupIsSubscriber 订阅者判定——显式 subscriberKeys 名单（空 = 不自动转发）。
func groupIsSubscriber(g *group.Summary, key string) bool {
	if g == nil || key == "" {
		return false
	}
	return groupContainsKey(g.SubscriberKeys, key)
}

// groupContainsKey 名单精确匹配。
func groupContainsKey(keys []string, key string) bool {
	for _, item := range keys {
		if item == key {
			return true
		}
	}
	return false
}

// groupParseSeq 解析 AppendMessage 回执中的 seq（ok|组名|seq=N）。
func groupParseSeq(result string) int64 {
	pos := strings.LastIndex(result, "seq=")
	if pos < 0 {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(result[pos+4:]), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// groupJSONString JSON 字符串属性读取——缺失 / 非字符串返回空串。
func groupJSONString(obj map[string]any, name string) string {
	raw, exists := obj[name]
	if !exists {
		return ""
	}
	text, ok := raw.(string)
	if !ok {
		return ""
	}
	return text
}

// groupJSONBool JSON 布尔属性读取——缺失 / 非 true 一律 false（notify 缺省不通知）。
func groupJSONBool(obj map[string]any, name string) bool {
	raw, exists := obj[name]
	if !exists {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}

// groupJSONInt64 JSON 数值属性读取——缺失 / 非数值返回 false。
func groupJSONInt64(obj map[string]any, name string) (int64, bool) {
	raw, exists := obj[name]
	if !exists {
		return 0, false
	}
	switch value := raw.(type) {
	case float64:
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// utf16Length 字符串的 UTF-16 码元长度（C# string.Length 同口径——非 BMP 计 2）。
func utf16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}
