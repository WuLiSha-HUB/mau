package front

import "strings"

// AnalysisResult 是图论分析结果——错误拦截 + 报告。
type AnalysisResult struct {
	Errors  []Diag
	Reports []string
}

// Success 报告分析零错误。
func (r *AnalysisResult) Success() bool {
	return len(r.Errors) == 0
}

// Analyze 做状态机图论验证——E300 无界环 / E301 不可达状态 / E302 失败侧无恢复路径。
//
// 关键路径（SCC 收缩 + 拓扑 DP）写进 Reports，只作排错定位，不拦截。
func Analyze(doc *Doc) *AnalysisResult {
	result := &AnalysisResult{}
	if doc == nil {
		result.Errors = append(result.Errors, Diag{Code: "E300", Line: 1, Msg: "空 IR——无可分析对象"})
		return result
	}
	// [段1] 建图——节点（状态机.状态）+ 边（导线结果转移）+ 外生目标（无状态断言条件的导线目标）
	graph := buildGraph(doc)
	// [段2] 稳定性——无事件子图找环（环上无传感器条件 = 无界环）
	findUnboundedLoops(graph, result)
	// [段3] 可达性——BFS 从初始态 + 外生目标出发
	findUnreachable(graph, result)
	// [段4] 扰动覆盖——失败侧目标必须有出路（出度 ≥1 或终态）
	findNoRecovery(doc, graph, result)
	// [段5] 关键路径——SCC 收缩 → DAG → 拓扑 DP（报告级）
	buildCriticalPathReport(graph, result)
	return result
}

// stateGraph 是状态转移图——节点 / 边 / 初始集 / 外生集 / 出度。
type stateGraph struct {
	nodeIndex map[string]int
	nodeNames []string
	nodeLine  []int
	initial   []string
	extern    []string
	edges     []stateEdge
	outDegree []int
}

// stateEdge 是转移边——源状态 → 目标状态（携带导线 + 事件驱动标记）。
type stateEdge struct {
	from        string
	to          string
	wire        *Wire
	eventDriven bool
}

// buildGraph 构建状态转移图。
func buildGraph(doc *Doc) *stateGraph {
	g := &stateGraph{nodeIndex: make(map[string]int)}
	// [段1] 节点注册——每个状态的唯一 key = "状态机.状态"
	for i := range doc.StateMachines {
		sm := &doc.StateMachines[i]
		for _, st := range sm.States {
			key := sm.Name + "." + st
			g.nodeIndex[key] = len(g.nodeNames)
			g.nodeNames = append(g.nodeNames, key)
			g.nodeLine = append(g.nodeLine, sm.Line)
		}
		if len(sm.States) > 0 {
			g.initial = append(g.initial, sm.Name+"."+sm.States[0])
		}
	}
	// [段2] 边——导线条件（状态断言）→ 结果目标
	for i := range doc.Wires {
		w := &doc.Wires[i]
		hasEvent := hasSensorCondition(w)
		var sources []string
		for _, c := range w.Conditions {
			if c.IsStateAssert {
				sources = append(sources, c.StateName+"."+c.StateValue)
			}
		}
		if len(sources) == 0 {
			// 外生边——无状态断言源（纯传感器触发）——目标状态外生可达
			for _, r := range w.Results {
				target := r.StateName + "." + r.StateValue
				if _, ok := g.nodeIndex[target]; ok {
					g.extern = append(g.extern, target)
				}
			}
			continue
		}
		for _, src := range sources {
			if _, ok := g.nodeIndex[src]; !ok {
				continue
			}
			for _, r := range w.Results {
				target := r.StateName + "." + r.StateValue
				if _, ok := g.nodeIndex[target]; !ok {
					continue
				}
				g.edges = append(g.edges, stateEdge{from: src, to: target, wire: w, eventDriven: hasEvent})
			}
		}
	}
	// [段3] 出度统计——终态判定（出度 0）
	g.outDegree = make([]int, len(g.nodeNames))
	for _, e := range g.edges {
		if idx, ok := g.nodeIndex[e.from]; ok {
			g.outDegree[idx]++
		}
	}
	return g
}

