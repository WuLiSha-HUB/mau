package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// timebackNewLine 是返回体拼接用的换行——对齐 C# Environment.NewLine（Windows 宿主为 CRLF）。
const timebackNewLine = "\r\n"

// 重启族工具名单——请求判定的单点真相源（timeback 暴毙黑名单共用同一清单）。
var restartToolNames = []string{"restart-full", "restart-incr", "restart-host"}

// TimebackScope 是 timeback 作用域——内存单对象（v1 不嵌套；宿主重启即失效）。
type TimebackScope struct {
	// ID 作用域编号（本猫内永久递增；归档 open/close 以 id 关联）
	ID int64
	// StartDeclIndex start 的 tool_calls 声明消息索引——锚点
	StartDeclIndex int
	// BackDeclIndex back 的 tool_calls 声明消息索引——回收区间上界
	BackDeclIndex int
	// StartAtMs 开锚时刻（Unix 毫秒）
	StartAtMs int64
	// Type 域类型
	Type string
	// Purpose 用途标签（≤48 字符）
	Purpose string
	// PendingFindings 待执行回收的带回载荷（nil = back 未发生）
	PendingFindings *string
	// EventCount 作用域内事件累计（think / 工具完成 / assistant 产出各计 1）
	EventCount int64
	// LastNotifyCount 上次状态自述时的计数水位
	LastNotifyCount int64
	// TokensAtOpen 开锚时的已知前文长度快照
	TokensAtOpen int64
	// PendingReleased 本次回收的释放条数（back 时刻预算）
	PendingReleased int
	// WriteLog 本域工具台账（工具名 · 目标标识 · 成败；含只读与被拒）
	WriteLog []string
	// CardPosted 域规范卡是否已注入
	CardPosted bool
}

// Timeback 是 timeback 上下文作用域控制器——开锚 / 回收 / 台账 / 归档。
//
// 语义：**锚点 = start 的 tool_calls 声明本身；回收 = back 的返回值**。
// 前文形态：[start 声明][start 结果] [查证过程…] [域内人工输入…] [back 声明][back 结果 = findings]
// 回收动作：删除「start 结果之后、back 声明之前」的全部消息——两次调用对与结论原样保留；
// **域内人工输入保留**（按原序落回锚定结果之后、back 声明之前）；系统注入随域删除。
//
// 依赖面全部以函数字段注入（messages / 落盘 / token 读数 / 系统注入口），控制器本身零 IO。
type Timeback struct {
	// CatKey 猫 key
	CatKey string
	// Archive 归档实例（nil = 归档不可用，功能不阻断）
	Archive *TimebackArchive
	// Messages 读当前前文消息
	Messages func() []Message
	// ReplaceMessages 内存替换前文（区间删除）
	ReplaceMessages func([]Message)
	// Persist 前文落盘重写（append-only 的合法例外）
	Persist func([]Message)
	// Tokens 已知最新前文长度（请求级真实 usage 值）
	Tokens func() int64
	// PostSystem 系统注入口（域规范卡 / 状态自述）
	PostSystem func(kind, content, imagesJSON string) string
	// InfoSnapshot 归档第二行的现场记录（nil = 该行不写）
	InfoSnapshot func() string
	// Browser 浏览器实例生命周期（nil = 未注入——浏览器工具按无服务口径；开锚预热 / 回收关闭）
	Browser BrowserLifecycle
	// scope 当前未闭合作用域（nil = 无作用域）
	scope *TimebackScope
}

// BrowserLifecycle 是浏览器实例生命周期窄接口（会话侧消费面——bricks.BrowserService 隐式实现）。
//
// 开锚预热（browser_vision 域）/ 域回收关闭——有头实例保活由实现方保证（人工登录窗口等待人的动作）。
type BrowserLifecycle interface {
	// Prepare 预热指定猫的实例（失败只记日志，不阻断）
	Prepare(catID string)
	// Close 关闭指定猫的实例（有头实例保活）
	Close(catID string)
}

// NewTimeback 建立控制器（无作用域）。
func NewTimeback(catKey string) *Timeback { return &Timeback{CatKey: catKey} }

// Active 是否存在未闭合作用域。
func (t *Timeback) Active() bool { return t.scope != nil }

// Scope 当前作用域（nil = 无）。
func (t *Timeback) Scope() *TimebackScope { return t.scope }

