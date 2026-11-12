// Package group 复现 C# GroupStore 的组数据面——Data/group/<组名>/{group.json, messages.jsonl}。
//
// 语义与 C# `CatHome4.Core/GroupStore.cs` 逐条对齐：组配置 / 消息流 / 订阅游标 / 启用态统计；
// 落盘范式同 delays.json——变更即原子写、重启扫描续接。
// 线程模型：全部操作锁内完成（HTTP 线程 / 主线程 / 组宿主共用）。
package group

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// 组名与读取上限（对齐 C# GroupStore 常量）。
const (
	// MaxNameLength 组名长度上限
	MaxNameLength = 32
	// MaxReadLimit 单次读消息条数上限
	MaxReadLimit = 500
	// DefaultReadLimit 单次读消息条数缺省值
	DefaultReadLimit = 100
	// DefaultSubscribeIdleSec 订阅推送静默阈值缺省值（秒）
	DefaultSubscribeIdleSec = 300
)

// Summary 是组投影——配置 + 消息流统计（不落盘，派生自 group.json 与 messages.jsonl）。
type Summary struct {
	// Name 组名——唯一键（目录名）
	Name string
	// MemberKeys 成员猫 key 列表（= Data/sessions/<key> 目录名）
	MemberKeys []string
	// AdminKeys 管理员猫 key 列表（显式配置；@ 派单权限来源）
	AdminKeys []string
	// SubscriberKeys 订阅自动转发的猫 key 列表（成员子集）
	SubscriberKeys []string
	// Project 项目绑定——自由文本（可空）
	Project string
	// CreatedAt 创建时刻——Unix 毫秒
	CreatedAt int64
	// Layer 层级——预留字段（当前未消费）
	Layer string
	// Enabled 启用意图——false = 组停用
	Enabled bool
	// Running 运行态——true = 已加载并在运行（工具面可用）
	Running bool
	// LastSeq 组内最大消息序号——0 = 无消息
	LastSeq int64
	// MsgCount 消息条数
	MsgCount int
	// LastAt 最新消息时刻——Unix 毫秒（0 = 无消息）
	LastAt int64
	// SubscribeIdleSec 订阅推送静默阈值——秒；0 = 未设置（按默认 300 秒）
	SubscribeIdleSec int
	// SubscribeCursors 订阅者未读游标——猫 key → 已推送到的 seq（键大小写不敏感）
	SubscribeCursors map[string]int64
}

// Message 是组消息——messages.jsonl 一行一条。
type Message struct {
	// Seq 组内单调递增序号
	Seq int64
	// From 发送方猫 key（空 = 系统/管理员）
	From string
	// To @ 目标猫 key（空 = 不指向具体猫）
	To string
	// Type 消息类型——delegation / receipt / note
	Type string
	// Text 正文
	Text string
	// Ts 发送时刻——Unix 毫秒
	Ts int64
	// Refs 关联锚——单号 / 轮次（可空）
	Refs string
}

// persistedGroup 是 group.json 的落盘形态（键序对齐 C# WriteGroupFile 的 map 装配序）。
type persistedGroup struct {
	// Name 组名
	Name string `json:"name"`
	// MemberKeys 成员猫 key
	MemberKeys []string `json:"memberKeys"`
	// AdminKeys 管理员猫 key
	AdminKeys []string `json:"adminKeys"`
	// SubscriberKeys 订阅猫 key
	SubscriberKeys []string `json:"subscriberKeys"`
	// SubscribeIdleSec 订阅静默阈值（秒）
	SubscribeIdleSec int `json:"subscribeIdleSec"`
	// SubscribeCursors 订阅游标
	SubscribeCursors map[string]int64 `json:"subscribeCursors"`
	// Project 项目绑定
	Project string `json:"project"`
	// CreatedAt 创建时刻（Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
	// Layer 层级（预留）
	Layer string `json:"layer"`
	// Enabled 启用意图
	Enabled bool `json:"enabled"`
}

// persistedMessage 是 messages.jsonl 的单行形态（键序对齐 C# JsonUtil.Object 装配序）。
type persistedMessage struct {
	// Seq 序号
	Seq int64 `json:"seq"`
	// From 发送方
	From string `json:"from"`
	// To @ 目标
	To string `json:"to"`
	// Type 类型
	Type string `json:"type"`
	// Text 正文
	Text string `json:"text"`
	// Ts 时刻
	Ts int64 `json:"ts"`
	// Refs 关联锚
	Refs string `json:"refs"`
}

