package observe

import (
	"sort"
	"time"
)

// patchState 是增量 diff 缓存——Pump 单 goroutine 独占（对齐 C# 主线程泵语义：
// 缓存字段只由快照节拍写，HTTP 线程不触碰）。
type patchState struct {
	// catsJSON 上次 cats 段 JSON（Flow 注册表整段比对）
	catsJSON string
	// sessionsJSON 上次 sessions 段 JSON（会话状态整段比对）
	sessionsJSON string
	// depsJSON 上次 deps 段稳定视图 JSON（不含 probedAt）
	depsJSON string
	// oaJSON 上次 oa 段 JSON（四计数比对）
	oaJSON string
	// boxes 上次 boxes 条目（scope+key → 条目 JSON；字典 diff 基线）
	boxes map[string]string
	// lastResPush 上次 res 段推送时刻（零值 = 未推——首帧即带；1 秒节拍强制刷新）
	lastResPush time.Time
}

// depStableItem 是 deps 段稳定视图条目——probedAt 不进对比面。
//
// C# 同口径（ObserveService.BuildDepsJson）：依赖探测的 probedAt 每轮变化，
// 混入 diff 面即每帧误推；对比只用 level/name/state/detail。
type depStableItem struct {
	// Level 级别（required / optional / dev）
	Level string `json:"level"`
	// Name 显示名
	Name string `json:"name"`
	// State 状态（ok / unconfigured / missing / unusable）
	State string `json:"state"`
	// Detail 细节
	Detail string `json:"detail"`
}

// boxRef 是 boxes patch 的删除引用（scope + key）。
type boxRef struct {
	// Scope 盒子域
	Scope string `json:"scope"`
	// Key 盒子键
	Key string `json:"key"`
}

// boxesPatch 是 boxes 段的字典 diff——新增/变化进 set，消失进 del。
type boxesPatch struct {
	// Set 新增或变化的条目（完整条目对象）
	Set []Box `json:"set"`
	// Del 消失的条目引用
	Del []boxRef `json:"del"`
}

// patchPayload 是增量 patch 载荷——段按需携带（nil = 未变化，omitempty 省略）；
// 键序对齐 C# BuildPatchJson 的插入序：frame / cats / sessions / deps / res / oa / boxes。
type patchPayload struct {
	// Frame 当前全局帧号
	Frame int64 `json:"frame"`
	// Cats Flow 注册表段（整段替换）
	Cats *[]Cat `json:"cats,omitempty"`
	// Sessions 会话状态段（整段替换）
	Sessions *[]any `json:"sessions,omitempty"`
	// Deps 依赖探测段（整段替换；含 probedAt）
	Deps *[]DepResult `json:"deps,omitempty"`
	// Res 资源占用段（1 秒节拍强制携带）
	Res *Res `json:"res,omitempty"`
	// OA 工单四态计数（整段替换）
	OA *OA `json:"oa,omitempty"`
	// Boxes DataBox 字典 diff
	Boxes *boxesPatch `json:"boxes,omitempty"`
}

