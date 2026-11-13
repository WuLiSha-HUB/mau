package main

// 猫级文件服务注册表——对齐 C# ToolCatContext / ResolveCatRootEntries 的作用面：
//
//   - 空 / null enabledRoots = 空白名单：仅常驻根（workspace / runtime）可用（失败默认收紧，不回落全量）；
//   - 非空 = 全局根池子集（大小写不敏感，池外 id 剔除）；
//   - workspace / runtime 强制常驻（FilterRoots 统一实现）。
//
// 工具载荷由会话层注入 catId（Session.injectCatID）；积木按 catId 取本表实例，
// 未注册 / 载荷缺 catId 回退全局服务（单猫形态与旧载荷行为不倒退）。

import (
	"encoding/json"
	"strings"
	"sync"

	"cathome5/host/config"
	"cathome5/mau/fs"
)

// catFSRegistry 是猫级文件服务表（key 归一为小写）。
type catFSRegistry struct {
	// mu 保护 per
	mu sync.RWMutex
	// workspace 全局受控根表（过滤源）
	workspace *config.WorkspaceConfig
	// per 猫级服务（catID 小写 → 服务；值恒非 nil）
	per map[string]*fs.Service
	// logf 观测出口（nil = 静默）
	logf func(level int, message, category string)
}

// newCatFSRegistry 构造猫级服务表。
func newCatFSRegistry(workspace *config.WorkspaceConfig, logf func(level int, message, category string)) *catFSRegistry {
	return &catFSRegistry{workspace: workspace, per: map[string]*fs.Service{}, logf: logf}
}

// ServiceFor 按工具载荷取猫级服务——载荷顶层 catId 命中即用；缺 catId / 未注册返回 nil（调用方回退全局）。
func (r *catFSRegistry) ServiceFor(argsJSON string) *fs.Service {
	if r == nil {
		return nil
	}
	catID := catIDFromArgs(argsJSON)
	if catID == "" {
		return nil
	}
	return r.Resolve(catID)
}

// Resolve 按猫 key 取服务（未注册 → nil）。
func (r *catFSRegistry) Resolve(catID string) *fs.Service {
	if r == nil || catID == "" {
		return nil
	}
	r.mu.RLock()
	svc := r.per[strings.ToLower(catID)]
	r.mu.RUnlock()
	return svc
}

// Rebuild 按猫配置重建服务并落审计行（cat.roots | 来源=空白名单/子集 | 声明=N | 实际=M | [ids]）。
//
// 调用点：会话装配（cat.new / cat.start / 启动扫描）与 catcfg.apply（配置落盘后即时生效）。
func (r *catFSRegistry) Rebuild(catID string, cfg *config.CatCfg) {
	if r == nil || r.workspace == nil || catID == "" {
		return
	}
	var enabled []string
	if cfg != nil {
		enabled = cfg.EnabledRoots
	}
	roots := config.FilterRoots(r.workspace.FSRoots(), enabled)
	source := "空白名单"
	if len(enabled) > 0 {
		source = "子集"
	}
	ids := config.RootIDs(roots)
	svc := buildFileServiceFromRoots(roots)
	r.mu.Lock()
	if svc == nil {
		delete(r.per, strings.ToLower(catID))
	} else {
		r.per[strings.ToLower(catID)] = svc
	}
	r.mu.Unlock()
	if r.logf != nil {
		r.logf(1, "cat.roots | cat="+catID+" | 来源="+source+" | 声明="+itoa(len(enabled))+
			" | 实际="+itoa(len(roots))+" | ["+strings.Join(ids, ",")+"]", "CHAT")
	}
}

// Drop 移除猫级服务（cat.delete 用；未注册无副作用）。
func (r *catFSRegistry) Drop(catID string) {
	if r == nil || catID == "" {
		return
	}
	r.mu.Lock()
	delete(r.per, strings.ToLower(catID))
	r.mu.Unlock()
}

// catIDFromArgs 取工具载荷顶层 catId（非对象 / 解析失败 / 缺键 → 空串）。
func catIDFromArgs(argsJSON string) string {
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" || trimmed[0] != '{' {
		return ""
	}
	var payload struct {
		CatID string `json:"catId"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.CatID)
}
