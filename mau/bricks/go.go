package bricks

// GO 四件积木——GoCat 工具执行面（宿主首个 Go 工具组；C# 时代无基准，语义按 Go 官方工具链自定）。
//
// 语义：薄壳只做两件事——服务未注入报 GO_NO_SERVICE → 交服务层。
// 参数面校验归服务层（GoService 各方法内 Validate）——四件工具的参数全部可选（dir / path / output /
// args / filter / count1 / verbose / mode / timeout_ms），故空载荷视同 {}，不判 BAD_ARGS。
// 服务层 ERR| 文本按工具成功返回（失败原因对 Dog 可见）。

// GoBuild 执行 go.build——编译裁决唯一权威（go build）。
func GoBuild(goSvc *GoService, argsJSON string) (string, bool) {
	if goSvc == nil {
		return "ERR|GO_NO_SERVICE|宿主未注入 GoService", false
	}
	return goSvc.Build(argsJSON), true
}

// GoTest 执行 go.test——跑测（go test；cached 是缓存命中，不是没跑）。
func GoTest(goSvc *GoService, argsJSON string) (string, bool) {
	if goSvc == nil {
		return "ERR|GO_NO_SERVICE|宿主未注入 GoService", false
	}
	return goSvc.Test(argsJSON), true
}

// GoVet 执行 go.vet——官方静态检查（go vet）。
func GoVet(goSvc *GoService, argsJSON string) (string, bool) {
	if goSvc == nil {
		return "ERR|GO_NO_SERVICE|宿主未注入 GoService", false
	}
	return goSvc.Vet(argsJSON), true
}

// GoFmt 执行 go.fmt——格式门禁（gofmt -l 干跑 / -w 写盘）。
func GoFmt(goSvc *GoService, argsJSON string) (string, bool) {
	if goSvc == nil {
		return "ERR|GO_NO_SERVICE|宿主未注入 GoService", false
	}
	return goSvc.Fmt(argsJSON), true
}
