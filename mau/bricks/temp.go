package bricks

// TEMP 两件积木——TempToolCat 工具执行面（对齐 C# BRIK-TEMP-001 / BRIK-TEMP-002）。
//
// 语义：临时工具注册表（Key → handler str→str）+ 双入口积木——
// temp.exec（按 Key 分发执行）· temp.keys（枚举当前可用 Key 组）。
// C# 侧 TempRegistry 是静态空字典（纯净就绪态）——生产注册表保持空；
// 新增临时工具即 Register 一行 + 写一个 handler（str→str），调度骨架零改动。
// 【迁移转正】handler 纯逻辑 → 独立积木（public bool Xxx(string argsJson, out string result)）；
// 认领线 → 复制 TempToolCat 语料模板换工具名；声明 → BuildToolSpecs 加 ToolSpec；注册表 → 工具池登记。
//
// 回执形态对齐 A214：首行 JSON 头（ok / tool / target / items + 专有字段）+ 正文摘要行 + 载荷。
// 失败面：参数面 → ERR|BAD_ARGS|（与 C# JsonArgs 同文案）；缺 key → ERR|TEMP_BAD_ARGS|缺少参数 key；
// 未知 key → ERR|TEMP_KEY_NOT_FOUND|可用Key: …；handler 异常 → ERR|TEMP_EXEC|<类型>|<消息>。
//
// 已知语言差异（不重推）：C# Dictionary 遍历按插入序，Go map 无序——ListKeys 按字典序输出（确定性优先）；
// handler 异常类型名取 Go 的 %T（C# 取 ex.GetType().Name）——文案形态同构，类型名属方言。

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// TempRegistry 是临时工具注册表——Key → handler（str→str）【LLM 可改区】唯一载体。
type TempRegistry struct {
	// mu 读写锁——handler 在 par 后台线程执行，注册表读取需线程安全
	mu sync.RWMutex
	// tools Key → handler（空表 = 暂无临时工具）
	tools map[string]func(string) string
}

// NewTempRegistry 构造空注册表。
func NewTempRegistry() *TempRegistry {
	return &TempRegistry{tools: map[string]func(string) string{}}
}

// Register 登记 / 覆盖一个临时工具 handler。
//
// Key 建议用未来独立工具名的连字符形式，迁移时同名转正；handler 只收 content（str→str）。
func (r *TempRegistry) Register(key string, handler func(string) string) {
	if r == nil || handler == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = map[string]func(string) string{}
	}
	r.tools[key] = handler
}

// Find 按 Key 查 handler——不存在返回 (nil, false)。
func (r *TempRegistry) Find(key string) (func(string) string, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.tools[key]
	return fn, ok
}

// ListKeys Key 组枚举——逗号分隔（空 = 暂无临时工具）；按字典序输出（Go map 无序——确定性优先）。
func (r *TempRegistry) ListKeys() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.tools))
	for key := range r.tools {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// DefaultTempRegistry 是宿主默认临时工具注册表——生产空表（纯净就绪态）。
//
// 新增临时工具即 Register 一行 + 写一个 handler（str→str）；迁移转正后移除。
var DefaultTempRegistry = NewTempRegistry()

// TempExec 执行 temp.exec——参数面校验 → 注册表分发 → A214 回执（成功 / 失败均带文本）。
func TempExec(reg *TempRegistry, argsJSON string) (string, bool) {
	badArgs := Validate(argsJSON, "key content", "key content", "", "")
	if badArgs != "" {
		return badArgs, false
	}
	key := Get(argsJSON, "key")
	if key == "" {
		return "ERR|TEMP_BAD_ARGS|缺少参数 key", false
	}
	fn, ok := reg.Find(key)
	if !ok {
		return "ERR|TEMP_KEY_NOT_FOUND|可用Key: " + reg.ListKeys(), false
	}
	content := Get(argsJSON, "content")
	body, execErr := runTempHandler(fn, content)
	if execErr != "" {
		return execErr, false
	}
	// 结构化返回体（design-ch4-tools 附录 · A214）——首行 JSON 头（target + 专有 chars）+ 正文摘要行 + 载荷
	chars := utf16Length(body)
	head := MetaHeadFields("temp-exec", true, key, -1, [][2]any{{"chars", chars}})
	if body == "" {
		return head, true
	}
	return head + "\n" + key + " | " + itoa(chars) + " 字\n" + body, true
}

// TempKeys 执行 temp.keys——枚举当前可用临时工具 Key 组（A214：items 计数 + keys 数组 + 摘要行）。
func TempKeys(reg *TempRegistry) (string, bool) {
	list := reg.ListKeys()
	parts := []string{}
	if list != "" {
		parts = strings.Split(list, ",")
	}
	head := MetaHeadFields("temp-info", true, "", len(parts), [][2]any{{"keys", parts}})
	if list == "" {
		return head, true
	}
	return head + "\n" + itoa(len(parts)) + " 个 Key\n" + list, true
}

// runTempHandler 调用 handler 并捕获异常——C# try/catch 的 Go 对应（panic → ERR|TEMP_EXEC|）。
func runTempHandler(fn func(string) string, content string) (body, errText string) {
	defer func() {
		if r := recover(); r != nil {
			errText = "ERR|TEMP_EXEC|" + fmt.Sprintf("%T", r) + "|" + fmt.Sprint(r)
		}
	}()
	return fn(content), ""
}

// utf16Length 按 UTF-16 码元计长——对齐 C# string.Length（emoji 等非 BMP 字符计 2）。
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}
