package config

import (
	"strings"

	"cathome5/mau/fs"
)

// FSRoots 把受控根条目转换为文件系统服务的根表。
//
// 认路注释（Note）不进 fs.Root——它是给 LLM 的语义提示，由 info / 工具面消费；
// 文件系统服务只关心 id / path / writable。
func (w *WorkspaceConfig) FSRoots() []fs.Root {
	roots := make([]fs.Root, 0, len(w.Roots))
	for _, r := range w.Roots {
		roots = append(roots, fs.Root{ID: r.ID, Path: r.Path, Writable: r.Writable})
	}
	return roots
}

// FilterRoots 按启用清单过滤根表。
//
// 语义与 C# 每猫 enabledRoots 一致：空清单 = 空白名单（仅常驻根 workspace / runtime 可用）；
// 非空清单 = 白名单（大小写不敏感）；workspace / runtime 恒在（强制根）。
// 失败默认收紧——空清单当「未配置」放行全量会把权限面放大，与 C# 相反。
func FilterRoots(all []fs.Root, enabled []string) []fs.Root {
	allow := make(map[string]bool, len(enabled)+2)
	for _, id := range enabled {
		allow[strings.ToLower(id)] = true
	}
	allow["workspace"] = true
	allow["runtime"] = true
	out := make([]fs.Root, 0, len(all))
	for _, r := range all {
		if allow[strings.ToLower(r.ID)] {
			out = append(out, r)
		}
	}
	return out
}

// RootIDs 取根 id 清单（保序；展示 / 审计用）。
func RootIDs(roots []fs.Root) []string {
	ids := make([]string, 0, len(roots))
	for _, r := range roots {
		ids = append(ids, r.ID)
	}
	return ids
}
