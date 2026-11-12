package bricks

// Proc 四件积木——ProcCat 工具执行面（进程槽：任意受控根内 exe）。
//
// 语义对齐 C# BRIK-PROC-001..004：薄壳直通——参数面校验 / 路径边界 / 台账全在服务侧
// （ProcService），积木只做「空载荷检查 + 服务注入检查 + 结果直返」。
// 服务层 ERR| 文本按工具成功返回（失败原因对 Dog 可见）；缺服务 / 空载荷按工具失败返回。

// ProcRun 执行 proc.run——同步执行受控根内 exe 到退出（跑测用）。
func ProcRun(svc *ProcService, argsJSON string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PROC_BAD_ARGS|缺少参数 path", false
	}
	if svc == nil {
		return "ERR|PROC_NO_SERVICE|宿主未注入 IProcService", false
	}
	return svc.Run(argsJSON), true
}

// ProcStart 执行 proc.start——脱手起受控根内 exe 并登记台账。
func ProcStart(svc *ProcService, argsJSON string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PROC_BAD_ARGS|缺少参数 path", false
	}
	if svc == nil {
		return "ERR|PROC_NO_SERVICE|宿主未注入 IProcService", false
	}
	return svc.Start(argsJSON), true
}

// ProcStop 执行 proc.stop——停掉 proc-start 起的进程（优雅 → 强杀兜底）。
func ProcStop(svc *ProcService, argsJSON string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PROC_BAD_ARGS|缺少参数 id", false
	}
	if svc == nil {
		return "ERR|PROC_NO_SERVICE|宿主未注入 IProcService", false
	}
	return svc.Stop(argsJSON), true
}

// ProcList 执行 proc.list——台账清单 + 活体核对（无参）。
func ProcList(svc *ProcService, argsJSON string) (string, bool) {
	if svc == nil {
		return "ERR|PROC_NO_SERVICE|宿主未注入 IProcService", false
	}
	return svc.List(argsJSON), true
}