// tokensKnown 已知前文长度（钩子置空 = 0）。
func (t *Timeback) tokensKnown() int64 {
	if t.Tokens == nil {
		return 0
	}
	return t.Tokens()
}

// messages 当前前文（钩子置空 = 空）。
func (t *Timeback) messages() []Message {
	if t.Messages == nil {
		return nil
	}
	return t.Messages()
}

// ExecuteStart timeback-start 执行体——参数面（type / purpose 必填；未知参数拒绝）。
func (t *Timeback) ExecuteStart(argsJSON string) string {
	domainType := ""
	purpose := ""
	if strings.HasPrefix(argsJSON, "{") {
		fields, err := parseOrderedObject(argsJSON)
		if err != nil {
			return "ERR|TIMEBACK_ARGS|timeback-start 参数解析失败: " + err.Error()
		}
		for _, field := range fields {
			switch field.Key {
			case "catId":
			case "type":
				var s string
				if err := json.Unmarshal(field.Value, &s); err != nil {
					return "ERR|TIMEBACK_ARGS|type 需为字符串"
				}
				domainType = s
			case "purpose":
				var s string
				if err := json.Unmarshal(field.Value, &s); err != nil {
					return "ERR|TIMEBACK_ARGS|purpose 需为字符串"
				}
				purpose = s
			default:
				return "ERR|TIMEBACK_ARGS|未知参数: " + field.Key + "（支持 type / purpose）"
			}
		}
	}
	return t.Start(domainType, purpose)
}

// Start 登记作用域：锚点 = 本刻前文末条（本次 start 的 tool_calls 声明）。
//
// v1 未闭合前禁止再次 start。
func (t *Timeback) Start(domainType, purpose string) string {
	if t.scope != nil {
		return "ERR|TIMEBACK_NESTED|已有未闭合作用域 #" + itoa64(t.scope.ID) + "——先 back 再 start"
	}
	if !TimebackTypeValid(domainType) {
		message := "ERR|TIMEBACK_ARGS|start 需要合法 type（域类型）: " + TimebackTypeListText()
		if domainType != "" {
			message += "——收到「" + domainType + "」"
		}
		return message
	}
	if purpose == "" {
		return "ERR|TIMEBACK_ARGS|start 需要 purpose（用途标签）"
	}
	declIndex := len(t.messages()) - 1
	if declIndex < 0 {
		return "ERR|TIMEBACK_ANCHOR|前文为空——无锚点可记"
	}
	scope := &TimebackScope{
		StartDeclIndex: declIndex,
		StartAtMs:      nowMillis(),
		Type:           domainType,
		Purpose:        truncateRunes(purpose, 48),
		TokensAtOpen:   t.tokensKnown(),
	}
	if t.Archive != nil {
		scope.ID = t.Archive.NextID()
	}
	t.scope = scope
	logAt(LogModuleHost, 1, "timeback #"+itoa64(scope.ID)+" 开锚（锚点 "+strconv.Itoa(declIndex)+" / 用途 "+scope.Purpose+"）", LogCategoryTimeback)
	// 域级生命周期——browser_vision 开锚预热本猫浏览器实例（失败只记日志，不阻断开锚）
	if t.Browser != nil && TimebackNeedsBrowser(domainType) {
		t.Browser.Prepare(t.CatKey)
	}
	return metaHead("timeback-start", true, itoa64(scope.ID), -1,
		[]metaField{
			{"anchor", declIndex},
			{"type", scope.Type},
			{"purpose", scope.Purpose},
		}, "")
}

// ExecuteBack timeback-back 执行体——参数面（findings 必填；未知参数拒绝）。
func (t *Timeback) ExecuteBack(argsJSON string) string {
	findings := ""
	if strings.HasPrefix(argsJSON, "{") {
		fields, err := parseOrderedObject(argsJSON)
		if err != nil {
			return "ERR|TIMEBACK_ARGS|timeback-back 参数解析失败: " + err.Error()
		}
		for _, field := range fields {
			switch field.Key {
			case "catId":
			case "findings":
				var s string
				if err := json.Unmarshal(field.Value, &s); err != nil {
					return "ERR|TIMEBACK_ARGS|findings 需为字符串"
				}
				findings = s
			default:
				return "ERR|TIMEBACK_ARGS|未知参数: " + field.Key + "（支持 findings）"
			}
		}
	}
	return t.Back(findings)
}

