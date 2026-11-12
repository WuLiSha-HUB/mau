package bricks

// TS 五件积木——TsCat 工具执行面（TypeScript 编码工具组）。
//
// 语义对齐 C# BRIK-TS-001..005：参数面零容忍 → 受控根寻址展开绝对路径 → 组 payload
// → INodeService.Invoke（服务实例名 "ts"）→ 回执原样返回（无 A214 头——C# 同口径：
// ts-* 的工具回执首行即服务 JSON，前端 ovJson 直解）。
// 路径展开基准取进程工作目录（对齐 C# Environment.CurrentDirectory）；服务层 ERR| 文本按
// 工具成功返回（失败原因对 Dog 可见），参数面 / 寻址 / 服务未注入按工具失败返回。

import (
	"os"
	"strconv"

	"cathome5/mau/fs"
)

// 工具名常量。
const (
	toolTsCheck   = "ts-check"
	toolTsList    = "ts-list"
	toolTsRead    = "ts-read"
	toolTsFindRef = "ts-find-ref"
	toolTsPatch   = "ts-patch"
)

// TsCheck 执行 ts.check——TypeScript 工程口径类型诊断。
func TsCheck(svc *fs.Service, node *TsService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path full", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	fullText := Get(argsJSON, "full")
	if path == ParseFail || fullText == ParseFail {
		return "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败", false
	}
	absPath, pathErr := expandTsPath(svc, path)
	if pathErr != "" {
		return pathErr, false
	}
	full := fullText == "true"
	payload := `{"op":"ts.check","path":` + jsonString(absPath) + `,"full":` + strconv.FormatBool(full) + `}`
	return invokeTs(node, payload)
}

// TsList 执行 ts.list——TypeScript 单文件结构清单。
func TsList(svc *fs.Service, node *TsService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "file kind", "file", "", ""); bad != "" {
		return bad, false
	}
	file := Get(argsJSON, "file")
	kind := Get(argsJSON, "kind")
	if file == ParseFail || kind == ParseFail {
		return "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败", false
	}
	absFile, pathErr := expandTsPath(svc, file)
	if pathErr != "" {
		return pathErr, false
	}
	payload := `{"op":"ts.list","file":` + jsonString(absFile) + `,"kind":` + jsonString(kind) + `}`
	return invokeTs(node, payload)
}

// TsRead 执行 ts.read——TypeScript 符号源码读取（symbol 空 = 文件概览）。
func TsRead(svc *fs.Service, node *TsService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "file symbol", "file", "", ""); bad != "" {
		return bad, false
	}
	file := Get(argsJSON, "file")
	symbol := Get(argsJSON, "symbol")
	if file == ParseFail || symbol == ParseFail {
		return "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败", false
	}
	absFile, pathErr := expandTsPath(svc, file)
	if pathErr != "" {
		return pathErr, false
	}
	payload := `{"op":"ts.read","file":` + jsonString(absFile) + `,"symbol":` + jsonString(symbol) + `}`
	return invokeTs(node, payload)
}

// TsFindRef 执行 ts.findRef——TypeScript 语义级引用查找。
func TsFindRef(svc *fs.Service, node *TsService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "file symbol tsconfig", "file symbol", "", ""); bad != "" {
		return bad, false
	}
	file := Get(argsJSON, "file")
	symbol := Get(argsJSON, "symbol")
	tsconfig := Get(argsJSON, "tsconfig")
	if file == ParseFail || symbol == ParseFail || tsconfig == ParseFail {
		return "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败", false
	}
	absFile, fileErr := expandTsPath(svc, file)
	if fileErr != "" {
		return fileErr, false
	}
	absConfig := ""
	if tsconfig != "" {
		expanded, configErr := expandTsPath(svc, tsconfig)
		if configErr != "" {
			return configErr, false
		}
		absConfig = expanded
	}
	payload := `{"op":"ts.findRef","file":` + jsonString(absFile) + `,"symbol":` + jsonString(symbol) +
		`,"tsconfig":` + jsonString(absConfig) + `}`
	return invokeTs(node, payload)
}

// TsPatch 执行 ts.patch——TypeScript 改动（符号模式 / 替换模式二选一）。
func TsPatch(svc *fs.Service, node *TsService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "file symbol body old new tsconfig", "file", "", ""); bad != "" {
		return bad, false
	}
	file := Get(argsJSON, "file")
	symbol := Get(argsJSON, "symbol")
	body := Get(argsJSON, "body")
	oldText := Get(argsJSON, "old")
	newText := Get(argsJSON, "new")
	tsconfig := Get(argsJSON, "tsconfig")
	if file == ParseFail || symbol == ParseFail || body == ParseFail ||
		oldText == ParseFail || newText == ParseFail || tsconfig == ParseFail {
		return "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败", false
	}
	// [模式判定] 符号模式与替换模式二选一——两者同时给或都不给一律拒（语义必须唯一）
	symbolMode := symbol != ""
	replaceMode := oldText != ""
	if symbolMode == replaceMode {
		return "ERR|TS_BAD_ARGS|须二选一——符号模式（symbol + body）或替换模式（old + new）", false
	}
	if symbolMode && body == "" {
		return "ERR|TS_BAD_ARGS|符号模式下 body 不可为空", false
	}
	absFile, fileErr := expandTsPath(svc, file)
	if fileErr != "" {
		return fileErr, false
	}
	absConfig := ""
	if tsconfig != "" {
		expanded, configErr := expandTsPath(svc, tsconfig)
		if configErr != "" {
			return configErr, false
		}
		absConfig = expanded
	}
	payload := `{"op":"ts.patch","file":` + jsonString(absFile) +
		`,"symbol":` + jsonString(symbol) +
		`,"body":` + jsonString(body) +
		`,"old":` + jsonString(oldText) +
		`,"new":` + jsonString(newText) +
		`,"tsconfig":` + jsonString(absConfig) + `}`
	return invokeTs(node, payload)
}

// expandTsPath 受控根寻址展开——<根id>:<相对> 或绝对路径 → 绝对路径。
//
// 基准取进程工作目录（C# TsCheckBrick 传 Environment.CurrentDirectory）；svc 为 nil 时
// 原样透传（对齐 C# WorkspaceConfig 未注入时的回落）。失败文案逐字对齐 C#。
func expandTsPath(svc *fs.Service, raw string) (string, string) {
	if svc == nil {
		return raw, ""
	}
	base, _ := os.Getwd()
	full, _, code, _, ok := fs.ResolveOwnedRoot(raw, svc.Roots, base)
	if !ok {
		return "", "ERR|TS_BAD_PATH|路径解析失败（" + raw + "）: " + code
	}
	return full, ""
}

// invokeTs 投递服务调用——payload 交 ts 服务（进程与协议归服务层）。
func invokeTs(node *TsService, payload string) (string, bool) {
	if node == nil {
		return "ERR|TS_NO_SERVICE|宿主未注入 INodeService", false
	}
	return node.Invoke(payload), true
}

// 工具名常量与 C# 工具定义（tools.tscat）同名——命名铁律：分隔符用连字符（ts-check），点号只用于积木名（ts.check）。