// BuildPatch 构建增量 patch JSON；全无变化返回空串（零推送——Idle 稳态静默）。
//
// 口径对齐 C# ObserveService.BuildPatchJson：
//   - cats / sessions —— 整段序列化比对（状态转移才变，频率低）
//   - deps —— 稳定视图比对（probedAt 不参与 diff）
//   - res —— 固定 1 秒节拍强制携带（内存数字每秒在变，不参与内容比对）
//   - oa —— 四计数比对
//   - boxes —— 字典 diff（scope+key → 条目；新增/变化进 set，消失进 del）
//
// 主线程泵调用（Pump 单 goroutine）；缓存字段由本方法独占。
func (b *Builder) BuildPatch() string {
	st := &b.patch
	// [段1] cats —— 整段序列化对比
	cats := b.buildCats()
	catsJSON := marshalJSON(cats)
	catsChanged := catsJSON != st.catsJSON
	// [段1b] sessions —— 整段序列化对比（会话四相环 / 轮次 / 消息数变化推送）
	sessions := b.buildSessions()
	sessionsJSON := marshalJSON(sessions)
	sessionsChanged := sessionsJSON != st.sessionsJSON
	// [段1c] deps —— 稳定视图对比（probedAt 不进对比面）
	deps := b.buildDeps()
	depsJSON := depsStableJSON(deps)
	depsChanged := depsJSON != st.depsJSON
	// [段1d] res —— 固定 1 秒节拍强制带（打破 Idle 稳态的零推送静默）
	var resObj *Res
	resDue := st.lastResPush.IsZero() || time.Since(st.lastResPush) >= time.Second
	if resDue {
		captured := CaptureRes()
		resObj = &captured
	}
	// [段2] oa —— 四计数对比
	oa := b.buildOA()
	oaJSON := marshalJSON(oa)
	oaChanged := oaJSON != st.oaJSON
	// [段3] boxes —— 字典 diff（scope+key → 条目 JSON；内部盒已由 buildBoxes 过滤）
	boxesNow := make(map[string]string)
	boxesNowByKey := make(map[string]Box)
	for _, box := range b.buildBoxes() {
		entryKey := box.Scope + "\u0001" + box.Key
		boxesNow[entryKey] = marshalJSON(box)
		boxesNowByKey[entryKey] = box
	}
	boxSet := make([]Box, 0)
	for _, entryKey := range sortedKeys(boxesNow) {
		old, exists := st.boxes[entryKey]
		if !exists || old != boxesNow[entryKey] {
			boxSet = append(boxSet, boxesNowByKey[entryKey])
		}
	}
	boxDel := make([]boxRef, 0)
	for _, entryKey := range sortedKeys(st.boxes) {
		if _, exists := boxesNow[entryKey]; exists {
			continue
		}
		boxDel = append(boxDel, splitBoxKey(entryKey))
	}
	// [段4] 无变化零推送
	if !catsChanged && !sessionsChanged && !depsChanged && !oaChanged && len(boxSet) == 0 && len(boxDel) == 0 && !resDue {
		return ""
	}
	// [段5] patch 组装 + 缓存更新
	patch := patchPayload{Frame: b.currentFrame()}
	if catsChanged {
		patch.Cats = &cats
		st.catsJSON = catsJSON
	}
	if sessionsChanged {
		patch.Sessions = &sessions
		st.sessionsJSON = sessionsJSON
	}
	if depsChanged {
		patch.Deps = &deps
		st.depsJSON = depsJSON
	}
	if resDue {
		patch.Res = resObj
		st.lastResPush = time.Now()
	}
	if oaChanged {
		patch.OA = &oa
		st.oaJSON = oaJSON
	}
	if len(boxSet) > 0 || len(boxDel) > 0 {
		patch.Boxes = &boxesPatch{Set: boxSet, Del: boxDel}
		st.boxes = boxesNow
	}
	return marshalJSON(patch)
}

// depsStableJSON 构建 deps 段稳定视图 JSON（去 probedAt——对比面）。
func depsStableJSON(deps []DepResult) string {
	stable := make([]depStableItem, 0, len(deps))
	for _, dep := range deps {
		stable = append(stable, depStableItem{
			Level:  dep.Level,
			Name:   dep.Name,
			State:  dep.State,
			Detail: dep.Detail,
		})
	}
	return marshalJSON(stable)
}

// isInternalBox 内部实现盒判定——观测面屏蔽（对齐 C# IsInternalBox）。
//
// log.entries / log.sync 是内部数据结构不入快照；global.frame 是帧号观测元数据
// （每帧变化，混入 diff 会导致每帧误推——快照帧号走顶层 frame 字段）。
func isInternalBox(scope, key string) bool {
	if scope == "log" && (key == "entries" || key == "sync") {
		return true
	}
	if scope == "global" && key == "frame" {
		return true
	}
	return false
}

// sortedKeys 返回字典键的稳定序（map 遍历随机是语言保证，比对与输出需要确定性）。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// splitBoxKey 拆回 scope+key（scope 与 key 由 \u0001 分隔）。
func splitBoxKey(entryKey string) boxRef {
	for i := 0; i < len(entryKey); i++ {
		if entryKey[i] == '\u0001' {
			return boxRef{Scope: entryKey[:i], Key: entryKey[i+1:]}
		}
	}
	return boxRef{Scope: entryKey, Key: ""}
}
