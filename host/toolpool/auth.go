package toolpool

import "strings"

// BlockedToolNames 是受限工具名单（硬编码，非配置）——任何写入路径都不得把这些工具「赋予」目标猫。
//
// 第一批（2026-11-09）：group-catcfg-set（组内配置写）/ group-newsession（会话销毁）。
// 语义边界：只拦「新增」——现值已持有的保留（否则一次无关字段保存就会静默夺权）。
var BlockedToolNames = []string{"group-catcfg-set", "group-newsession"}

// IsDefaultCatKey 默认会话判定——特权工具永久激活基准（猫 key = majordomo / 空）。
func IsDefaultCatKey(key string) bool {
	if key == "" {
		return true
	}
	return key == "majordomo"
}

// IsBlockedToolName 受限工具名判定。
func IsBlockedToolName(name string) bool { return containsName(BlockedToolNames, name) }

// SplitToolNameList 拆分工具名单串（逗号 / 空格 / 制表分隔去空项）。
func SplitToolNameList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// IsPrivilegedName 特权工具判定——读注册面组级标记（未注册 = 非特权）。
func (r *Registry) IsPrivilegedName(name string) bool {
	entry, ok := r.Find(name)
	if !ok {
		return false
	}
	return entry.Privileged
}

// GetAllPrivilegedToolNames 返回特权工具全量名（默认会话永久激活集来源）。
func (r *Registry) GetAllPrivilegedToolNames() []string {
	out := make([]string, 0, 4)
	for _, name := range r.GetAllNames() {
		if r.IsPrivilegedName(name) {
			out = append(out, name)
		}
	}
	return out
}

// GetConfigurableToolNames 返回可配置工具名清单（全量剔除特权工具）。
func (r *Registry) GetConfigurableToolNames() []string {
	out := make([]string, 0, 128)
	for _, name := range r.GetAllNames() {
		if !r.IsPrivilegedName(name) {
			out = append(out, name)
		}
	}
	return out
}

// FallbackConfigurableNames 返回全量保底面（受限名单工具不参与全量授予）。
//
// 消费面：ResolveConfigurableNames 的三处全量保底（raw 空 / "*" / 过滤后全空）。
func (r *Registry) FallbackConfigurableNames() []string {
	all := r.GetConfigurableToolNames()
	out := make([]string, 0, len(all))
	for _, name := range all {
		if !IsBlockedToolName(name) {
			out = append(out, name)
		}
	}
	return out
}

// ResolveConfigurableNames 名单解析（可配置口径）——空 / "*" / 过滤后全空 → 全量保底。
func (r *Registry) ResolveConfigurableNames(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return r.FallbackConfigurableNames()
	}
	all := r.GetConfigurableToolNames()
	names := make([]string, 0, len(all))
	for _, part := range SplitToolNameList(raw) {
		name := strings.TrimSpace(part)
		if name == "*" {
			return r.FallbackConfigurableNames()
		}
		if containsName(all, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return r.FallbackConfigurableNames()
	}
	return names
}

// AppendPrivilegedNames 特权工具并入——默认会话永久激活（去重并入；其他会话原样返回）。
func (r *Registry) AppendPrivilegedNames(names []string, includePrivileged bool) []string {
	if !includePrivileged {
		return names
	}
	out := make([]string, 0, len(names)+4)
	out = append(out, names...)
	for _, name := range r.GetAllPrivilegedToolNames() {
		if !containsName(names, name) {
			out = append(out, name)
		}
	}
	return out
}

// ResolveToolNames 工具名单解析（含特权并入）——默认会话永久激活口径。
func (r *Registry) ResolveToolNames(raw string, includePrivileged bool) []string {
	return r.AppendPrivilegedNames(r.ResolveConfigurableNames(raw), includePrivileged)
}

// FilterToolSpecs 工具声明面裁剪——按名单从全量表取子集；名单全非法 → 全量保底。
//
// 特权组按 includePrivileged 处置（默认会话永久激活 / 其他会话恒剔除）。
func (r *Registry) FilterToolSpecs(names []string, includePrivileged bool) []Def {
	all := r.BuildSpecs()
	list := make([]Def, 0, len(all))
	for _, spec := range all {
		if r.IsPrivilegedName(spec.Name) {
			if includePrivileged {
				list = append(list, spec)
			}
			continue
		}
		if containsName(names, spec.Name) {
			list = append(list, spec)
		}
	}
	if len(list) == 0 {
		fallback := make([]Def, 0, len(all))
		for _, spec := range all {
			if includePrivileged || !r.IsPrivilegedName(spec.Name) {
				fallback = append(fallback, spec)
			}
		}
		return fallback
	}
	return list
}

// ResolveCatAuthorizedFromCfg 按猫的 cat.cfg 原始名单解析授权集。
//
// 与 C# ResolveCatAuthorizedToolNames 同语义：catKey 为空 / majordomo 时用
// defaultRaw（默认猫名单）；其余按 catRaw（该猫名单）。
func (r *Registry) ResolveCatAuthorizedFromCfg(catKey, defaultRaw, catRaw string) []string {
	raw := defaultRaw
	if !IsDefaultCatKey(catKey) {
		raw = catRaw
	}
	return r.ResolveToolNames(raw, IsDefaultCatKey(catKey))
}

// ResolveStaleToolNames 返回清单里已失效的工具名（cat.cfg 含而池无）。
func (r *Registry) ResolveStaleToolNames(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	all := r.GetAllNames()
	stale := make([]string, 0, 4)
	for _, part := range SplitToolNameList(raw) {
		name := strings.TrimSpace(part)
		if name == "" || name == "*" {
			continue
		}
		if !containsName(all, name) {
			stale = append(stale, name)
		}
	}
	return stale
}
