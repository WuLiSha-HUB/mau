package toolpool

import (
	"encoding/json"
	"strings"
)

// Brief 是工具用法摘要——漂移新增工具条目（name / desc / args）。
type Brief struct {
	// Name 工具名
	Name string `json:"name"`
	// Desc 描述首句（≤60 字）
	Desc string `json:"desc"`
	// Args 参数键摘要（必填标 *）
	Args string `json:"args"`
}

// Drift 是工具面漂移——注入面（会话快照）与授权面（实时名单）的差集。
type Drift struct {
	// Added 新增可用（授权面有、注入面没有，且三面全过）
	Added []Brief `json:"added"`
	// Removed 已失效（注入面有、授权面没有）
	Removed []string `json:"removed"`
}

// Empty 判定漂移是否为空（空 = 不输出该段）。
func (d *Drift) Empty() bool {
	return d == nil || (len(d.Added) == 0 && len(d.Removed) == 0)
}

// ComputeDrift 计算工具面漂移。
//
// declared = 会话注入面快照（DeclaredToolNames）；authorized = 当前授权集（实时）。
// added 需三面判定全过（特权面 / 池校验 / 授权集）——不能用 = 不报。
func ComputeDrift(reg *Registry, declared, authorized []string, isAllowed func(string) bool) *Drift {
	drift := &Drift{Added: make([]Brief, 0, 2), Removed: make([]string, 0, 2)}
	for _, name := range authorized {
		if containsName(declared, name) {
			continue
		}
		if isAllowed != nil && !isAllowed(name) {
			continue
		}
		drift.Added = append(drift.Added, BuildToolBrief(reg, name))
	}
	for _, name := range declared {
		if !containsName(authorized, name) {
			drift.Removed = append(drift.Removed, name)
		}
	}
	return drift
}

// BuildToolBrief 构建工具用法摘要——数据源 = 注册表声明。
//
// 注册表缺失 / 参数 JSON 不可析 → 退化为仅名字（未知工具仍可见，不静默丢条目）。
func BuildToolBrief(reg *Registry, name string) Brief {
	brief := Brief{Name: name}
	entry, ok := reg.Find(name)
	if !ok {
		return brief
	}
	brief.Desc = FirstSentence(entry.Spec.Description, 60)
	brief.Args = BuildArgKeys(entry.Spec.ParametersJSON)
	return brief
}

// FirstSentence 取描述首句——截到首个句读（。；换行 ". "）；超出上限按字符截断加省略号。
func FirstSentence(text string, max int) string {
	line := strings.TrimSpace(text)
	if line == "" {
		return ""
	}
	cut := -1
	for _, stop := range []string{"。", "；", "\n", ". "} {
		if idx := strings.Index(line, stop); idx >= 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut >= 0 {
		line = strings.TrimSpace(line[:cut])
	}
	runes := []rune(line)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return line
}

// BuildArgKeys 参数键摘要——从工具声明 parameters JSON 提取属性名（required 成员标 *）。
//
// 无参 / 不可析 = 空串。属性按声明序拼接（「、」分隔）。
func BuildArgKeys(parametersJSON string) string {
	if parametersJSON == "" {
		return ""
	}
	var root struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(parametersJSON), &root); err != nil {
		return ""
	}
	if len(root.Properties) == 0 {
		return ""
	}
	// 属性声明序——用原始文本扫描还原（encoding/json 的 map 无序）
	order := propertiesOrder(parametersJSON)
	var b strings.Builder
	for _, name := range order {
		if b.Len() > 0 {
			b.WriteString("、")
		}
		b.WriteString(name)
		if containsName(root.Required, name) {
			b.WriteString("*")
		}
	}
	return b.String()
}

// propertiesOrder 扫描 parameters JSON，按出现序提取 properties 的键名。
//
// 只服务摘要展示（畸形 JSON 返回空——调用方按空处理）。
func propertiesOrder(parametersJSON string) []string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(parametersJSON), &doc); err != nil {
		return nil
	}
	propsRaw, ok := doc["properties"]
	if !ok || len(propsRaw) == 0 || propsRaw[0] != '{' {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(propsRaw)))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil
	}
	order := make([]string, 0, 4)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return order
		}
		key, ok := keyTok.(string)
		if !ok {
			return order
		}
		order = append(order, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return order
		}
	}
	return order
}