// Back 校验后返回载荷（findings 即本次工具调用的返回值）；区间删除在批后段执行（Apply）。
func (t *Timeback) Back(findings string) string {
	if t.scope == nil {
		return "ERR|TIMEBACK_NO_SCOPE|无未闭合作用域——back 无对象"
	}
	if findings == "" {
		return "ERR|TIMEBACK_ARGS|back 需要 findings（带回载荷）"
	}
	t.scope.PendingFindings = &findings
	t.scope.BackDeclIndex = len(t.messages()) - 1
	t.scope.PendingReleased = t.countReleased(t.scope)
	tokensNow := t.tokensKnown()
	infoLine := "📉 释放 " + strconv.Itoa(t.scope.PendingReleased) + " 条前文 · 域内节约 " +
		itoa64(tokensNow-t.scope.TokensAtOpen) + "token · 工具使用 " +
		strconv.Itoa(len(t.scope.WriteLog)) + " 次"
	if len(t.scope.WriteLog) > 0 {
		infoLine += "（详见下表）"
	}
	body := infoLine + "\n" + t.buildWrites() + findings + timebackNewLine + timebackNewLine + TimebackReadback(t.scope.Type)
	return metaHead("timeback-back", true, itoa64(t.scope.ID), t.scope.PendingReleased,
		[]metaField{
			{"anchor", t.scope.StartDeclIndex},
			{"type", t.scope.Type},
			{"purpose", t.scope.Purpose},
			{"tokens", tokensNow},
			{"grew", tokensNow - t.scope.TokensAtOpen},
			{"tools", len(t.scope.WriteLog)},
		}, body)
}

// countReleased 释放条数预算——按当前前文算「[start 结果之后 .. back 声明之前]」的消息条数。
func (t *Timeback) countReleased(scope *TimebackScope) int {
	all := t.messages()
	keepEnd := scope.StartDeclIndex
	for keepEnd+1 < len(all) && all[keepEnd+1].Role == RoleTool {
		keepEnd++
	}
	from := keepEnd + 1
	to := scope.BackDeclIndex - 1
	if from > to || from >= len(all) {
		return 0
	}
	if to >= len(all) {
		to = len(all) - 1
	}
	keptUsers := 0
	for i := from; i <= to && i < len(all); i++ {
		if IsHumanUserMessage(all[i]) {
			keptUsers++
		}
	}
	return to - from + 1 - keptUsers
}

