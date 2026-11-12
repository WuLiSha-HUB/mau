package http

// routes 装配主端口路由表。
//
// 与 C# BuildApp 同构：恒注册段无条件挂载；条件注册段只在对应构建器非 nil 时挂载
// （不存在的 Map 根本不执行——这是每猫端口差异的机制，不是运行时白名单）。
func (s *Server) routes() {
	// 恒注册——快照 / SSE / 指令 / 日志 / 配置 / 静态
	s.mux.HandleFunc("GET /api/v1/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("GET /api/v1/stream", s.handleStream)
	s.mux.HandleFunc("POST /api/v1/command", s.handleCommand)
	s.mux.HandleFunc("GET /api/v1/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfigGet)
	s.mux.HandleFunc("POST /api/v1/config", s.handleConfigPost)
	s.mux.HandleFunc("GET /api/v1/frontend-test", s.handleFrontendTest)

	// 条件注册——构建器未绑定时不挂载
	if s.depsRefresh != nil {
		s.mux.HandleFunc("POST /api/v1/deps/refresh", s.handleDepsRefresh)
	}
	if s.llmProbe != nil {
		s.mux.HandleFunc("POST /api/v1/llm/probe", s.handleLLMProbe)
	}
	if s.catsBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/cats", s.handleCats)
	}
	if s.apiRoleBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/api-role", s.handleAPIRole)
	}
	if s.apiModelsBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/api-models", s.handleAPIModels)
	}
	// 管理端路由表（段 1/2/4 起）——构造期装配，nil = 不挂载
	if s.adminRegister != nil {
		s.adminRegister(s.mux)
	}

	// 静态面——全部禁缓存（fonts 长缓存），路径穿越校验
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /css/{path...}", s.handleStatic("css", "text/css"))
	s.mux.HandleFunc("GET /js/{path...}", s.handleStatic("js", "application/javascript"))
	s.mux.HandleFunc("GET /pet/{path...}", s.handleStatic("pet", "image/webp"))
	s.mux.HandleFunc("GET /fonts/{path...}", s.handleStatic("fonts", "font/ttf"))
	s.mux.HandleFunc("GET /favicon.ico", s.handleRootFile("favicon.ico", "image/x-icon"))
}
