package toolpool

import "strconv"

// ResolveOrder 解析工具执行序——静态表裁决入口（timeback 两态各取一端钉死值）。
func ResolveOrder(name string) int {
	if name == "" {
		return OrderDefault
	}
	if name == "timeback-start" {
		return OrderTimebackStart
	}
	if name == "timeback-back" {
		return OrderTimebackBack
	}
	if containsName(readOnlyNames, name) {
		return OrderReadOnly
	}
	if containsName(writeNames, name) {
		return OrderWrite
	}
	if containsName(exclusiveNames, name) {
		return OrderExclusive
	}
	if containsName(buildNames, name) {
		return OrderBuild
	}
	return OrderDefault
}

// IsExclusive 判定独占档——本档工具每次调用各自成一批。
func IsExclusive(name string) bool {
	if name == "" {
		return false
	}
	return containsName(exclusiveNames, name)
}

// OrderText 是执行序显示文本——toolcard 载荷与配置面共用出口。
func OrderText(name string) string { return strconv.Itoa(ResolveOrder(name)) }

// NoteLine 是工具定义标准注记（描述尾行，形态 `order: <n>`，永不为空）。
func NoteLine(name string) string { return "order: " + OrderText(name) }

// PlanBatches 按 order 值升序分桶：同值一批、批内保持入参声明序，独占档每次调用各自成批。
//
// 返回每批的下标列表（下标指向入参序）。
func PlanBatches(names []string, orders []int) [][]int {
	plan := make([][]int, 0, 4)
	if len(names) != len(orders) {
		return plan
	}
	values := make([]int, 0, 6)
	for _, v := range orders {
		if !containsInt(values, v) {
			values = append(values, v)
		}
	}
	sortInts(values)
	for _, v := range values {
		current := make([]int, 0, 4)
		for i, order := range orders {
			if order != v {
				continue
			}
			if IsExclusive(names[i]) {
				if len(current) > 0 {
					plan = append(plan, current)
					current = make([]int, 0, 4)
				}
				plan = append(plan, []int{i})
				continue
			}
			current = append(current, i)
		}
		if len(current) > 0 {
			plan = append(plan, current)
		}
	}
	return plan
}

// RegisteredNames 返回已登记工具名全集（对账门禁基准）。
func RegisteredNames() []string {
	total := len(readOnlyNames) + len(writeNames) + len(exclusiveNames) + len(buildNames) + len(defaultNames) + len(pinnedNames)
	all := make([]string, 0, total)
	all = append(all, readOnlyNames...)
	all = append(all, writeNames...)
	all = append(all, exclusiveNames...)
	all = append(all, buildNames...)
	all = append(all, defaultNames...)
	all = append(all, pinnedNames...)
	return all
}

// FindUnregistered 返回未登记工具名（顺序同入参；空 = 全量已登记）。
func FindUnregistered(names []string) []string {
	registered := RegisteredNames()
	missing := make([]string, 0, 4)
	for _, name := range names {
		if name == "" {
			continue
		}
		if !containsName(registered, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// containsName 名单线性查找（规模 < 100，免建索引）。
func containsName(names []string, name string) bool {
	for _, item := range names {
		if item == name {
			return true
		}
	}
	return false
}

// containsInt 整数名单查找。
func containsInt(values []int, v int) bool {
	for _, item := range values {
		if item == v {
			return true
		}
	}
	return false
}

// sortInts 升序排序（内置整数排序，规模小）。
func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