// Store 是组数据面——组缓存 + 组根目录 + 日志出口。
type Store struct {
	// mu 存储锁——全部读写锁内完成
	mu sync.Mutex
	// root 组根目录（空 = 仅内存，不落盘）
	root string
	// groups 组缓存——小写组名 → 组投影（大小写不敏感，避免同组两目录）
	groups map[string]*Summary
	// logf 日志出口（nil = 静默）
	logf func(level int, message, category string)
}

// New 建立组数据面（root = Data/group 绝对路径；空 = 仅内存）。
func New(root string, logf func(level int, message, category string)) *Store {
	return &Store{root: root, groups: make(map[string]*Summary), logf: logf}
}

// log 记一条组面日志（nil 出口静默丢弃）。
func (s *Store) log(level int, message string) {
	if s.logf != nil {
		s.logf(level, message, "GROUP")
	}
}

// Load 启动加载——扫描组根目录下全部组，读配置并统计消息流（重启续接）。
func (s *Store) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = make(map[string]*Summary)
	if s.root == "" {
		return
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		s.log(2, "组目录枚举失败: "+err.Error())
		return
	}
	loaded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		if strings.EqualFold(dirName, "_trash") {
			continue
		}
		dir := filepath.Join(s.root, dirName)
		info := s.readGroupFile(dir, dirName)
		if info == nil {
			continue
		}
		// 启用组才扫消息流并进入运行态；停用组保持轻量条目（统计归零、零消息流 IO）
		if info.Enabled {
			s.scanMessages(dir, info)
			info.Running = true
		}
		s.groups[strings.ToLower(info.Name)] = info
		loaded++
	}
	if loaded > 0 {
		s.log(1, "组数据面已加载："+strconv.Itoa(loaded)+" 组")
	}
}

// groupDir 组目录路径——Data/group/<组名>。
func (s *Store) groupDir(name string) string {
	return filepath.Join(s.root, name)
}

// readGroupFile 组配置读取——防御式（缺失按目录名构造并出声；坏 JSON 出声后按默认构造，不静默吞组）。
func (s *Store) readGroupFile(dir, fallbackName string) *Summary {
	info := &Summary{
		Name:             fallbackName,
		Enabled:          true,
		MemberKeys:       []string{},
		AdminKeys:        []string{},
		SubscriberKeys:   []string{},
		SubscribeCursors: map[string]int64{},
	}
	path := filepath.Join(dir, "group.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.log(2, "组配置缺失（按目录名建最小配置）: "+path)
			s.writeGroupFile(info)
			return info
		}
		s.log(3, "组配置读取失败（按目录名建最小配置）: "+path+" | "+err.Error())
		return info
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		s.log(3, "组配置解析失败（按目录名建最小配置）: "+path+" | "+err.Error())
		return info
	}
	if name := stringField(fields, "name"); name != "" {
		info.Name = name
	}
	info.MemberKeys = stringArrayField(fields, "memberKeys")
	info.AdminKeys = stringArrayField(fields, "adminKeys")
	info.SubscriberKeys = stringArrayField(fields, "subscriberKeys")
	if cursors, ok := longMapField(fields, "subscribeCursors"); ok {
		info.SubscribeCursors = cursors
	}
	info.SubscribeIdleSec = int(numberField(fields, "subscribeIdleSec"))
	info.Project = stringField(fields, "project")
	info.Layer = stringField(fields, "layer")
	info.CreatedAt = numberField(fields, "createdAt")
	if raw, exists := fields["enabled"]; exists {
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err == nil && !enabled {
			info.Enabled = false
		}
	}
	return info
}

