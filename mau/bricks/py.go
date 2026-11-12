package bricks

// PY 三件积木——PyCat 工具执行面（对齐 C# BRIK-PY-001 / 002 / 003）。
//
// 语义：薄壳只做三件事——空参判 C# 文案 → 服务未注入报 PY_NO_SERVICE → 交服务层。
// 🔴 C# 侧 py 三件**不做参数面校验**（与 ts 五件的 JsonArgs.Validate 不同）：未知键忽略、
// 缺必填与枚举非法一律由服务层回 ERR|PY_BAD_ARGS|（文案在 PyService）。
// 服务层 ERR| 文本按工具成功返回（失败原因对 Dog 可见）；空参 / 未注入按工具失败返回。
// 参数面与工具定义（BRIK-TOOLS-016 tools.pycat）逐字一致：py-run=script/args/timeout_ms ·
// py-list=dir · py-service=name/action（start|stop|status）——cwd 只在服务层内部使用，不透传。

// PyRun 执行 py.run——短命脚本执行（起-跑-退）。
func PyRun(py *PyService, argsJSON string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PY_BAD_ARGS|缺少参数 script", false
	}
	if py == nil {
		return "ERR|PY_NO_SERVICE|宿主未注入 IPyService", false
	}
	return py.Run(argsJSON), true
}

// PyList 执行 py.list——受控根内 *.py 扫描（dir 可选；空 = mau 根下 tools）。
func PyList(py *PyService, argsJSON string) (string, bool) {
	if py == nil {
		return "ERR|PY_NO_SERVICE|宿主未注入 IPyService", false
	}
	return py.List(argsJSON), true
}

// PyServiceControl 执行 py.service——常驻服务启停查（name / action 必填）。
func PyServiceControl(py *PyService, argsJSON string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PY_BAD_ARGS|缺少参数 name", false
	}
	if py == nil {
		return "ERR|PY_NO_SERVICE|宿主未注入 IPyService", false
	}
	return py.Service(argsJSON), true
}
