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
	if s.flowReloader != nil {
		s.mux.HandleFunc("POST /api/v1/flow/reload", s.handleFlowReload)
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
	// 对话页条件面——构造期条件注册（管理面板实例不注入 → 不挂载，C# 同构）
	if s.contextBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/context", s.handleContext)
	}
	if s.keyInfoBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/keyinfo", s.handleKeyInfo)
	}
	if s.fullContextBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/fullctx", s.handleFullContext)
	}
	if s.effortBuilder != nil {
		s.mux.HandleFunc("GET /api/v1/effort", s.handleEffortGet)
	}
	if s.effortSetter != nil {
		s.mux.HandleFunc("POST /api/v1/effort", s.handleEffortPost)
	}
	if s.apiRoleToggler != nil {
		s.mux.HandleFunc("POST /api/v1/api-role/toggle", s.handleAPIRoleToggle)
	}
	if s.apiModelSetter != nil {
		s.mux.HandleFunc("POST /api/v1/api-model", s.handleAPIModelPost)
	}
	// 项目挂载三件——管理面已注册同名路由（段 12 同一实现）：此处不重复注册（ServeMux 同模式重复注册会 panic）。
	// 管理面未挂载时由条件面补挂（C# 对话页实例 RouteRegistrar 同语义）。
	if s.adminRegister != nil {
		s.adminRegister(s.mux)
	} else {
		if s.injectProjectsBuilder != nil {
			s.mux.HandleFunc("GET /api/v1/inject/projects", s.handleInjectProjects)
		}
		if s.injectProjectSetter != nil {
			s.mux.HandleFunc("POST /api/v1/inject/project", s.handleInjectProjectPost)
		}
		if s.injectAutoGetter != nil {
			s.mux.HandleFunc("GET /api/v1/inject/auto", s.handleInjectAutoGet)
		}
		if s.injectAutoSetter != nil {
			s.mux.HandleFunc("POST /api/v1/inject/auto", s.handleInjectAutoPost)
		}
	}
	// 管理端路由表（段 1/2/4 起）——构造期装配，nil = 不挂载

	// 静态面——全部禁缓存（fonts 长缓存），路径穿越校验
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /css/{path...}", s.handleStatic("css", "text/css"))
	s.mux.HandleFunc("GET /js/{path...}", s.handleStatic("js", "application/javascript"))
	s.mux.HandleFunc("GET /pet/{path...}", s.handleStatic("pet", "image/webp"))
	s.mux.HandleFunc("GET /fonts/{path...}", s.handleStatic("fonts", "font/ttf"))
	s.mux.HandleFunc("GET /favicon.ico", s.handleRootFile("favicon.ico", "image/x-icon"))
}