// Apply 批后回收执行——工具批结果全部回填后调用。
//
// ① 前文删除「start 结果之后、back 声明之前」的全部消息（两次调用对与结论保留）；back 声明思考换占位句
// ② 归档一次成档（retained 消息与预算同源可对账）→ 关闭作用域。返回实际删除条数。
func (t *Timeback) Apply() int {
	scope := t.scope
	if scope == nil || scope.PendingFindings == nil {
		return 0
	}
	all := t.messages()
	keepEnd := scope.StartDeclIndex
	for keepEnd+1 < len(all) && all[keepEnd+1].Role == RoleTool {
		keepEnd++
	}
	from := keepEnd + 1
	to := scope.BackDeclIndex - 1
	removed := 0
	keptUsers := 0
	removedMessages := make([]Message, 0, 8)
	infoJSON := t.collectInfo()
	nowMs := nowMillis()
	seconds := (nowMs - scope.StartAtMs) / 1000
	if seconds < 0 {
		seconds = 0
	}
	if from <= to && from < len(all) {
		if to >= len(all) {
			to = len(all) - 1
		}
		secondStart := scope.BackDeclIndex
		if secondStart < keepEnd+1 {
			secondStart = keepEnd + 1
		}
		keep := make([]Message, 0, len(all))
		for i := 0; i <= keepEnd && i < len(all); i++ {
			keep = append(keep, all[i])
		}
		// 域内人工输入保留——按原序插在锚定结果之后、back 声明之前；系统注入随域删除
		for i := from; i <= to && i < len(all); i++ {
			if !IsHumanUserMessage(all[i]) {
				continue
			}
			keep = append(keep, all[i])
			keptUsers++
		}
		for i := secondStart; i < len(all); i++ {
			if i == scope.BackDeclIndex {
				keep = append(keep, withBackReasoningCleared(all[i]))
				continue
			}
			keep = append(keep, all[i])
		}
		// 被删前文快照——归档正文（保留下来的用户消息仍在会话里，不入被删档）
		for i := from; i <= to && i < len(all); i++ {
			if IsHumanUserMessage(all[i]) {
				continue
			}
			removedMessages = append(removedMessages, all[i])
		}
		removed = len(all) - len(keep)
		if t.ReplaceMessages != nil {
			t.ReplaceMessages(keep)
			// 落盘写的是**结构修复后**的前文（C# 同口径：ReplaceMessages 之后再取 GetMessages 落盘）
			if t.Persist != nil {
				t.Persist(t.messages())
			}
		}
	} else {
		if from <= to {
			logAt(LogModuleHost, 2, "timeback 回收区间越界（from "+strconv.Itoa(from)+" / to "+strconv.Itoa(to)+" / len "+strconv.Itoa(len(all))+"）——本次未删", LogCategoryTimeback)
		}
		// 未走删除路径（同批开收 / 区间为空 / 索引越界）时索引未变，就地净化 back 声明思考
		current := t.messages()
		if scope.BackDeclIndex >= 0 && scope.BackDeclIndex < len(current) {
			cleared := withBackReasoningCleared(current[scope.BackDeclIndex])
			if cleared.ReasoningContent != current[scope.BackDeclIndex].ReasoningContent {
				current[scope.BackDeclIndex] = cleared
				if t.ReplaceMessages != nil {
					t.ReplaceMessages(current)
					if t.Persist != nil {
						t.Persist(t.messages())
					}
				}
			}
		}
	}
	// 归档——一次回收一个文件（首行 meta + 该猫 info 快照 + 被删前文消息）
	archived := t.writeArchive(scope, removed, keptUsers, removedMessages, infoJSON, nowMs, seconds)
	// 释放条数对账——预算 vs 实际删除数（归档记实际；不一致必须出声）
	if removed != scope.PendingReleased {
		logAt(LogModuleHost, 2, "timeback #"+itoa64(scope.ID)+" 释放条数对账不一致（预算 "+
			strconv.Itoa(scope.PendingReleased)+" / 实际 "+strconv.Itoa(removed)+"）", LogCategoryTimeback)
	}
	t.scope = nil
	keptNote := ""
	if keptUsers > 0 {
		keptNote = " · 保留人工输入 " + strconv.Itoa(keptUsers) + " 条"
	}
	logAt(LogModuleHost, 1, "timeback #"+itoa64(scope.ID)+" 已回收 "+strconv.Itoa(removed)+" 条"+keptNote+
		"（"+itoa64(seconds)+" 秒 / 锚点 "+strconv.Itoa(scope.StartDeclIndex)+"）", LogCategoryTimeback)
	if !archived {
		logAt(LogModuleHost, 2, "timeback #"+itoa64(scope.ID)+" 归档落档失败（运行不受影响）", LogCategoryTimeback)
	}
	// 域级生命周期——回收即关本猫浏览器实例（有头实例由实现方保活）
	if t.Browser != nil {
		t.Browser.Close(t.CatKey)
	}
	return removed
}

// writeArchive 归档写出一一一次回收一个文件（首行 meta + info 快照 + 被删前文消息）。
func (t *Timeback) writeArchive(scope *TimebackScope, removed, keptUsers int, removedMessages []Message, infoJSON string, nowMs, seconds int64) bool {
	if t.Archive == nil {
		return false
	}
	record := &TimebackScopeRecord{
		ID:        scope.ID,
		CatKey:    t.CatKey,
		Type:      scope.Type,
		Purpose:   scope.Purpose,
		Anchor:    scope.StartDeclIndex,
		StartAt:   scope.StartAtMs,
		BackAt:    nowMs,
		Seconds:   seconds,
		N:         removed,
		KeptUsers: keptUsers,
		Tokens:    t.tokensKnown(),
		Grew:      t.tokensKnown() - scope.TokensAtOpen,
		Released:  scope.PendingReleased,
		Findings:  derefString(scope.PendingFindings),
		Writes:    scope.WriteLog,
	}
	path, ok := t.Archive.WriteScopeFile(record, infoJSON, removedMessages)
	if ok {
		logAt(LogModuleHost, 1, "timeback #"+itoa64(scope.ID)+" 归档落档: "+path, LogCategoryTimeback)
	}
	return ok
}

