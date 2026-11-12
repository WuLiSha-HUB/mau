package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TruncatedRepairNote 截断修复标注——补全成功的消息在 Content 尾部追加（LLM 可见）。
const TruncatedRepairNote = "\n\n[系统自动修复] 本消息在宿主中断时被截断——以上为崩溃前已落盘部分"

// repairChars 补全候选字符集——闭合字符串 / 转义引号 / 闭合对象 / 闭合数组。
var repairChars = []byte{'"', '\\', '}', ']'}

// repairMaxDepth 补全候选深度上限。
const repairMaxDepth = 4

// SessionStore 是会话前文管理器——JSONL 增量落盘。
//
// 落盘：sessions/<id>/<id>.jsonl——每行一条独立 JSON 记录（消息行 t=m / 元数据行 t=meta）。
// 写面：每消息完成即 append（崩溃只影响最后一行）+ 会话起点 / 截断原子重写。
// 读面：逐行解析 + 末行残缺补全（结构补齐 + 显式标注——不丢弃已落盘内容）。
type SessionStore struct {
	// mu 串行化读写（前文面单写者：主线程泵）
	mu sync.Mutex
	// path 会话文件路径（.jsonl）
	path string
	// sessionID 会话标识（落盘记录字段；恒 = 猫 key）
	sessionID string
}

// NewSessionStore 建立前文管理器。
func NewSessionStore(path string) *SessionStore { return &SessionStore{path: path} }

// Path 会话文件路径（诊断用）。
func (s *SessionStore) Path() string { return s.path }

// SessionID 会话标识。
func (s *SessionStore) SessionID() string { return s.sessionID }

// SetSessionID 设置会话标识（唯一标识 = 猫 key，宿主构造注入；session.new 不换标识）。
func (s *SessionStore) SetSessionID(id string) { s.sessionID = id }

// NewSessionID 新标识——ticks 字符串（唯一生成点：cat.new 建猫 id / session.fork 新猫 id）。
func NewSessionID() string {
	// .NET ticks = 0001-01-01 起的 100 纳秒数（与 C# DateTime.Now.Ticks 同形态：19 位十进制）
	const ticksAtUnixEpoch = 621355968000000000
	return itoa64(ticksAtUnixEpoch + time.Now().UnixNano()/100)
}

// metaLine 构建元数据行——t 标记 + 会话标识。
func (s *SessionStore) metaLine() string {
	return `{"t":"meta","SessionId":` + jsonStr(s.sessionID) + `}`
}

// Append 追加一条消息行——每消息完成即落盘（文件为空时先补元数据行）。
func (s *SessionStore) Append(msg Message) {
	s.appendLine(BuildMessageLine(msg), true)
}

// AppendRaw 追加原始行（预序列化——调用方已按消息行口径构造）。
func (s *SessionStore) AppendRaw(line string) { s.appendLine(line, true) }

// appendLine 追加一行到会话文件——开-写-关（无长驻句柄）。
//
// ensureMeta=true 且文件为空时先补元数据行（会话标识随首条消息落盘）。失败不阻断会话——记 ERR。
func (s *SessionStore) appendLine(line string, ensureMeta bool) {
	if line == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDirLocked(); err != nil {
		logAt(LogModuleHost, 3, "前文增量落盘失败（本条未落盘）: "+err.Error(), LogCategoryChat)
		return
	}
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logAt(LogModuleHost, 3, "前文增量落盘失败（本条未落盘）: "+err.Error(), LogCategoryChat)
		return
	}
	defer file.Close()
	info, statErr := file.Stat()
	body := ""
	if ensureMeta && statErr == nil && info.Size() == 0 {
		body = s.metaLine() + "\n"
	}
	body += line + "\n"
	if _, err := file.WriteString(body); err != nil {
		logAt(LogModuleHost, 3, "前文增量落盘失败（本条未落盘）: "+err.Error(), LogCategoryChat)
	}
}

// Rewrite 重写会话文件（原子——临时文件 + 替换）——会话起点 / 截断 / 前文重建唯一写通道。
func (s *SessionStore) Rewrite(messages []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDirLocked(); err != nil {
		logAt(LogModuleHost, 3, "前文重写落盘失败: "+err.Error(), LogCategoryChat)
		return
	}
	var b strings.Builder
	b.WriteString(s.metaLine())
	b.WriteByte('\n')
	for _, msg := range messages {
		b.WriteString(BuildMessageLine(msg))
		b.WriteByte('\n')
	}
	tmp := s.path + "." + NewSessionID() + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		logAt(LogModuleHost, 3, "前文重写落盘失败: "+err.Error(), LogCategoryChat)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		logAt(LogModuleHost, 3, "前文重写落盘失败: "+err.Error(), LogCategoryChat)
	}
}

