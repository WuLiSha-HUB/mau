package bricks

// CSHARP 桥积木——CsCat 13 条认领线共用（对齐 C# BRIK-PACK-003）。
//
// 语义：method / argsJSON 原样透传桥（POST /invoke）——不二次包装、不做方法白名单、
// 不做参数校验（C# 积木同口径：未知方法由桥回 ERR|UNKNOWN_METHOD，方法内参数由桥校验）。
// 桥回 {ok,result}：result 即工具回执（A214 头由桥内部产出），ok 决定 Flow 走 Done / Failed。

// CSharpBridge 执行 csharp.bridge——桥调用单一入口。
func CSharpBridge(svc *CsService, method, argsJSON string) (string, bool) {
	if svc == nil {
		return "ERR|CSHARP_NO_BRIDGE|宿主未注入 ICSharpBridge（Mau.Development.MauRoslynBridge）", false
	}
	return svc.Invoke(method, argsJSON)
}
