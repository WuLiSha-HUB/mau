// Package toolpool 复现宿主的工具面：工具定义聚合、执行序表、工具注册表、
// 授权面实时裁剪、注入面会话快照派生与 tools_drift 计算。
//
// 对齐 C# 版 CatHome4.Core.ToolPool / ToolRegistry / ToolOrderTable 与
// CatHome4.Admin.AdminService 的工具名单处理：
//   - 定义来源 = 工具组自曝（Bricks/TOOLS 源码常量解析）+ 内置定义源（宿主内建 11 件）
//   - 写侧局部构建 → 原子换引用；读侧取局部快照（无半更新可见）
//   - 授权面实时（按当前 cat.cfg 名单）与注入面会话快照（新会话才换）是两个面
package toolpool

// 工具执行序档位（ToolOrderTable 常量逐字对齐）。
const (
	// OrderTimebackStart 钉死档——timeback-start：会话结构约束优先，忽略 LLM 调用序。
	OrderTimebackStart = -100
	// OrderReadOnly 只读 / 无副作用档。
	OrderReadOnly = -1
	// OrderDefault 默认档——未登记回落值；也承载 sleep / timer。
	OrderDefault = 0
	// OrderWrite 写入 / 变更档。
	OrderWrite = 1
	// OrderExclusive 独占档——每次调用各自成一批（不与任何工具同批）。
	OrderExclusive = 2
	// OrderBuild 构建 / 执行 / 部署档。
	OrderBuild = 3
	// OrderTimebackBack 钉死档——timeback-back。
	OrderTimebackBack = 100
)

// readOnlyNames 是只读档清单（-1）——观测 / 查询类，无外部副作用。
var readOnlyNames = []string{
	"text-read", "text-read_lines", "text-read_between", "text-grep",
	"file-tree", "file-find", "file-version", "file-stat", "file-hash", "file-diff",
	"cs-check", "cs-list", "cs-read", "cs-find_ref", "cs-find", "cs-dead", "cs-comment_check",
	"mau-verify",
	"config-list", "config-get", "config-cat-get",
	"web-search", "web-ds-search", "image-analyze",
	"temp-info",
	"browser-open", "browser-read", "browser-shot",
	"majordomo-catinfo",
	"py-list", "proc-list",
	"db-query", "db-get", "dbAdmin-libs",
	"host-flows", "info", "time", "random", "pack", "Note",
	"http-get", "http-probe",
	"git-status", "git-log", "git-diff", "git-show",
}

// writeNames 是写入档清单（1）——改文件 / 改配置 / 改仓库产物。
var writeNames = []string{
	"text-write", "text-append", "text-replace",
	"file-move", "file-delete", "file-copy",
	"config-set", "config-reset", "config-cat-set",
	"image-inject",
	"browser-eval", "browser-tabs",
	"web-fetch", "web-fetch-jobs",
	"audio-play",
	"db-put", "db-update", "db-delete",
	"dbAdmin-create", "dbAdmin-drop",
	"http-post",
	"git-commit", "git-push",
}

// exclusiveNames 是独占档清单（2）——预检读全项目 / 争抢构建产物 / 同 profile 进程互斥。
var exclusiveNames = []string{
	"cs-patch", "cs-member", "cs-comment", "cs-format", "cs-build", "cs-test",
	"browser-headful", "proc-start", "proc-stop",
}

// buildNames 是构建 / 执行 / 部署档清单（3）。
var buildNames = []string{
	"powershell", "powershell7",
	"mau-gen", "mau-proj", "host-reload", "mau-setup",
	"restart-full", "restart-incr", "restart-host", "majordomo-cmd",
	"py-run", "py-service", "audio-speak",
	"temp-exec", "proc-run",
}

// defaultNames 是默认档清单（0）显式登记——对账门禁以「全量登记」为判据。
var defaultNames = []string{"sleep", "timer"}

// pinnedNames 是钉死档清单——timeback 两态，值由工具名直接裁决。
var pinnedNames = []string{"timeback-start", "timeback-back"}
