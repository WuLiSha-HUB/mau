package toolpool

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// Def 是工具定义条目——工具池的原子形态。
type Def struct {
	// Name 工具名（唯一键）
	Name string
	// Description 语义化描述（LLM 面——OpenAI function description）
	Description string
	// ParametersJSON 参数 JSON Schema（OpenAI 原样透传；空 = 无参工具）
	ParametersJSON string
	// Group 归属组（TextCat 等；内置 = ""）
	Group string
	// Privileged 组级特权声明——true = 仅主干会话可见可调
	Privileged bool
}

// Defect 是工具组定义缺陷条目——定义自曝链路的失败记录（A107）。
type Defect struct {
	// Group 组名（组 Flow 名 / 内置源）
	Group string
	// Stage 缺陷阶段——throw / parse / shape / empty
	Stage string
	// Reason 原因（日志与 info 直读，不再二次解释）
	Reason string
}

// GroupSource 是工具组定义来源——组名 + 定义 JSON（A107：失败时能报出是哪一组）。
type GroupSource struct {
	// Name 组名
	Name string
	// JSON 工具定义 JSON（{"group":"X","tools":[...]}）
	JSON string
}

// Pool 是工具池快照——三索引一次成型（写侧局部构建 → 整体换引用）。
type Pool struct {
	// byName 按名索引（重名覆盖：后注册优先）
	byName map[string]*Def
	// order 插入序（All / BuildSpecs 确定性：注册序 = 扫描添加序）
	order []*Def
	// byGroup 按组索引
	byGroup map[string][]*Def
	// defects 缺陷清单（与池同代，换代即清零）
	defects []Defect
	// produced 已产出定义的组名（成功取到 tools 数组即计入）
	produced []string
}

// newPool 构造空池。
func newPool() *Pool {
	return &Pool{
		byName:  make(map[string]*Def, 128),
		order:   make([]*Def, 0, 128),
		byGroup: make(map[string][]*Def, 24),
	}
}

// current 是当前池快照（唯一可变引用，写侧整体替换）。
var current atomic.Pointer[Pool]

// currentRegistry 是当前工具注册表快照（与池同代——整体换引用，读侧零锁）。
var currentRegistry atomic.Pointer[Registry]

// CurrentRegistry 返回当前注册表快照（永不为 nil——首次访问给空表）。
func CurrentRegistry() *Registry {
	if r := currentRegistry.Load(); r != nil {
		return r
	}
	return &Registry{entries: map[string]Entry{}, order: []string{}}
}

// StoreRegistry 原子换注册表引用——工具面热重载的唯一写入口。
func StoreRegistry(reg *Registry) {
	if reg == nil {
		return
	}
	currentRegistry.Store(reg)
}

// Current 返回当前池快照（永不为 nil；首次访问给空池）。
func Current() *Pool {
	if p := current.Load(); p != nil {
		return p
	}
	return newPool()
}

// RebuildAll 全量重建工具池——唯一写入口：局部构建新快照 → 原子换引用。
//
// 读者要么看到全旧、要么看到全新（无半更新窗口）；不在新表的工具随旧快照淘汰。
func RebuildAll(groups []GroupSource, builtinJSON string) {
	next := newPool()
	for _, group := range groups {
		addFromJSON(next, group.JSON, group.Name)
	}
	addFromJSON(next, builtinJSON, "内置")
	for _, group := range groups {
		noteEmptyIfSilent(next, group.Name)
	}
	// order 注记注入——工具执行序表作为标准注释追加到描述尾行（单点 = 全池生效）
	for _, def := range next.order {
		note := NoteLine(def.Name)
		if def.Description == "" {
			def.Description = note
			continue
		}
		def.Description = def.Description + "\n" + note
	}
	current.Store(next)
}