// hasSensorCondition 报告导线是否含传感器条件——事件驱动刹车。
func hasSensorCondition(w *Wire) bool {
	for _, c := range w.Conditions {
		if !c.IsStateAssert && !c.IsBoxAssert {
			return true
		}
	}
	return false
}

// adjacency 构建邻接表——allEdges=false 时只保留无事件边（环检测用）。
func (g *stateGraph) adjacency(allEdges bool) [][]int {
	adj := make([][]int, len(g.nodeNames))
	for _, e := range g.edges {
		if !allEdges && e.eventDriven {
			continue
		}
		from, okFrom := g.nodeIndex[e.from]
		to, okTo := g.nodeIndex[e.to]
		if okFrom && okTo {
			adj[from] = append(adj[from], to)
		}
	}
	return adj
}

// findUnboundedLoops 在无事件子图上找环——环上无传感器事件刹车即 E300。
func findUnboundedLoops(g *stateGraph, result *AnalysisResult) {
	adj := g.adjacency(false)
	color := make([]int, len(g.nodeNames))
	var stack []int
	for i := range g.nodeNames {
		if color[i] == 0 {
			dfsCycle(g, adj, i, color, &stack, result)
		}
	}
}

// dfsCycle 三色 DFS 找环——回边命中即报 E300（环上含积木动作则放行）。
func dfsCycle(g *stateGraph, adj [][]int, node int, color []int, stack *[]int, result *AnalysisResult) {
	color[node] = 1
	*stack = append(*stack, node)
	for _, next := range adj[node] {
		switch color[next] {
		case 0:
			dfsCycle(g, adj, next, color, stack, result)
		case 1:
			// 回边——环
			if cycleHasBrickAction(g, *stack, next, node) {
				// 环上含积木动作——积木执行有副作用，非纯空转——放行
				continue
			}
			cycle := buildCycleText(g, *stack, next)
			result.Errors = append(result.Errors, Diag{Code: "E300", Line: g.nodeLine[node], Msg: "无界环——环上无传感器事件刹车: " + cycle})
		}
	}
	*stack = (*stack)[:len(*stack)-1]
	color[node] = 2
}

// buildCycleText 把环路径文本化。
func buildCycleText(g *stateGraph, stack []int, backTo int) string {
	var b strings.Builder
	started := false
	for _, v := range stack {
		if v == backTo {
			started = true
		}
		if started {
			if b.Len() > 0 {
				b.WriteString(" → ")
			}
			b.WriteString(g.nodeNames[v])
		}
	}
	b.WriteString(" → ")
	b.WriteString(g.nodeNames[backTo])
	return b.String()
}

// cycleHasBrickAction 报告环上是否含积木动作——含则放行（副作用非纯空转）。
func cycleHasBrickAction(g *stateGraph, stack []int, backTo, node int) bool {
	// 回边 node → backTo
	if edgeHasAction(g, g.nodeNames[node], g.nodeNames[backTo]) {
		return true
	}
	// 栈路径 backTo → ... → node 的相邻对
	for i, v := range stack {
		if v != backTo {
			continue
		}
		for j := i; j < len(stack)-1; j++ {
			if edgeHasAction(g, g.nodeNames[stack[j]], g.nodeNames[stack[j+1]]) {
				return true
			}
		}
		break
	}
	return false
}

// edgeHasAction 报告该方向任一边是否含积木动作。
func edgeHasAction(g *stateGraph, from, to string) bool {
	for _, e := range g.edges {
		if e.from == from && e.to == to && len(e.wire.Actions) > 0 {
			return true
		}
	}
	return false
}

