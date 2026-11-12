package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// PendingMessage 是待处理消息实体——内容 + 来源 + 附件 + 来源锚 + 入队序号。
//
// 字段语义对齐 C# ChatSession.PendingMessage：Source 为 user（人工）或 sys 族名
// （systemauto / sleep / timer / delay / restart）；Seq 是两队列共享的到达序排序键。
type PendingMessage struct {
	// Content 消息内容
	Content string
	// Source 来源——user（人工）/ systemauto · sleep · timer · delay · restart（系统注入）
	Source string
	// ImagesJSON 附件引用 JSON 数组（图片绝对路径；空 = 无附件）
	ImagesJSON string
	// Origin 原注入来源标记（QQ 注入的轮起点锚；空 = 非 QQ 注入 / 系统注入）
	Origin string
	// Seq 入队序号（到达序——两队列合并排序键）
	Seq int64
}

// pendingStoreDir 是 pending 落盘目录（宿主启动期注入；空 = 仅内存）。
//
// 对齐 C# ChatSession.ConfigurePendingStore：Core 不依赖配置面，路径由入口壳给。
var pendingStoreDir atomic.Value

// ConfigurePendingStore 配置 pending 落盘目录（须先于会话创建；空 = 关闭落盘）。
func ConfigurePendingStore(dir string) {
	pendingStoreDir.Store(dir)
}

// PendingStoreDir 返回当前 pending 落盘目录（空 = 不落盘）。
func PendingStoreDir() string {
	value, _ := pendingStoreDir.Load().(string)
	return value
}

// HostRestarting 是宿主重启停机态判定——由装配层注入（空 = 不停机）。
//
// 对齐 C# ChatSession.IsHostRestarting（DataBox 全局盒 host_restart_state == "requested"）。
var HostRestarting = func() bool { return false }

// isHostRestarting 停机态判定（钩子置空 = 不停机）。
func isHostRestarting() bool {
	if HostRestarting == nil {
		return false
	}
	return HostRestarting()
}

// PendingQueue 是会话待处理队列——人工队列 + 系统注入队列（双队列并行消费）。
//
// 语义（A232）：**入队即物化**（受理后落盘）· **消费即移除**（取出后重写）·
// **构造时加载**（跨宿主重启保留——sys 队列仍丢弃）。
// 落盘面：<pendingStoreDir>/pending_<catKey>.json（每猫一文件——无跨猫读改写竞争）。
// 并发：C# 侧人工队列由 HTTP 线程投递、主线程消费；Go 侧统一走互斥锁（机制不同构，语义不变）。
type PendingQueue struct {
	// mu 保护 user / sys / seq
	mu sync.Mutex
	// catKey 猫 key（落盘文件名与日志定位）
	catKey string
	// user 人工队列（user / QQ 注入 / 指令注入）
	user []PendingMessage
	// sys 系统注入队列（来源族名随条目携带）
	sys []PendingMessage
	// seq 入队序号计数器（两队列共享）
	seq int64
	// loaded 已加载标志（防重复入队）
	loaded bool
}

// NewPendingQueue 建立队列并从磁盘恢复人工队列（构造时加载——seq 续接）。
//
// 文件缺失静默跳过；损坏记 WARN 出声（按空队列处理，不抛）。
func NewPendingQueue(catKey string) *PendingQueue {
	q := &PendingQueue{catKey: catKey}
	q.loadFromDisk()
	return q
}

// CatKey 返回队列归属猫 key。
func (q *PendingQueue) CatKey() string { return q.catKey }

// pendingFilePath 本队列落盘文件路径——非法文件名字符净化为 `_`（目录为空返回空串）。
func (q *PendingQueue) pendingFilePath() string {
	dir := PendingStoreDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "pending_"+sanitizeFileName(q.catKey)+".json")
}

