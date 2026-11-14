package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// NoteTracker 是任务追踪器——Note 工具的三字段状态 + 三动作（set / 无参推进 / 全完成清空）。
//
// 语义对齐 C# ChatSession 的 Note 分部（M4a 会话内直执）：
//   - set：写入新计划（原生字符串数组，元素已 Trim；未完成时无 force 拒绝覆盖并回报剩余条数）
//   - 无参：推进一条；推进到末条之后即全完成 → 清空内存 + 完成态提示
//   - 返回文本 = LLM 唯一状态面（首行 A214 结构化头 + 正文）
//
// 持久化归会话元数据面（session.json 的 Note 字段），本类只管内存态与文本面。
type NoteTracker struct {
	// mu 保护三字段（HTTP 线程读状态 JSON · 主线程推进）
	mu sync.Mutex
	// tasks 任务列表（nil / 空 = 无计划）
	tasks []string
	// current 当前任务索引（0-based）
	current int
	// done 已完成任务数
	done int
	// justCompleted 刚全部完成标志（完成态展示；新计划 / 新增清除）
	justCompleted bool
	// onChange 状态变化回调（会话侧接状态段推送；nil = 无消费方）
	onChange func()
}

// NewNoteTracker 建立空任务追踪器。
func NewNoteTracker() *NoteTracker { return &NoteTracker{} }

// SetOnChange 注入状态变化回调（Note 变化触发状态段推送）。
func (n *NoteTracker) SetOnChange(fn func()) { n.onChange = fn }

// notify 状态变化通知（锁外调用）。
func (n *NoteTracker) notify() {
	if n.onChange != nil {
		n.onChange()
	}
}

// Execute 执行 Note 工具体——参数 JSON（action / content / force）→ 进度反馈文本。
//
// 参数 JSON 损坏按无参数推进语义处理（CH2 同款容错，出声）。
func (n *NoteTracker) Execute(argsJSON string) string {
	action := ""
	var contentItems []string
	contentError := ""
	force := false
	if strings.HasPrefix(argsJSON, "{") {
		var root map[string]json.RawMessage
		if err := json.Unmarshal([]byte(argsJSON), &root); err != nil {
			logAt(LogModuleSession, 2, "Note 参数 JSON 损坏，按无参数推进: "+err.Error(), LogCategorySys)
		} else {
			if raw, exists := root["action"]; exists {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					action = s
				}
			}
			if raw, exists := root["content"]; exists {
				var anyValue any
				_ = json.Unmarshal(raw, &anyValue)
				items, isArray := anyValue.([]any)
				if !isArray {
					contentError = "content 须为字符串数组（原生 JSON array）——收到 " + jsonKindName(anyValue)
				} else if len(items) == 0 {
					contentError = "content 为空数组——计划至少一条"
				} else {
					collected := make([]string, 0, len(items))
					for i, item := range items {
						itemIndex := i + 1
						text, isString := item.(string)
						if !isString {
							contentError = "content 第 " + strconv.Itoa(itemIndex) + " 项须为字符串——收到 " + jsonKindName(item)
							break
						}
						one := strings.TrimSpace(text)
						if one == "" {
							contentError = "content 第 " + strconv.Itoa(itemIndex) + " 项为空——条目不得为空串"
							break
						}
						collected = append(collected, one)
					}
					if contentError == "" {
						contentItems = collected
					}
				}
			}
			if raw, exists := root["force"]; exists {
				var b bool
				if err := json.Unmarshal(raw, &b); err == nil && b {
					force = true
				}
			}
		}
	}
	// [段1] 形态校验（A129——多值参数一律原生数组；非数组 / 非字符串元素 / 空串元素一律出声）
	if contentError != "" {
		logAt(LogModuleSession, 2, "Note 参数形态非法——"+contentError, LogCategorySys)
		return "ERR|BAD_ARGS|Note content 形态非法——" + contentError + "；正确形态 content=[\"任务1\",\"任务2\"]"
	}

	n.mu.Lock()
	var result string
	if action == "set" && len(contentItems) > 0 {
		// [段2] set——写入新计划（未完成时无 force 拒绝覆盖）
		oldRemain := 0
		if len(n.tasks) > 0 && n.current < len(n.tasks) {
			oldRemain = len(n.tasks) - n.current
		}
		if oldRemain > 0 && !force {
			result = "[Note] ⚠️ 还有 " + strconv.Itoa(oldRemain) + " 条未完成。用 force=true 强制覆盖，或用 Note() 继续推进。"
		} else {
			n.tasks = contentItems
			n.current = 0
			n.done = 0
			n.justCompleted = false
			result = n.buildProgressLocked()
		}
	} else {
		// [段3] 无参数——推进一条
		if len(n.tasks) > 0 && n.current < len(n.tasks) {
			n.done++
			n.current++
		}
		// [段4] 全部完成——清空内存
		if len(n.tasks) > 0 && n.current >= len(n.tasks) {
			total := len(n.tasks)
			n.tasks = nil
			n.current = 0
			n.done = 0
			n.justCompleted = true
			result = metaHead("Note", true, "", total,
				[]metaField{{"state", "done"}},
				"[Note] 🎉 全部 "+strconv.Itoa(total)+" 条任务已完成！")
		} else {
			result = n.buildProgressLocked()
		}
	}
	n.mu.Unlock()
	n.notify()
	return result
}

