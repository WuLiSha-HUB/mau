package toolpool

import (
	"fmt"
	"log"
)

// BuildResult 是宿主启动期的工具面装配结果。
type BuildResult struct {
	// Pool 工具池快照（定义聚合结果）
	Pool *Pool
	// Registry 工具注册表（快照 tools 段与授权判定的数据源）
	Registry *Registry
	// BrickGroups Bricks 索引里聚合到的组数（0 = 未加载）
	BrickGroups int
	// BrickError Bricks 聚合失败原因（空 = 成功）
	BrickError string
}

// Build 装配工具面：Bricks 定义聚合 + 内置定义源 → 池 → 注册表（启动期入口，缺陷打 stdout）。
func Build(bricksIndexPath string) *BuildResult {
	return BuildWith(bricksIndexPath, nil)
}

// BuildWith 装配工具面并指定出声出口（热重载入口——缺陷进宿主日志而不是无处可去的 stdout）。
//
// 两条面同代：池（定义）与注册表（授权 + 注入）都原子换引用——读者要么全旧、要么全新。
func BuildWith(bricksIndexPath string, logf func(level int, message, category string)) *BuildResult {
	result := &BuildResult{}
	groups := make([]GroupSource, 0, 24)
	if bricksIndexPath == "" {
		result.BrickError = "未指定 Bricks/index.json 路径"
	} else {
		loaded, err := LoadFromBricks(bricksIndexPath)
		if err != nil {
			result.BrickError = err.Error()
		} else {
			groups = loaded
			result.BrickGroups = len(loaded)
		}
	}
	RebuildAll(groups, BuiltinToolsJSON())
	result.Pool = Current()
	result.Registry = NewRegistry(result.Pool.BuildSpecs(), result.Pool.BuildOwnerFlowMap(), result.Pool.BuildPrivilegedMap())
	StoreRegistry(result.Registry)
	report := func(message string) {
		if logf != nil {
			logf(1, message, "TOOL")
			return
		}
		log.Print(message)
	}
	// 执行序对账——池内工具必须逐一登记（未登记者静默落默认档，此处出声）
	if missing := FindUnregistered(result.Pool.AllNames()); len(missing) > 0 {
		report(fmt.Sprintf("工具执行序未登记（落默认档 0）| %d 件：%v", len(missing), missing))
	}
	// 定义缺陷出声（组名 / 阶段 / 原因）
	for _, defect := range result.Pool.Defects() {
		report("工具组定义缺陷 | " + defect.Group + " | " + defect.Stage + " | " + defect.Reason)
	}
	return result
}