// collectInfo 采集 info 快照——provider 未接线 / 采集失败 = 空串，不阻断回收。
func (t *Timeback) collectInfo() string {
	if t.InfoSnapshot == nil {
		return ""
	}
	return t.InfoSnapshot()
}

// NoteEvent 作用域内事件计数——think / 工具完成 / assistant 产出各计 1。
func (t *Timeback) NoteEvent() {
	if t.scope == nil {
		return
	}
	t.scope.EventCount++
}

// TimebackNoticeStep 状态自述步长——作用域内每累计 25 个事件注入一条提示。
const TimebackNoticeStep = 25

// FlushNotice 状态提示注入——批后段调用：累计满 25 个事件则追加一条 systemauto 提示。
func (t *Timeback) FlushNotice() {
	scope := t.scope
	if scope == nil {
		return
	}
	if scope.EventCount-scope.LastNotifyCount < TimebackNoticeStep {
		return
	}
	seconds := (nowMillis() - scope.StartAtMs) / 1000
	if seconds < 0 {
		seconds = 0
	}
	scope.LastNotifyCount = scope.EventCount
	text := "（系统自动 · timeback #" + itoa64(scope.ID) + "）你处在 timeback 中，已经历【" +
		itoa64(scope.EventCount) + "】条前文条目（已用 " + itoa64(seconds) + " 秒）"
	if t.PostSystem == nil {
		return
	}
	posted := t.PostSystem(SysKindSystemAuto, text, "")
	if strings.HasPrefix(posted, "ERR|") {
		logAt(LogModuleHost, 2, "timeback 状态提示未受理: "+posted, LogCategoryTimeback)
		return
	}
	logAt(LogModuleHost, 1, "timeback #"+itoa64(scope.ID)+" 状态提示注入（累计 "+itoa64(scope.EventCount)+
		" 事件 / 已用 "+itoa64(seconds)+" 秒）", LogCategoryTimeback)
}

// PostCard 域规范卡注入——批后段调用（Apply 之后）。
//
// 零动作：无作用域（同批开收）· 已注入 · back 已 pending（区间为空，注入即残留在区间外）。
func (t *Timeback) PostCard() {
	scope := t.scope
	if scope == nil || scope.CardPosted || scope.PendingFindings != nil {
		return
	}
	scope.CardPosted = true
	if t.PostSystem == nil {
		return
	}
	text := TimebackCard(scope.Type, scope.Purpose, scope.ID, scope.StartDeclIndex)
	posted := t.PostSystem(SysKindSystemAuto, text, "")
	if strings.HasPrefix(posted, "ERR|") {
		logAt(LogModuleHost, 2, "timeback #"+itoa64(scope.ID)+" 域规范卡未受理: "+posted, LogCategoryTimeback)
		return
	}
	logAt(LogModuleHost, 1, "timeback #"+itoa64(scope.ID)+" 域规范卡注入（"+scope.Type+"）", LogCategoryTimeback)
}

// TimebackWriteLogInlineLimit 台账回执内联上限——超出后只列前 N 条 + 归档指针（全文进 jsonl）。
const TimebackWriteLogInlineLimit = 20

// NoteWrite 作用域内工具台账——工具结果回填时登记（全量：域内用过的每个工具都记）。
//
// 唯二例外是域机制自用两件（timeback-start / timeback-back——记它等于每条都带噪声）。
func (t *Timeback) NoteWrite(name, argsJSON, result string) {
	scope := t.scope
	if scope == nil {
		return
	}
	if name == "timeback-start" || name == "timeback-back" {
		return
	}
	scope.WriteLog = append(scope.WriteLog, name+" · "+TimebackWriteTarget(argsJSON)+" · "+TimebackToolStatus(result))
}