// findUnreachable 从初始态 + 外生目标 BFS——不可达状态报 E301。
func findUnreachable(g *stateGraph, result *AnalysisResult) {
	adj := g.adjacency(true)
	visited := make([]bool, len(g.nodeNames))
	var queue []int
	for _, name := range g.initial {
		if idx, ok := g.nodeIndex[name]; ok {
			queue = append(queue, idx)
			visited[idx] = true
		}
	}
	for _, name := range g.extern {
		if idx, ok := g.nodeIndex[name]; ok && !visited[idx] {
			queue = append(queue, idx)
			visited[idx] = true
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, next := range adj[queue[head]] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	for i, name := range g.nodeNames {
		if !visited[i] {
			result.Errors = append(result.Errors, Diag{Code: "E301", Line: g.nodeLine[i], Msg: "不可达状态: '" + name + "'——无任何转移链到达（孤立或死状态）"})
		}
	}
}

// findNoRecovery 校验失败侧目标（多结果导线第 2 支起）能到达终态——否则 E302。
func findNoRecovery(doc *Doc, g *stateGraph, result *AnalysisResult) {
	adj := g.adjacency(true)
	n := len(g.nodeNames)
	terminal := make([]bool, n)
	terminalCount := 0
	for i := 0; i < n; i++ {
		terminal[i] = g.outDegree[i] == 0
		if terminal[i] {
			terminalCount++
		}
	}
	// 无终态系统（纯事件循环）——E302 不适用（无「归宿」概念）
	if terminalCount == 0 {
		return
	}
	for i := range doc.Wires {
		w := &doc.Wires[i]
		if len(w.Results) < 2 {
			continue
		}
		for r := 1; r < len(w.Results); r++ {
			target := w.Results[r].StateName + "." + w.Results[r].StateValue
			idx, ok := g.nodeIndex[target]
			if !ok {
				continue
			}
			if terminal[idx] {
				continue
			}
			if canReachTerminal(adj, terminal, idx) {
				continue
			}
			result.Errors = append(result.Errors, Diag{Code: "E302", Line: w.Line, Msg: "导线 '" + w.Name + "' 失败侧目标 '" + target + "' 无恢复路径——无法到达任何终态（非终态环内空转）"})
		}
	}
}

// canReachTerminal 报告从 start 出发能否到达任一终态。
func canReachTerminal(adj [][]int, terminal []bool, start int) bool {
	visited := make([]bool, len(adj))
	queue := []int{start}
	visited[start] = true
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if terminal[cur] {
			return true
		}
		for _, next := range adj[cur] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// buildCriticalPathReport 生成关键路径报告——SCC 收缩 + DAG 拓扑 DP 最长链。
func buildCriticalPathReport(g *stateGraph, result *AnalysisResult) {
	adj := g.adjacency(true)
	n := len(g.nodeNames)
	visited := make([]bool, n)
	var order []int
	for i := 0; i < n; i++ {
		if !visited[i] {
			dfsOrder(adj, i, visited, &order)
		}
	}
	rev := make([][]int, n)
	for i := 0; i < n; i++ {
		for _, next := range adj[i] {
			rev[next] = append(rev[next], i)
		}
	}
	comp := make([]int, n)
	for i := range comp {
		comp[i] = -1
	}
	compCount := 0
	for i := len(order) - 1; i >= 0; i-- {
		node := order[i]
		if comp[node] == -1 {
			dfsAssign(rev, node, comp, compCount)
			compCount++
		}
	}
	dp := make([]int, compCount)
	for i := range dp {
		dp[i] = 1
	}
	for i := len(order) - 1; i >= 0; i-- {
		node := order[i]
		for _, next := range adj[node] {
			if comp[node] != comp[next] && dp[comp[node]]+1 > dp[comp[next]] {
				dp[comp[next]] = dp[comp[node]] + 1
			}
		}
	}
	longest := 1
	for _, v := range dp {
		if v > longest {
			longest = v
		}
	}
	result.Reports = append(result.Reports, "关键路径: 最长状态转移链 "+itoa(longest)+" 个 SCC 节点（"+itoa(n)+" 状态 / "+itoa(len(g.edges))+" 边 / "+itoa(compCount)+" 强连通分量）")
}

// dfsOrder 是 Kosaraju 第一步——DFS 后序。
func dfsOrder(adj [][]int, node int, visited []bool, order *[]int) {
	visited[node] = true
	for _, next := range adj[node] {
		if !visited[next] {
			dfsOrder(adj, next, visited, order)
		}
	}
	*order = append(*order, node)
}

// dfsAssign 是 Kosaraju 第二步——反向图分量标注。
func dfsAssign(rev [][]int, node int, comp []int, compID int) {
	comp[node] = compID
	for _, next := range rev[node] {
		if comp[next] == -1 {
			dfsAssign(rev, next, comp, compID)
		}
	}
}