// writeGroupFile 组配置写入——group.json 原子写（唯一出口；失败出声不阻断调用方）。
func (s *Store) writeGroupFile(info *Summary) {
	dir := s.groupDir(info.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log(3, "组目录创建失败: "+dir+" | "+err.Error())
		return
	}
	payload := persistedGroup{
		Name:             info.Name,
		MemberKeys:       nonNilStrings(info.MemberKeys),
		AdminKeys:        nonNilStrings(info.AdminKeys),
		SubscriberKeys:   nonNilStrings(info.SubscriberKeys),
		SubscribeIdleSec: info.SubscribeIdleSec,
		SubscribeCursors: nonNilCursors(info.SubscribeCursors),
		Project:          info.Project,
		CreatedAt:        info.CreatedAt,
		Layer:            info.Layer,
		Enabled:          info.Enabled,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.log(3, "组配置序列化失败: "+info.Name+" | "+err.Error())
		return
	}
	if err := atomicWrite(filepath.Join(dir, "group.json"), body); err != nil {
		s.log(3, "组配置写入失败: "+info.Name+" | "+err.Error())
	}
}

// scanMessages 消息流统计扫描——读 messages.jsonl 取末序号 / 条数 / 最新时刻；坏行出声跳过。
func (s *Store) scanMessages(dir string, info *Summary) {
	path := filepath.Join(dir, "messages.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log(3, "组消息流读取失败: "+path+" | "+err.Error())
		}
		return
	}
	badCount := 0
	badFirstLine := 0
	for index, line := range strings.Split(string(raw), "\n") {
		lineNo := index + 1
		if strings.TrimSpace(line) == "" {
			continue
		}
		msg, ok := parseMessageLine(line)
		if !ok {
			badCount++
			if badFirstLine == 0 {
				badFirstLine = lineNo
			}
			continue
		}
		info.MsgCount++
		if msg.Seq > info.LastSeq {
			info.LastSeq = msg.Seq
		}
		if msg.Ts > info.LastAt {
			info.LastAt = msg.Ts
		}
	}
	if badCount > 0 {
		s.log(2, "组消息坏行跳过："+path+" | 共 "+strconv.Itoa(badCount)+" 行（首行 "+strconv.Itoa(badFirstLine)+"）")
	}
}

// parseMessageLine 消息行解析——JSONL 单行解析；非法（非对象 / 缺 seq）返回 false。
func parseMessageLine(line string) (Message, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return Message{}, false
	}
	rawSeq, exists := fields["seq"]
	if !exists {
		return Message{}, false
	}
	seq, ok := int64Value(rawSeq)
	if !ok {
		return Message{}, false
	}
	msg := Message{Seq: seq, Type: "note"}
	msg.From = stringField(fields, "from")
	msg.To = stringField(fields, "to")
	if msgType := stringField(fields, "type"); msgType != "" {
		msg.Type = msgType
	}
	msg.Text = stringField(fields, "text")
	msg.Ts = numberField(fields, "ts")
	msg.Refs = stringField(fields, "refs")
	return msg, true
}