// buildWrites 本域工具台账文本——附于 findings 之前（超上限给计数 + 首 N 条 + 归档指针）。
func (t *Timeback) buildWrites() string {
	scope := t.scope
	if scope == nil || len(scope.WriteLog) == 0 {
		return ""
	}
	text := "[本域工具台账 · 宿主记录 · " + strconv.Itoa(len(scope.WriteLog)) + " 条 · 含只读与被拒]\n"
	shown := len(scope.WriteLog)
	if shown > TimebackWriteLogInlineLimit {
		shown = TimebackWriteLogInlineLimit
	}
	for i := 0; i < shown; i++ {
		text += strconv.Itoa(i+1) + ". " + scope.WriteLog[i] + "\n"
	}
	if shown < len(scope.WriteLog) {
		text += "…（余 " + strconv.Itoa(len(scope.WriteLog)-shown) + " 条见归档 · timeback #" +
			itoa64(scope.ID) + " · data:runtime/timeback）\n"
	}
	return text + "\n"
}

// TimebackToolStatus 台账状态列——OK / DENY / ERR / FAIL / FAIL(明细) / UNPARSED。
func TimebackToolStatus(result string) string {
	if result == "" {
		return "OK"
	}
	if strings.HasPrefix(result, "ERR|TIMEBACK_PROFILE") ||
		strings.HasPrefix(result, "ERR|TIMEBACK_LOCKED") ||
		strings.HasPrefix(result, "ERR|TOOL_FORBIDDEN") {
		return "DENY"
	}
	if strings.HasPrefix(result, "ERR|") || strings.HasPrefix(result, "ROLLED_BACK") {
		return "ERR"
	}
	if strings.HasPrefix(result, "{") {
		return jsonHeadStatus(result)
	}
	return "OK"
}

// jsonHeadStatus 台账状态列·JSON 头分支——只认前缀会把编译 / 跑测失败记成 OK（事实列说谎）。
//
// ok 缺失 / 解析失败出声 UNPARSED——不回落 OK。
func jsonHeadStatus(result string) string {
	head := result
	if idx := strings.IndexByte(result, '\n'); idx >= 0 {
		head = result[:idx]
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(head)), &obj); err != nil {
		return "UNPARSED"
	}
	okRaw, exists := obj["ok"]
	if !exists {
		return "UNPARSED"
	}
	var ok bool
	if err := json.Unmarshal(okRaw, &ok); err != nil {
		return "UNPARSED"
	}
	if ok {
		return "OK"
	}
	detail := jsonFailDetail(obj)
	if detail == "" {
		return "FAIL"
	}
	return "FAIL(" + detail + ")"
}

// jsonFailDetail 失败明细——从结构化头取可读计数（exit / errors / failedTests / failed）。
func jsonFailDetail(obj map[string]json.RawMessage) string {
	detail := ""
	exit := jsonHeadInt(obj, "exit")
	errors := jsonHeadInt(obj, "errors")
	failed := jsonHeadInt(obj, "failedTests")
	if failed <= 0 {
		failed = jsonHeadInt(obj, "failed")
	}
	if exit != 0 {
		detail = "exit=" + strconv.Itoa(exit)
	}
	if errors > 0 {
		detail = appendDetail(detail, "errors="+strconv.Itoa(errors))
	}
	if failed > 0 {
		detail = appendDetail(detail, "failed="+strconv.Itoa(failed))
	}
	return detail
}

// jsonHeadInt 计数取值——缺字段 / 非数字一律 0（计数只作展示）。
func jsonHeadInt(obj map[string]json.RawMessage, key string) int {
	raw, exists := obj[key]
	if !exists {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}

// appendDetail 明细追加——已有时以逗号连接。
func appendDetail(detail, item string) string {
	if detail == "" {
		return item
	}
	return detail + "," + item
}

// TimebackWriteTarget 台账目标标识——从参数约定键取首个非空值（路径类取文件名，标识类原样）。
func TimebackWriteTarget(argsJSON string) string {
	if argsJSON == "" {
		return "—"
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argsJSON), &obj); err != nil {
		return "—"
	}
	for _, key := range []string{"path", "dest", "src", "file", "proj", "dir", "class", "member"} {
		if value := rawString(obj, key); value != "" {
			return shortenTimebackTarget(value)
		}
	}
	// powershell——命令行本身即标识（外部通道 · 单行指令一次一条），取原文不截断
	if cmd := rawString(obj, "command"); cmd != "" {
		return cmd
	}
	return "—"
}

