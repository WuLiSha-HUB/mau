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
// 语义与 C# 每猫 enabledRoots 一致：空清单 = 未配置白名单（全量放行）；
// 非空清单 = 白名单（大小写不敏感）；workspace 恒在（强制根）。
func FilterRoots(all []fs.Root, enabled []string) []fs.Root {
	if len(enabled) == 0 {
		return all
	}
	allow := make(map[string]bool, len(enabled)+1)
	for _, id := range enabled {
		allow[strings.ToLower(id)] = true
	}
	allow["workspace"] = true
	out := make([]fs.Root, 0, len(all))
	for _, r := range all {
		if allow[strings.ToLower(r.ID)] {
			out = append(out, r)
		}
	}
	return out
}