// stringField 字符串属性读取——缺失 / 非字符串返回空串。
func stringField(fields map[string]json.RawMessage, name string) string {
	raw, exists := fields[name]
	if !exists {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// stringArrayField 字符串数组属性读取——缺失 / 非数组返回空数组（项级非字符串与空串跳过）。
func stringArrayField(fields map[string]json.RawMessage, name string) []string {
	raw, exists := fields[name]
	if !exists {
		return []string{}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return []string{}
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
	return out
}

// longMapField 键值映射读取——对象形态 {键: 数值}；缺失 / 非对象返回 false。
func longMapField(fields map[string]json.RawMessage, name string) (map[string]int64, bool) {
	raw, exists := fields[name]
	if !exists {
		return map[string]int64{}, false
	}
	var items map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return map[string]int64{}, false
	}
	out := make(map[string]int64, len(items))
	for key, item := range items {
		if value, ok := int64Value(item); ok {
			out[key] = value
		}
	}
	return out, true
}

// numberField 数值属性读取——缺失 / 非数值返回 0。
func numberField(fields map[string]json.RawMessage, name string) int64 {
	raw, exists := fields[name]
	if !exists {
		return 0
	}
	value, _ := int64Value(raw)
	return value
}

// int64Value JSON 数值 → int64（非整数形态返回 false）。
func int64Value(raw json.RawMessage) (int64, bool) {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

// readAllMessages 消息流全量读取——逐行解析（坏行出声计数跳过），seq 升序。
func (s *Store) readAllMessages(name string) []Message {
	list := []Message{}
	useName := strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[useName]
	if !exists {
		return list
	}
	path := filepath.Join(s.groupDir(info.Name), "messages.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log(3, "组消息流读取失败: "+path+" | "+err.Error())
		}
		return list
	}
	badCount := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		msg, ok := parseMessageLine(line)
		if !ok {
			badCount++
			continue
		}
		list = append(list, msg)
	}
	if badCount > 0 {
		s.log(2, "组消息坏行跳过："+path+" | 共 "+strconv.Itoa(badCount)+" 行")
	}
	return list
}

// ReadTailMessages 读消息流尾部——取最后 limit 条（seq 升序）；hasMore = 更早还有未返回的条目。
func (s *Store) ReadTailMessages(name string, limit int) (messages []Message, hasMore bool) {
	useLimit := limit
	if useLimit <= 0 {
		useLimit = DefaultReadLimit
	}
	if useLimit > MaxReadLimit {
		useLimit = MaxReadLimit
	}
	all := s.readAllMessages(name)
	if len(all) == 0 {
		return []Message{}, false
	}
	start := len(all) - useLimit
	if start < 0 {
		start = 0
	}
	return append([]Message{}, all[start:]...), start > 0
}

// CopySummary 组投影副本——缓存不直出（调用方改动不污染缓存）。
func copySummary(src *Summary) *Summary {
	cp := &Summary{
		Name:             src.Name,
		MemberKeys:       append([]string{}, src.MemberKeys...),
		AdminKeys:        append([]string{}, src.AdminKeys...),
		SubscriberKeys:   append([]string{}, src.SubscriberKeys...),
		Project:          src.Project,
		CreatedAt:        src.CreatedAt,
		Layer:            src.Layer,
		Enabled:          src.Enabled,
		Running:          src.Running,
		LastSeq:          src.LastSeq,
		MsgCount:         src.MsgCount,
		LastAt:           src.LastAt,
		SubscribeIdleSec: src.SubscribeIdleSec,
		SubscribeCursors: make(map[string]int64, len(src.SubscribeCursors)),
	}
	for key, value := range src.SubscribeCursors {
		cp.SubscribeCursors[key] = value
	}
	return cp
}

// ListGroups 组全量列表——创建时刻升序（同刻按组名序）。
func (s *Store) ListGroups() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Summary, 0, len(s.groups))
	for _, info := range s.groups {
		list = append(list, *copySummary(info))
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt < list[j].CreatedAt
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// GetGroup 单组查询（不存在 = nil）。
func (s *Store) GetGroup(name string) *Summary {
	useName := strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[useName]
	if !exists {
		return nil
	}
	return copySummary(info)
}

// IsValidName 组名校验——字母 / 数字 / 中文 / 连字符 / 下划线，1..32 字符；禁路径分隔与空白。
func IsValidName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// keepMembers 名单收敛——只保留同时出现在成员名单里的项（管理员 / 订阅者恒为成员子集）。
func keepMembers(wanted, members []string) []string {
	kept := []string{}
	for _, want := range normalizeMembers(wanted) {
		for _, member := range members {
			if want == member {
				kept = append(kept, want)
				break
			}
		}
	}
	return kept
}

// reseedCursors 订阅游标收敛——只保留当前订阅者的键；新订阅者从 seedSeq 起（不补历史）。
func reseedCursors(current map[string]int64, subscribers []string, seedSeq int64) map[string]int64 {
	next := make(map[string]int64, len(subscribers))
	for _, key := range subscribers {
		if strings.TrimSpace(key) == "" {
			continue
		}
		cursor := seedSeq
		for existingKey, existingCursor := range current {
			if strings.EqualFold(existingKey, key) {
				cursor = existingCursor
				break
			}
		}
		next[key] = cursor
	}
	return next
}

// normalizeMembers 成员清洗——去空白 / 去空项 / 去重（保持入参序）。
func normalizeMembers(members []string) []string {
	list := []string{}
	for _, member := range members {
		key := strings.TrimSpace(member)
		if key == "" {
			continue
		}
		exists := false
		for _, kept := range list {
			if kept == key {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, key)
		}
	}
	return list
}

// CreateOrUpdate 新建 / 改组——同名覆盖成员与项目（创建时刻与启用态保留）；返回 ok 或 ERR| 前缀文本。
func (s *Store) CreateOrUpdate(name string, memberKeys []string, project string, adminKeys, subscriberKeys []string, subscribeIdleSec int) string {
	useName := strings.TrimSpace(name)
	if !IsValidName(useName) {
		return "ERR|GROUP_NAME_INVALID|组名非法（字母/数字/中文/-/_，1.." + strconv.Itoa(MaxNameLength) + " 字符）: " + useName
	}
	members := normalizeMembers(memberKeys)
	if len(members) == 0 {
		return "ERR|GROUP_MEMBERS_EMPTY|组至少需要一名成员"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(useName)
	info, found := s.groups[key]
	if !found {
		info = &Summary{
			Name:             useName,
			CreatedAt:        Now(),
			Enabled:          true,
			Running:          true,
			MemberKeys:       []string{},
			AdminKeys:        []string{},
			SubscriberKeys:   []string{},
			SubscribeCursors: map[string]int64{},
		}
		s.groups[key] = info
	}
	info.MemberKeys = members
	info.Project = strings.TrimSpace(project)
	// 管理员 / 订阅者恒为成员子集——未显式给出时按原名单随成员变更收敛（不静默保留非成员）
	wantAdmins := info.AdminKeys
	if adminKeys != nil {
		wantAdmins = adminKeys
	}
	wantSubs := info.SubscriberKeys
	if subscriberKeys != nil {
		wantSubs = subscriberKeys
	}
	info.AdminKeys = keepMembers(wantAdmins, members)
	info.SubscriberKeys = keepMembers(wantSubs, members)
	if subscribeIdleSec >= 0 {
		info.SubscribeIdleSec = subscribeIdleSec
	}
	// 游标收敛——取消订阅即删游标；新订阅者从当前末序号起（只收之后的消息，不补历史）
	info.SubscribeCursors = reseedCursors(info.SubscribeCursors, info.SubscriberKeys, info.LastSeq)
	s.writeGroupFile(info)
	verb := "组新建: "
	if found {
		verb = "组更新: "
	}
	s.log(1, verb+useName+" | 成员 "+strconv.Itoa(len(members))+" 名 | 管理员 "+strconv.Itoa(len(info.AdminKeys))+" 名 | 订阅 "+strconv.Itoa(len(info.SubscriberKeys))+" 名")
	return "ok|" + useName
}

// SetEnabled 组启用 / 停用——落 group.json 的启用意图；运行态由 StartGroup / StopGroup 管理。
func (s *Store) SetEnabled(name string, enabled bool) string {
	useName := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[strings.ToLower(useName)]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	info.Enabled = enabled
	s.writeGroupFile(info)
	verb := "组停用: "
	if enabled {
		verb = "组启用: "
	}
	s.log(1, verb+info.Name)
	return "ok|" + info.Name
}

// StartGroup 组启用——进入运行态：扫描消息流统计并置 Running；重复启用幂等。
func (s *Store) StartGroup(name string) string {
	useName := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[strings.ToLower(useName)]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	if info.Running {
		return "ok|" + info.Name
	}
	info.LastSeq = 0
	info.MsgCount = 0
	info.LastAt = 0
	s.scanMessages(s.groupDir(info.Name), info)
	info.Running = true
	s.log(1, "组启动: "+info.Name+" | 消息 "+strconv.Itoa(info.MsgCount)+" 条")
	return "ok|" + info.Name
}

// StopGroup 组停用——退出运行态：统计归零（不删配置、不动磁盘）；重复停用幂等。
func (s *Store) StopGroup(name string) string {
	useName := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[strings.ToLower(useName)]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	if !info.Running {
		return "ok|" + info.Name
	}
	info.Running = false
	info.LastSeq = 0
	info.MsgCount = 0
	info.LastAt = 0
	s.log(1, "组停止: "+info.Name)
	return "ok|" + info.Name
}

// IsRunning 运行态查询——工具面门禁用（停用组一律拒绝）。
func (s *Store) IsRunning(name string) bool {
	useName := strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[useName]
	if !exists {
		return false
	}
	return info.Running
}

// Delete 删组——整目录移入 Data/group/_trash/<组名>_<时刻>/（可恢复），缓存移除。
func (s *Store) Delete(name string) string {
	useName := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(useName)
	info, exists := s.groups[key]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	dir := s.groupDir(info.Name)
	trashDir := filepath.Join(s.root, "_trash")
	target := filepath.Join(trashDir, info.Name+"_"+strconv.FormatInt(Now(), 10))
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		s.log(3, "组删除失败: "+info.Name+" | "+err.Error())
		return "ERR|GROUP_DELETE_FAIL|删组失败: " + err.Error()
	}
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, target); err != nil {
			s.log(3, "组删除失败: "+info.Name+" | "+err.Error())
			return "ERR|GROUP_DELETE_FAIL|删组失败: " + err.Error()
		}
	}
	delete(s.groups, key)
	s.log(1, "组删除: "+info.Name+" → "+target)
	return "ok|" + info.Name
}

