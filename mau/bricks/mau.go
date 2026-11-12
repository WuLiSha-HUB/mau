package bricks

// MAU 四件积木——MauCat 工具执行面（对齐 C# BRIK-MAU-001 / 002 / 003 / 004）。
//
// 语义：薄壳只做「服务未注入报 MAU_NO_SERVICE → 交服务层」；参数面校验在服务层
// （Validate 口径与 C# JsonArgs 一致：未知键 / 缺必填 / 非法枚举 → ERR|BAD_ARGS）。
// 工具定义（BRIK-TOOLS-002 tools.maucat）逐字一致：
//   mau-verify=file · mau-gen=proj · mau-proj=proj/build · mau-setup=mode/target/report。
// 回执格式（A214 头 / 正文 / FAIL| 行）由服务层按 C# 积木出口逐字拼装。

// MauVerify 执行 mau.verify——Go 原生 front 全链检查。
func MauVerify(mau *MauService, argsJSON string) (string, bool) {
	if mau == nil {
		return "ERR|MAU_NO_SERVICE|宿主未注入 IMauService", false
	}
	return mau.Verify(argsJSON)
}

// MauGen 执行 mau.gen——组翻译中间产物（经 cs 桥进程内直调）。
func MauGen(mau *MauService, argsJSON string) (string, bool) {
	if mau == nil {
		return "ERR|MAU_NO_SERVICE|宿主未注入 IMauService", false
	}
	return mau.Gen(argsJSON)
}

// MauProj 执行 mau.proj——组翻译 + 编译（经 cs 桥进程内直调）。
func MauProj(mau *MauService, argsJSON string) (string, bool) {
	if mau == nil {
		return "ERR|MAU_NO_SERVICE|宿主未注入 IMauService", false
	}
	return mau.Proj(argsJSON)
}

// MauSetup 执行 mau.setup——SetUp.exe 子进程 + 报告 JSON 摘要。
func MauSetup(mau *MauService, argsJSON string) (string, bool) {
	if mau == nil {
		return "ERR|MAU_NO_SERVICE|宿主未注入 IMauService", false
	}
	return mau.Setup(argsJSON)
}
