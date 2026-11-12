// Package config 复现宿主的配置面：数据根解析、端口段、schema 同步、全局配置群、
// 每猫配置与受控根加载。
//
// 对齐 C# 版 Program.Resolve.cs / Mau.Runtime.ConfigStore + ConfigEffective +
// WorkspaceConfig / CatHostService.CatCfg.cs。错误面用降级条目而非抛异常——
// 配置与环境问题不阻断启动（A260 语义）。
package config

import (
	"os"
	"path/filepath"
)

// Degrade 是一条启动降级事实（code|state|detail）——与 C# StartupDegradation
// 的登记形态对齐；降级不阻断启动，由启动健康面汇总。
type Degrade struct {
	// Code 降级码（如 DATA_ROOT_UNUSABLE / DATA_ROOT_LEGACY）
	Code string
	// State 处置状态（fallback / use-subdir / rebuilt / unavailable）
	State string
	// Detail 明细文案
	Detail string
}

// ResolveDataRoot 按三级锚定解析数据根，返回绝对路径与降级清单。
//
// 锚定顺序与 C# 一致：
//  1. 环境变量 CH4_DATA_ROOT（进程级——用户级 / 机器级注册表回读留待后续轮）
//  2. 仓库根（自可执行文件目录向上找到 Mau.sln——测试区数据隔离）
//  3. %LOCALAPPDATA%/CatHome4（部署实例持久数据）
//
// 数据根不可写时逐级回落，每级回落记一条降级。
func ResolveDataRoot() (string, []Degrade) {
	var degrades []Degrade
	if env := os.Getenv("CH4_DATA_ROOT"); env != "" {
		if EnsureDataRoot(env) {
			return FinalizeDataRoot(env, &degrades), degrades
		}
		degrades = append(degrades, Degrade{
			Code:   "DATA_ROOT_UNUSABLE",
			State:  "fallback",
			Detail: "CH4_DATA_ROOT 不可用：" + env,
		})
	}
	if root := FindRepoRoot(ExeDir()); root != "" {
		return FinalizeDataRoot(root, &degrades), degrades
	}
	return FinalizeDataRoot(filepath.Join(LocalAppDataDir(), "CatHome4"), &degrades), degrades
}

// EnsureDataRoot 探测候选数据根可写——创建目录成功即视为可用，失败不抛。
func EnsureDataRoot(root string) bool {
	if root == "" {
		return false
	}
	return os.MkdirAll(root, 0o755) == nil
}

// FinalizeDataRoot 定型数据根：检测旧布局（根/Data 子层）并出声。
//
// C# 版在此执行「根/Data/* 上移一层」的迁移；P3-a 只检测不迁移——迁移属
// 破坏性动作，留到整机切换前单独决策（降级条目使差异可观测，不静默）。
func FinalizeDataRoot(root string, degrades *[]Degrade) string {
	legacy := filepath.Join(root, "Data")
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		*degrades = append(*degrades, Degrade{
			Code:   "DATA_ROOT_LEGACY",
			State:  "use-subdir",
			Detail: "检测到旧布局数据子层（未迁移，直接使用）：" + legacy,
		})
		return legacy
	}
	return root
}

// FindRepoRoot 自 start 目录向上查找含 Mau.sln 的仓库根；找不到返回空串。
func FindRepoRoot(start string) string {
	if start == "" {
		return ""
	}
	dir := start
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "Mau.sln")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// ExeDir 返回可执行文件所在目录；取不到时返回空串。
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// LocalAppDataDir 返回 %LOCALAPPDATA%；缺失时按 USERPROFILE 拼接，再缺失返回空串。
func LocalAppDataDir() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	if v := os.Getenv("USERPROFILE"); v != "" {
		return filepath.Join(v, "AppData", "Local")
	}
	return ""
}