// ClearMessages 清空消息流——原地归档 messages.jsonl（改名 .bak）后重建空流；统计与订阅游标归零。
func (s *Store) ClearMessages(name string) string {
	useName := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[strings.ToLower(useName)]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	dir := s.groupDir(info.Name)
	path := filepath.Join(dir, "messages.jsonl")
	archived := ""
	if _, err := os.Stat(path); err == nil {
		stamp := time.Now().Format("20060102_150405")
		target := filepath.Join(dir, "messages."+stamp+".jsonl.bak")
		suffix := 1
		for {
			if _, statErr := os.Stat(target); errors.Is(statErr, os.ErrNotExist) {
				break
			}
			target = filepath.Join(dir, "messages."+stamp+"_"+strconv.Itoa(suffix)+".jsonl.bak")
			suffix++
		}
		if err := os.Rename(path, target); err != nil {
			s.log(3, "组消息归档失败: "+path+" | "+err.Error())
			return "ERR|GROUP_WRITE_FAIL|消息归档失败: " + err.Error()
		}
		archived = filepath.Base(target)
	}
	info.LastSeq = 0
	info.MsgCount = 0
	info.LastAt = 0
	info.SubscribeCursors = map[string]int64{}
	s.writeGroupFile(info)
	archiveText := "（无消息文件）"
	if archived != "" {
		archiveText = archived
	}
	s.log(1, "组消息清空: "+info.Name+" | 归档 "+archiveText)
	return "ok|" + info.Name + "|" + archived
}