// shortenTimebackTarget 目标标识截短——取路径最后一段；超 48 字符截断。
func shortenTimebackTarget(value string) string {
	text := value
	cut := strings.LastIndex(text, "/")
	if cut2 := strings.LastIndex(text, "\\"); cut2 > cut {
		cut = cut2
	}
	if cut >= 0 && cut+1 < len(text) {
		text = text[cut+1:]
	}
	return truncateRunes(text, 48)
}

// ActiveSnapshot 活跃作用域快照——info timeback.active 数据源（无作用域 = nil）。
func (t *Timeback) ActiveSnapshot() map[string]any {
	scope := t.scope
	if scope == nil {
		return nil
	}
	return map[string]any{
		"id":      scope.ID,
		"type":    scope.Type,
		"purpose": scope.Purpose,
		"anchor":  scope.StartDeclIndex,
		"startAt": scope.StartAtMs,
		"events":  scope.EventCount,
	}
}

// TimebackBackReasoningPlaceholder back 声明思考占位句。
const TimebackBackReasoningPlaceholder = "原思考内容已因锚回收被清除"

// withBackReasoningCleared back 声明思考净化——把该条 assistant 消息的思考换成占位句（其余字段原样）。
func withBackReasoningCleared(msg Message) Message {
	if msg.Role != RoleAssistant || msg.ReasoningContent == "" {
		return msg
	}
	msg.ReasoningContent = TimebackBackReasoningPlaceholder
	return msg
}

// IsRestartTool 是否重启族工具。
func IsRestartTool(name string) bool { return containsName(restartToolNames, name) }

// IsTimebackBodyLocked 作用域内禁用判定——只拦「会让进程 / 作用域当场失效」的操作。
func IsTimebackBodyLocked(name string) bool {
	return IsRestartTool(name) || name == "host-reload"
}

// IsTimebackScopedTool 域限定工具判定——仅作用域内可用（域外拒绝）。
func IsTimebackScopedTool(name string) bool {
	if name == "image-inject" {
		return true
	}
	// 例外——browser-headful 是主干登录工具（域外专属），与其余 browser-* 方向相反
	if name == "browser-headful" {
		return false
	}
	return strings.HasPrefix(name, "browser-")
}

// IsTimebackProfileExempt 域类型白名单豁免——不归白名单管辖的工具（交各自机制报错）。
func IsTimebackProfileExempt(name string) bool {
	if name == "timeback-start" || name == "timeback-back" {
		return true
	}
	return name == "Note" || name == "sleep" || name == "timer"
}

// CheckTool 工具面门禁——返回空串表示放行，否则为 ERR| 回执文本。
//
// 判定序与 C# 工具批入口一致：暴毙黑名单 → 域专属 → 域类型白名单。
// 授权面（会话声明面）不在本表先验——它自会拒绝。
func (t *Timeback) CheckTool(name string) string {
	if t.scope != nil && IsTimebackBodyLocked(name) {
		return "ERR|TIMEBACK_LOCKED|timeback 作用域内禁止会让进程或作用域当场失效的操作（重启 / 热重载 / 部署）: " + name + "——先 back 回收"
	}
	if IsTimebackScopedTool(name) && t.scope == nil {
		return "ERR|TIMEBACK_REQUIRED|" + name + " 仅在 timeback 作用域内可用（非主干信息——用完回收）——先 start 开锚"
	}
	if t.scope != nil && !IsTimebackProfileExempt(name) && !TimebackAllows(t.scope.Type, name) {
		return "ERR|TIMEBACK_PROFILE|" + name + " 不在本域类型「" + t.scope.Type +
			"」的白名单内——域类型决定域内可用工具面；需用则先 back 回收、回主干调用"
	}
	return ""
}

// CheckBuiltinTool 内置工具锁定——Note / sleep / timer 在作用域内存活期一律拒绝。
func (t *Timeback) CheckBuiltinTool(name string) string {
	if t.scope != nil && (name == "Note" || name == "sleep" || name == "timer") {
		return "ERR|TIMEBACK_LOCKED|timeback 作用域内 " + name + " 不可用——先 back 回收再调用"
	}
	return ""
}

// nowMillis 当前时刻（Unix 毫秒）。
func nowMillis() int64 { return time.Now().UnixMilli() }

// truncateRunes 按字符截断（C# Substring 语义的字符面等价）。
func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// derefString 解引用字符串指针（nil = 空串）。
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
