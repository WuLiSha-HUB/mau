package session

// 日志出口——由装配层注入（空函数 = 不落日志）。
//
// 参数与 C# LogStore.Add 的三元组对齐：模块名 / 危险等级（1=INFO · 2=WARN · 3=ERR）/ 消息 / 类别。
// 会话层不直接依赖 host/observe：离线对拍与单测不需要启动日志总账。
var Logf = func(module string, level int, message, category string) {}

// logAt 写一条日志（Logf 置空时零动作）。
func logAt(module string, level int, message, category string) {
	if Logf == nil {
		return
	}
	Logf(module, level, message, category)
}

// 模块名常量——与 C# 侧 LogStore.Add 首参同源（日志模块列的判据）。
const (
	// LogModuleHost 宿主会话面（C# 侧写 "CatHome4"）
	LogModuleHost = "CatHome4"
	// LogModuleSession 会话实体（C# 侧写 "ChatSession"）
	LogModuleSession = "ChatSession"
	// LogModuleViewStore 视图存储（C# 侧写 "SessionViewStore"）
	LogModuleViewStore = "SessionViewStore"
)

// 日志类别常量（C# 侧第四参）。
const (
	// LogCategoryPending 待处理队列
	LogCategoryPending = "PENDING"
	// LogCategoryRestart 重启停机态
	LogCategoryRestart = "RESTART"
	// LogCategoryChat 会话轮次
	LogCategoryChat = "CHAT"
	// LogCategorySys 系统面
	LogCategorySys = "SYS"
	// LogCategoryTimeback timeback 作用域
	LogCategoryTimeback = "TIMEBACK"
	// LogCategoryLLM LLM 请求与流
	LogCategoryLLM = "LLM"
	// LogCategoryTool 工具面
	LogCategoryTool = "TOOL"
)