// buildProgressLocked 进度反馈文本（调用方持锁）——无计划 / 进度 / 末条提示。
func (n *NoteTracker) buildProgressLocked() string {
	if len(n.tasks) == 0 {
		return metaHead("Note", true, "", -1,
			[]metaField{{"state", "empty"}},
			"[Note] 暂无计划。用 action=set + content=任务1\\n任务2 来创建。")
	}
	// [段1] 待完成口径——总数 - 已完成（含当前未完成条）：已完成 + 待完成 == 总数
	remain := len(n.tasks) - n.done
	msg := "[Note] 第" + strconv.Itoa(n.current+1) + "/" + strconv.Itoa(len(n.tasks)) +
		"条  已完成" + strconv.Itoa(n.done) + "  待完成" + strconv.Itoa(remain) +
		"\n任务目标：" + n.tasks[n.current]
	// [段2] 末条提示——当前即最后一条
	last := false
	if n.current+1 >= len(n.tasks) {
		msg += "（已是最后一条需求，完成后可结束本轮）"
		last = true
	}
	return metaHead("Note", true, "", len(n.tasks),
		[]metaField{
			{"state", "progress"},
			{"index", n.current + 1},
			{"done", n.done},
			{"remain", remain},
			{"last", last},
		}, msg)
}

// Add 手动新增任务——追加到末尾；空计划时创建（前端 note.add 指令执行体）。
func (n *NoteTracker) Add(text string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	n.mu.Lock()
	n.tasks = append(append([]string{}, n.tasks...), t)
	n.justCompleted = false
	n.mu.Unlock()
	n.notify()
}

// Start 拼接计划全文 + 当前进度，以系统注入名义推给 LLM 开始执行（前端 note.start 指令执行体）。
//
// post 为系统注入口（会话待处理队列）；nil = 无投递面（零动作）。
func (n *NoteTracker) Start(post func(kind, content, imagesJSON string) string) {
	n.mu.Lock()
	if len(n.tasks) == 0 {
		n.mu.Unlock()
		return
	}
	var b strings.Builder
	b.WriteString("[Note 计划] 共 ")
	b.WriteString(strconv.Itoa(len(n.tasks)))
	b.WriteString(" 条，当前第 ")
	b.WriteString(strconv.Itoa(n.current + 1))
	b.WriteString(" 条：\n")
	for i, task := range n.tasks {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(task)
		b.WriteString("\n")
	}
	b.WriteString("请从当前任务开始逐条执行，每条完成后调用 Note 推进。")
	text := b.String()
	total := len(n.tasks)
	current := n.current + 1
	n.mu.Unlock()
	if post == nil {
		return
	}
	post(SysKindSystemAuto, text, "")
	logAt(LogModuleHost, 1, "Note 启动：共 "+strconv.Itoa(total)+" 条任务，当前第 "+strconv.Itoa(current)+" 条", LogCategoryChat)
}

// BuildJSON 构建状态段 note 字段 JSON——{tasks,current,done,justCompleted}。
func (n *NoteTracker) BuildJSON() string {
	n.mu.Lock()
	tasks := make([]string, len(n.tasks))
	copy(tasks, n.tasks)
	current := n.current
	done := n.done
	justCompleted := n.justCompleted
	n.mu.Unlock()
	var b strings.Builder
	b.WriteString(`{"tasks":[`)
	for i, task := range tasks {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonStr(task))
	}
	b.WriteString(`],"current":`)
	b.WriteString(strconv.Itoa(current))
	b.WriteString(`,"done":`)
	b.WriteString(strconv.Itoa(done))
	b.WriteString(`,"justCompleted":`)
	if justCompleted {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteByte('}')
	return b.String()
}

// Snapshot 读取持久化形态——任务列表 / 当前索引 / 完成数（空计划 = 空数组）。
func (n *NoteTracker) Snapshot() (tasks []string, current, done int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.tasks))
	copy(out, n.tasks)
	return out, n.current, n.done
}

// Clear 清空计划——回滚复位（状态段下一帧按空计划输出；无计划即「暂无计划」）。
func (n *NoteTracker) Clear() {
	n.mu.Lock()
	n.tasks = nil
	n.current = 0
	n.done = 0
	n.justCompleted = false
	n.mu.Unlock()
	n.notify()
}

// Restore 恢复落盘 Note——启动恢复（空计划不改内存态；索引越界夹取）。
func (n *NoteTracker) Restore(tasks []string, current, done int) {
	if len(tasks) == 0 {
		return
	}
	if current < 0 {
		current = 0
	}
	if current >= len(tasks) {
		current = len(tasks) - 1
	}
	n.mu.Lock()
	n.tasks = append([]string{}, tasks...)
	n.current = current
	n.done = done
	n.mu.Unlock()
}

// jsonKindName 值类型名——对齐 System.Text.Json 的 JsonValueKind 文案（错误面逐字一致）。
func jsonKindName(value any) string {
	switch value.(type) {
	case nil:
		return "Null"
	case bool:
		if value.(bool) {
			return "True"
		}
		return "False"
	case string:
		return "String"
	case float64, int, int64, json.Number:
		return "Number"
	case []any:
		return "Array"
	case map[string]any:
		return "Object"
	}
	return "Undefined"
}