// sanitizeFileName 非法文件名字符替换为下划线（对齐 Path.GetInvalidFileNameChars 的 Windows 集合）。
func sanitizeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		bad := r < 0x20
		if !bad {
			switch r {
			case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
				bad = true
			}
		}
		if bad {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// loadFromDisk 从磁盘加载人工队列——按落盘序入队，序号计数器续接（防回退重号）。
//
// 重复调用零动作（loaded 守卫）。系统注入队列不加载（重启即丢弃）。
func (q *PendingQueue) loadFromDisk() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.loaded {
		return
	}
	q.loaded = true
	path := q.pendingFilePath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		logAt(LogModuleHost, 2, "待处理消息恢复失败（按空队列处理）: cat="+q.catKey+" | "+err.Error(), LogCategoryPending)
		return
	}
	if len(raw) == 0 {
		return
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		logAt(LogModuleHost, 2, "待处理消息恢复失败（按空队列处理）: cat="+q.catKey+" | "+err.Error(), LogCategoryPending)
		return
	}
	entriesRaw, exists := root["entries"]
	if !exists {
		return
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(entriesRaw, &entries); err != nil {
		return
	}
	var maxSeq int64 = -1
	for _, item := range entries {
		msg := PendingMessage{
			Content:    rawElemStr(item, "content"),
			Source:     rawElemStr(item, "source"),
			ImagesJSON: rawElemStr(item, "imagesJson"),
			Origin:     rawElemStr(item, "origin"),
			Seq:        rawElemLong(item, "seq"),
		}
		if msg.Source == "" {
			msg.Source = SourceUser
		}
		q.user = append(q.user, msg)
		if msg.Seq > maxSeq {
			maxSeq = msg.Seq
		}
	}
	if maxSeq >= q.seq {
		q.seq = maxSeq + 1
	}
	logAt(LogModuleHost, 1, "待处理消息恢复: cat="+q.catKey+" | 载入 "+strconv.Itoa(len(q.user))+" 条", LogCategoryPending)
}

// rawElemStr 取对象字符串字段（缺失 / 非字符串 / 非对象返回空串）。
func rawElemStr(raw json.RawMessage, name string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	value, exists := obj[name]
	if !exists {
		return ""
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return ""
	}
	return s
}

// rawElemLong 取对象长整数字段（缺失 / 非数字返回 0）。
func rawElemLong(raw json.RawMessage, name string) int64 {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0
	}
	value, exists := obj[name]
	if !exists {
		return 0
	}
	var n int64
	if err := json.Unmarshal(value, &n); err != nil {
		return 0
	}
	return n
}

