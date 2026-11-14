package toolpool

import "strings"

// Entry 是工具注册表条目——工具名 → 归属 + 声明 + 状态。
type Entry struct {
	// Name 工具名（唯一键）
	Name string
	// Spec 工具声明（Name / Description / ParametersJSON）
	Spec Def
	// OwnerFlow 归属 Flow 名——OA 工具 = 工具组名；内置 = ""
	OwnerFlow string
	// IsBuiltin 是否内置——true = 不走 OA（会话内或宿主直执）
	IsBuiltin bool
	// Privileged 是否特权——仅主干会话可见可调（组级声明派生）
	Privileged bool
}

// Registry 是运行时工具注册表——工具名 → 条目（单一真相源；内置 + OA 双轨）。
type Registry struct {
	// entries 工具名 → 条目
	entries map[string]Entry
	// order 注册序（字典枚举序的等价物——插入序稳定）
	order []string
}

// NewRegistry 用声明表构建注册表。
//
// ownerFlowMap / privilegedMap 为 nil 时回落前缀映射 / 无特权项（C# Init 同语义）。
func NewRegistry(specs []Def, ownerFlowMap map[string]string, privilegedMap map[string]bool) *Registry {
	r := &Registry{entries: make(map[string]Entry, len(specs)), order: make([]string, 0, len(specs))}
	for _, spec := range specs {
		builtin := isBuiltinName(spec.Name)
		ownerFlow := ""
		if group, ok := ownerFlowMap[spec.Name]; ok {
			ownerFlow = group
		} else {
			ownerFlow = ownerFlowFor(spec.Name)
		}
		privileged := false
		if flag, ok := privilegedMap[spec.Name]; ok {
			privileged = flag
		}
		// 内置 host-* 一律特权——宿主级动作（语料热重载 / Flow 观测）跨会话生效，
		// 不允许非主干猫触发（与 restart-* 同一授权口径）。
		if !privileged && builtin && strings.HasPrefix(spec.Name, "host-") {
			privileged = true
		}
		if _, exists := r.entries[spec.Name]; !exists {
			r.order = append(r.order, spec.Name)
		}
		r.entries[spec.Name] = Entry{Name: spec.Name, Spec: spec, OwnerFlow: ownerFlow, IsBuiltin: builtin, Privileged: privileged}
	}
	return r
}

// Find 按名查条目。
func (r *Registry) Find(name string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	entry, ok := r.entries[name]
	return entry, ok
}

// All 返回全量条目（注册序）。
func (r *Registry) All() []Entry {
	if r == nil {
		return nil
	}
	out := make([]Entry, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.entries[name])
	}
	return out
}

// BuildSpecs 返回全量声明表（注册序）。
func (r *Registry) BuildSpecs() []Def {
	if r == nil {
		return nil
	}
	out := make([]Def, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.entries[name].Spec)
	}
	return out
}

// GetAllNames 返回全量工具名（注册序）。
func (r *Registry) GetAllNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// isBuiltinName 内置工具判定——不走 OA 的宿主直执面。
func isBuiltinName(name string) bool {
	switch name {
	case "Note", "time", "random", "info", "pack", "sleep", "timer", "timeback-start", "timeback-back":
		return true
	}
	return strings.HasPrefix(name, "host-")
}

// ownerFlowFor 工具名前缀 → 归属工具组名（未知 = 内置空归属）。
func ownerFlowFor(name string) string {
	switch {
	case strings.HasPrefix(name, "text-"):
		return "TextCat"
	case strings.HasPrefix(name, "mau-"):
		return "MauCat"
	case strings.HasPrefix(name, "cs-"):
		return "CsCat"
	case strings.HasPrefix(name, "config-"):
		return "ConfigCat"
	case strings.HasPrefix(name, "web-"):
		return "SearchCat"
	case strings.HasPrefix(name, "image-"):
		return "VisionCat"
	case strings.HasPrefix(name, "temp-"):
		return "TempToolCat"
	case name == "powershell":
		return "PsCat"
	case strings.HasPrefix(name, "proc-"):
		return "ProcCat"
	}
	return ""
}