// AppendMessage 追加消息——组内 seq 单调递增（末序号 + 1），一行一条追加写 messages.jsonl。
func (s *Store) AppendMessage(name, from, msgType, text, refs, to string) string {
	useName := strings.TrimSpace(name)
	useType := strings.TrimSpace(msgType)
	useTo := strings.TrimSpace(to)
	if useType == "" {
		useType = "note"
	}
	if useType != "delegation" && useType != "receipt" && useType != "note" {
		return "ERR|GROUP_TYPE_INVALID|消息类型非法（delegation|receipt|note）: " + useType
	}
	useText := strings.TrimSpace(text)
	if useText == "" {
		return "ERR|GROUP_TEXT_EMPTY|消息正文为空"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[strings.ToLower(useName)]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	if !info.Running {
		return "ERR|GROUP_NOT_RUNNING|组未启用: " + useName
	}
	fromKey := strings.TrimSpace(from)
	if fromKey != "" && !containsKey(info.MemberKeys, fromKey) {
		return "ERR|GROUP_FROM_NOT_MEMBER|发送方不在组内: " + fromKey
	}
	if useTo != "" {
		if !containsKey(info.MemberKeys, useTo) {
			return "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: " + useTo
		}
		if fromKey != "" && useTo == fromKey {
			return "ERR|GROUP_TARGET_SELF|不能 @ 自己: " + useTo
		}
	}
	nextSeq := info.LastSeq + 1
	now := Now()
	line := persistedMessage{
		Seq:  nextSeq,
		From: fromKey,
		To:   useTo,
		Type: useType,
		Text: useText,
		Ts:   now,
		Refs: strings.TrimSpace(refs),
	}
	body, err := json.Marshal(line)
	if err != nil {
		return "ERR|GROUP_WRITE_FAIL|消息写入失败: " + err.Error()
	}
	dir := s.groupDir(info.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log(3, "组消息追加失败: "+info.Name+" | "+err.Error())
		return "ERR|GROUP_WRITE_FAIL|消息写入失败: " + err.Error()
	}
	file, err := os.OpenFile(filepath.Join(dir, "messages.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		s.log(3, "组消息追加失败: "+info.Name+" | "+err.Error())
		return "ERR|GROUP_WRITE_FAIL|消息写入失败: " + err.Error()
	}
	_, writeErr := file.WriteString(string(body) + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		if writeErr == nil {
			writeErr = closeErr
		}
		s.log(3, "组消息追加失败: "+info.Name+" | "+writeErr.Error())
		return "ERR|GROUP_WRITE_FAIL|消息写入失败: " + writeErr.Error()
	}
	info.LastSeq = nextSeq
	info.MsgCount++
	info.LastAt = now
	return "ok|" + info.Name + "|seq=" + strconv.FormatInt(nextSeq, 10)
}

// ReadMessages 读消息流——按 seq 升序返回 afterSeq 之后的消息（afterSeq ≤ 0 = 从头）。
func (s *Store) ReadMessages(name string, afterSeq int64, limit int) []Message {
	useLimit := limit
	if useLimit <= 0 {
		useLimit = DefaultReadLimit
	}
	if useLimit > MaxReadLimit {
		useLimit = MaxReadLimit
	}
	list := []Message{}
	all := s.readAllMessages(name)
	for _, msg := range all {
		if msg.Seq <= afterSeq {
			continue
		}
		list = append(list, msg)
		if len(list) >= useLimit {
			break
		}
	}
	return list
}

// GetSubscribeIdleSec 订阅静默阈值（秒）——组配置字段；未设置（≤0）按默认 300 秒。
func GetSubscribeIdleSec(info *Summary) int {
	if info == nil || info.SubscribeIdleSec <= 0 {
		return DefaultSubscribeIdleSec
	}
	return info.SubscribeIdleSec
}

// GetSubscriberCursor 订阅游标读取——未设置返回 0（从头）。
func (s *Store) GetSubscriberCursor(name, catKey string) int64 {
	useName := strings.ToLower(strings.TrimSpace(name))
	useCat := strings.TrimSpace(catKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[useName]
	if !exists {
		return 0
	}
	for key, value := range info.SubscribeCursors {
		if strings.EqualFold(key, useCat) {
			return value
		}
	}
	return 0
}

// SetSubscriberCursor 订阅游标推进——写入并落盘（低频操作，整文件原子写）。
func (s *Store) SetSubscriberCursor(name, catKey string, seq int64) string {
	useName := strings.TrimSpace(name)
	useCat := strings.TrimSpace(catKey)
	if useCat == "" {
		return "ERR|GROUP_CURSOR_KEY_EMPTY|订阅游标缺少猫 key"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, exists := s.groups[strings.ToLower(useName)]
	if !exists {
		return "ERR|GROUP_NOT_FOUND|组不存在: " + useName
	}
	next := make(map[string]int64, len(info.SubscribeCursors)+1)
	for key, value := range info.SubscribeCursors {
		if strings.EqualFold(key, useCat) {
			continue
		}
		next[key] = value
	}
	next[useCat] = seq
	info.SubscribeCursors = next
	s.writeGroupFile(info)
	return "ok|" + info.Name + "|" + useCat + "|" + strconv.FormatInt(seq, 10)
}

// Now 当前时刻——Unix 毫秒。
func Now() int64 {
	return time.Now().UnixMilli()
}

// containsKey 名单包含判定——按 key 精确匹配。
func containsKey(keys []string, key string) bool {
	for _, item := range keys {
		if item == key {
			return true
		}
	}
	return false
}

// nonNilStrings 空数组兜底（落盘形态恒为数组，不写 null）。
func nonNilStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// nonNilCursors 空游标表兜底（落盘形态恒为对象，不写 null）。
func nonNilCursors(cursors map[string]int64) map[string]int64 {
	if cursors == nil {
		return map[string]int64{}
	}
	return cursors
}

// atomicWrite 原子写——同目录临时文件 + rename（失败清临时件）。
func atomicWrite(path string, body []byte) error {
	tmp := path + "." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