// addFromJSON 解析工具定义 JSON 并加入目标快照。
//
// 四条静默路径全部出声（空串 / 缺 tools / 结构不符 / 解析异常）。
func addFromJSON(target *Pool, raw string, groupHint string) {
	if raw == "" {
		target.defects = append(target.defects, Defect{Group: groupHint, Stage: "shape", Reason: "工具定义 JSON 为空"})
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		target.defects = append(target.defects, Defect{Group: groupHint, Stage: "parse", Reason: "工具定义 JSON 非法: " + err.Error()})
		return
	}
	group := ""
	if el, ok := fields["group"]; ok {
		_ = json.Unmarshal(el, &group)
	}
	privileged := false
	if el, ok := fields["privileged"]; ok {
		_ = json.Unmarshal(el, &privileged)
	}
	// tools 字段存在性判定——必须是数组；显式空组声明 tools:[] 合法，不判缺陷
	toolsRaw, ok := fields["tools"]
	if !ok || len(toolsRaw) == 0 || toolsRaw[0] != '[' {
		target.defects = append(target.defects, Defect{Group: groupHint, Stage: "shape", Reason: "工具定义缺 tools 数组"})
		return
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		target.defects = append(target.defects, Defect{Group: groupHint, Stage: "shape", Reason: "工具定义缺 tools 数组"})
		return
	}
	if !containsName(target.produced, groupHint) {
		target.produced = append(target.produced, groupHint)
	}
	accepted := 0
	for _, item := range tools {
		var one struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(item, &one); err != nil {
			continue
		}
		if one.Name == "" {
			continue
		}
		def := &Def{
			Name:           one.Name,
			Description:    one.Description,
			ParametersJSON: string(one.Parameters),
			Group:          group,
			Privileged:     privileged,
		}
		addTo(target, def)
		accepted++
	}
	if len(tools) > 0 && accepted == 0 {
		target.defects = append(target.defects, Defect{
			Group:  groupHint,
			Stage:  "shape",
			Reason: fmt.Sprintf("工具条目全部无效——%d 条均缺 name 或非对象", len(tools)),
		})
	}
}

// addTo 添加单条目——重名覆盖（保持原插入序）+ 组索引同步。
func addTo(target *Pool, def *Def) {
	if def == nil || def.Name == "" {
		return
	}
	_, exists := target.byName[def.Name]
	target.byName[def.Name] = def
	if !exists {
		target.order = append(target.order, def)
	} else {
		for i, item := range target.order {
			if item.Name == def.Name {
				target.order[i] = def
				break
			}
		}
	}
	group := def.Group
	list := target.byGroup[group]
	replaced := false
	for i, item := range list {
		if item.Name == def.Name {
			list[i] = def
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, def)
	}
	target.byGroup[group] = list
}

// noteEmptyIfSilent 空产出兜底登记——组已加载却零工具入池，且此前无更具体缺陷时记 empty。
func noteEmptyIfSilent(target *Pool, groupName string) {
	if groupName == "" {
		return
	}
	for _, defect := range target.defects {
		if defect.Group == groupName {
			return
		}
	}
	if containsName(target.produced, groupName) {
		return
	}
	target.defects = append(target.defects, Defect{
		Group:  groupName,
		Stage:  "empty",
		Reason: "工具组已加载但工具定义零产出——池内无本组工具",
	})
}

// All 返回全量工具定义（注册序）。
func (p *Pool) All() []*Def {
	out := make([]*Def, len(p.order))
	copy(out, p.order)
	return out
}

// AllNames 返回全量工具名（配置界面名称清单）。
func (p *Pool) AllNames() []string {
	names := make([]string, 0, len(p.order))
	for _, def := range p.order {
		names = append(names, def.Name)
	}
	return names
}

// ByGroup 按组取工具（组空 = 内置组；不存在返回空数组）。
func (p *Pool) ByGroup(group string) []*Def {
	list := p.byGroup[group]
	out := make([]*Def, len(list))
	copy(out, list)
	return out
}

// AllGroups 返回全部组名（按首次出现序，含内置空组）。
func (p *Pool) AllGroups() []string {
	groups := make([]string, 0, 8)
	for _, def := range p.order {
		if !containsName(groups, def.Group) {
			groups = append(groups, def.Group)
		}
	}
	return groups
}

// Defects 返回缺陷清单（无缺陷 = 空数组）。
func (p *Pool) Defects() []Defect {
	out := make([]Defect, len(p.defects))
	copy(out, p.defects)
	return out
}

// BuildSpecs 构建声明表（Def 投影，注册序）。
func (p *Pool) BuildSpecs() []Def {
	out := make([]Def, 0, len(p.order))
	for _, def := range p.order {
		out = append(out, *def)
	}
	return out
}

// BuildOwnerFlowMap 构建工具名 → 组名映射（注册表路由表数据源）。
func (p *Pool) BuildOwnerFlowMap() map[string]string {
	m := make(map[string]string, len(p.byName))
	for name, def := range p.byName {
		m[name] = def.Group
	}
	return m
}

// BuildPrivilegedMap 构建工具名 → 特权标记映射（组级声明派生）。
func (p *Pool) BuildPrivilegedMap() map[string]bool {
	m := make(map[string]bool, len(p.byName))
	for name, def := range p.byName {
		m[name] = def.Privileged
	}
	return m
}

// MemberCount 返回池内工具数（自证用）。
func (p *Pool) MemberCount() int { return len(p.order) }

// String 给出池摘要（日志 / 报告用）。
func (p *Pool) String() string {
	return fmt.Sprintf("pool(%d tools, %d groups, %d defects)", len(p.order), len(p.byGroup), len(p.defects))
}

// trimBOM 剥 UTF-8 BOM（Bricks 源码与 JSON 可能带）。
func trimBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}