// saveToDisk 待处理消息落盘（入队 / 消费后调用）——原子写（临时文件 + 覆盖移动）。
//
// 失败不抛（记 ERR 出声）；目录为空零动作。落盘格式：{"entries":[{content,source,imagesJson,origin,seq}]}。
func (q *PendingQueue) saveToDisk() {
	path := q.pendingFilePath()
	if path == "" {
		return
	}
	q.mu.Lock()
	list := make([]PendingMessage, len(q.user))
	copy(list, q.user)
	q.mu.Unlock()
	sort.SliceStable(list, func(i, j int) bool { return list[i].Seq < list[j].Seq })
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"content":`)
		b.WriteString(jsonStr(list[i].Content))
		b.WriteString(`,"source":`)
		b.WriteString(jsonStr(list[i].Source))
		b.WriteString(`,"imagesJson":`)
		b.WriteString(jsonStr(list[i].ImagesJSON))
		b.WriteString(`,"origin":`)
		b.WriteString(jsonStr(list[i].Origin))
		b.WriteString(`,"seq":`)
		b.WriteString(itoa64(list[i].Seq))
		b.WriteString("}")
	}
	b.WriteString("]}")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logAt(LogModuleHost, 3, "待处理消息落盘失败: cat="+q.catKey+" | "+err.Error(), LogCategoryPending)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		logAt(LogModuleHost, 3, "待处理消息落盘失败: cat="+q.catKey+" | "+err.Error(), LogCategoryPending)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		logAt(LogModuleHost, 3, "待处理消息落盘失败: cat="+q.catKey+" | "+err.Error(), LogCategoryPending)
	}
}

// Post 用户消息入队——空文本且无附件拒收；宿主停机态拒收；入队即物化。
//
// 返回 true = 已受理入队 / false = 未受理（空内容 · 宿主停机态）。
func (q *PendingQueue) Post(content, source, imagesJSON, origin string) bool {
	if content == "" && imagesJSON == "" {
		return false
	}
	if source == "" {
		source = SourceUser
	}
	if isHostRestarting() {
		logAt(LogModuleHost, 2, "宿主重启中——本条输入未受理（来源 "+source+"）", LogCategoryRestart)
		return false
	}
	q.mu.Lock()
	msg := PendingMessage{Content: content, Source: source, ImagesJSON: imagesJSON, Origin: origin, Seq: q.seq}
	q.seq++
	q.user = append(q.user, msg)
	q.mu.Unlock()
	q.saveToDisk()
	return true
}

// PostSystem 系统注入入队——类型校验 + 停机态校验 + 入 sys 队列（固定前缀在落前文单点套用）。
//
// 返回 "OK" 或 "ERR|CODE|msg"（失败可见：未知类型 / 空内容 / 停机态一律拒绝）。
func (q *PendingQueue) PostSystem(kind, content, imagesJSON string) string {
	if !IsKnownSysKind(kind) {
		return "ERR|BAD_ARGS|未知系统注入类型: " + kind
	}
	if strings.TrimSpace(content) == "" {
		return "ERR|BAD_ARGS|系统注入内容为空"
	}
	if isHostRestarting() {
		logAt(LogModuleHost, 2, "宿主重启中——sys 注入未受理（类型 "+kind+"）", LogCategoryRestart)
		return "ERR|HOST_RESTARTING|系统注入未受理（宿主重启停机态 · 类型 " + kind + "）"
	}
	q.mu.Lock()
	msg := PendingMessage{Content: content, Source: kind, ImagesJSON: imagesJSON, Origin: "", Seq: q.seq}
	q.seq++
	q.sys = append(q.sys, msg)
	q.mu.Unlock()
	logAt(LogModuleHost, 1, "系统注入入队: cat="+q.catKey+" | 类型 "+kind+" | 长度 "+strconv.Itoa(len([]rune(content))), LogCategoryChat)
	return "OK"
}

// TakeAll 两队列按到达时间序合并取尽——消费即移除（人工队列被取走即重写落盘）。
//
// includeUser=false 时只取 sys 队列（人工输入空闲注入开关开启时轮内不取）。
// 返回按入队序号升序的消息清单。
func (q *PendingQueue) TakeAll(includeUser bool) []PendingMessage {
	q.mu.Lock()
	all := make([]PendingMessage, 0, len(q.user)+len(q.sys))
	tookUser := false
	if includeUser {
		all = append(all, q.user...)
		tookUser = len(q.user) > 0
		q.user = nil
	}
	all = append(all, q.sys...)
	q.sys = nil
	q.mu.Unlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].Seq < all[j].Seq })
	if tookUser {
		q.saveToDisk()
	}
	return all
}

// DrainIntoRound 本轮边界——两队列按到达序取尽并交给注入回调（轮首取尽插入前文）。
//
// 返回 true = 有插入（调用方据此续轮）。
func (q *PendingQueue) DrainIntoRound(includeUser bool, appendInjection func(PendingMessage)) bool {
	batch := q.TakeAll(includeUser)
	if len(batch) == 0 {
		return false
	}
	for _, msg := range batch {
		appendInjection(msg)
	}
	return true
}

// DropSysIfRestarting 停机态丢弃在途系统注入——人工队列跨重启保留，不在本方法清理面。
//
// 返回丢弃条数（0 = 未触发）。
func (q *PendingQueue) DropSysIfRestarting() int {
	q.mu.Lock()
	count := len(q.sys)
	q.mu.Unlock()
	if count == 0 {
		return 0
	}
	if !isHostRestarting() {
		return 0
	}
	q.mu.Lock()
	q.sys = nil
	q.mu.Unlock()
	logAt(LogModuleHost, 2, "宿主重启停机态——丢弃在途系统注入 "+strconv.Itoa(count)+" 条", LogCategoryRestart)
	return count
}

// Len 返回两队列当前条数（人工 / 系统）。
func (q *PendingQueue) Len() (user int, sys int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.user), len(q.sys)
}

// BuildFragment 构建 pending 段 JSON——人工队列待处理消息投影（按入队序升序）。
//
// 条目字段：content / source / origin（附件面 ImagesJSON 不投影——展示面用不到）。
func (q *PendingQueue) BuildFragment() string {
	q.mu.Lock()
	list := make([]PendingMessage, len(q.user))
	copy(list, q.user)
	q.mu.Unlock()
	sort.SliceStable(list, func(i, j int) bool { return list[i].Seq < list[j].Seq })
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"content":`)
		b.WriteString(jsonStr(list[i].Content))
		b.WriteString(`,"source":`)
		b.WriteString(jsonStr(list[i].Source))
		b.WriteString(`,"origin":`)
		b.WriteString(jsonStr(list[i].Origin))
		b.WriteString("}")
	}
	b.WriteString("]}")
	return b.String()
}