// ensureDirLocked 确保会话文件目录存在（调用方持锁）。
func (s *SessionStore) ensureDirLocked() error {
	dir := filepath.Dir(s.path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// TryLoad 尝试加载前文——逐行解析 + 末行残缺补全（结构补齐 + 显式标注）。
//
// 末行残缺：补全为合法 JSON 后按消息消费（Content 追加修复标注）；无法构成消息 → 丢弃 + 告警。
// 非末行损坏：跳过坏行 + 告警，其余照常加载（不整文件作废）。全文件不可解析 → 备份 .bad + 返回 false。
// 元数据行只承载会话标识，不进读面（身份 = 猫 key）。
func (s *SessionStore) TryLoad() ([]Message, bool) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logAt(LogModuleHost, 3, "前文读取失败（按无前文处理）: "+err.Error(), LogCategoryChat)
		}
		return nil, false
	}
	lines := strings.Split(string(raw), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	lastIndex := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastIndex = i
			break
		}
	}
	if lastIndex < 0 {
		return nil, false
	}
	list := make([]Message, 0, len(lines))
	repairedAny := false
	repairedLineIndex := -1
	repairedLineText := ""
	badLines := 0
	contentLines := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		contentLines++
		isLast := i == lastIndex
		repaired := false
		objOK := isJSONObject(line)
		if !objOK && isLast {
			if fixed, ok := tryRepairLine(line); ok {
				line = fixed
				objOK = true
				repaired = true
				repairedAny = true
				repairedLineIndex = i
			}
		}
		if !objOK {
			badLines++
			if isLast {
				logAt(LogModuleHost, 2, "前文末行残缺且无法补全——该行丢弃（无角色信息）", LogCategoryChat)
			} else {
				logAt(LogModuleHost, 2, "前文第 "+itoa(i+1)+" 行损坏——跳过（其余照常加载）", LogCategoryChat)
			}
			continue
		}
		tag := lineTag(line)
		if tag == "meta" {
			continue
		}
		msg, ok := ParseMessageLine(line)
		if !ok {
			badLines++
			logAt(LogModuleHost, 2, "前文第 "+itoa(i+1)+" 行无角色信息——丢弃（残缺丢失已落盘内容）", LogCategoryChat)
			continue
		}
		if repaired {
			msg.Content += TruncatedRepairNote
			if msg.CreatedAt <= 0 {
				prevStamp := int64(0)
				if len(list) > 0 {
					prevStamp = list[len(list)-1].CreatedAt
				}
				if prevStamp > 0 {
					msg.CreatedAt = prevStamp + 1
				} else {
					msg.CreatedAt = nowStamp()
				}
			}
			repairedLineText = BuildMessageLine(msg)
			logAt(LogModuleHost, 2, "前文末行残缺——已补全为合法 JSON（内容不完整，已标注）", LogCategoryChat)
		}
		list = append(list, msg)
	}
	if len(list) == 0 {
		if badLines > 0 && badLines == contentLines {
			s.backupCorruptFile()
		}
		return nil, false
	}
	if repairedAny && repairedLineText != "" {
		s.writeBackRepairedLine(lines, repairedLineIndex, repairedLineText)
	}
	return list, true
}

// isJSONObject 单行是否为合法 JSON 对象。
func isJSONObject(line string) bool {
	var raw map[string]json.RawMessage
	return json.Unmarshal([]byte(line), &raw) == nil
}

// tryRepairLine 末行残缺补全——闭合未终止的 JSON 字符串 + 补齐缺失的括号。
//
// 候选序列按长度递增枚举（补全字符集：引号 / 反斜杠 / 右括号 / 右方括号），
// 首个「合法且含角色信息」的结果即采用；已落盘内容全部保留（不裁剪）。
func tryRepairLine(line string) (string, bool) {
	current := []string{line}
	for depth := 1; depth <= repairMaxDepth; depth++ {
		next := make([]string, 0, len(current)*len(repairChars))
		for _, base := range current {
			for _, ch := range repairChars {
				candidate := base + string(ch)
				if isJSONObject(candidate) {
					if lineHasRoleOrMeta(candidate) {
						return candidate, true
					}
				}
				next = append(next, candidate)
			}
		}
		current = next
	}
	return "", false
}

// lineHasRoleOrMeta 候选行是否含角色信息或元数据标记。
func lineHasRoleOrMeta(line string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return false
	}
	if _, exists := raw["Role"]; exists {
		return true
	}
	return rawString(raw, "t") == "meta"
}

// writeBackRepairedLine 回写末行残缺补全结果——磁盘与内存一致（原子写）。
func (s *SessionStore) writeBackRepairedLine(lines []string, index int, fixedLine string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for i, line := range lines {
		if i == index {
			b.WriteString(fixedLine)
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		logAt(LogModuleHost, 3, "前文补全回写失败（内存已修复，磁盘未同步）: "+err.Error(), LogCategoryChat)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		logAt(LogModuleHost, 3, "前文补全回写失败（内存已修复，磁盘未同步）: "+err.Error(), LogCategoryChat)
		return
	}
	logAt(LogModuleHost, 1, "前文末行残缺——已回写补全结果（磁盘与内存一致）", LogCategoryChat)
}

// backupCorruptFile 备份不可用前文文件——改名 .bad（保留原样可追溯；下次启动不再重复解析坏文件）。
func (s *SessionStore) backupCorruptFile() {
	badPath := s.path + ".bad"
	if err := os.Remove(badPath); err != nil && !os.IsNotExist(err) {
		logAt(LogModuleHost, 2, "坏前文备份失败，保留原文件: "+err.Error(), LogCategoryChat)
		return
	}
	if err := os.Rename(s.path, badPath); err != nil {
		logAt(LogModuleHost, 2, "坏前文备份失败，保留原文件: "+err.Error(), LogCategoryChat)
		return
	}
	logAt(LogModuleHost, 3, "会话前文全部不可解析，已备份为 "+badPath+"（本次按新会话启动）", LogCategoryChat)
}
