// Command cathome5 是 CatHome5 宿主进程入口——复现 CatHome4 的启动链与主端口外观层。
//
// P3-a 范围：数据根 → schema 同步 → 配置加载 → workspace.json → 路由装配 → 起服 → 250ms Pump。
// 会话 / 工具池 / LLM 面归 P3-b / P3-c；管理面板写面归 P3-d。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cathome5/host/admin"
	"cathome5/host/config"
	"cathome5/host/fetest"
	"cathome5/host/group"
	cathomehttp "cathome5/host/http"
	"cathome5/host/llm"
	"cathome5/host/observe"
	"cathome5/host/session"
	"cathome5/host/toolpool"
	"cathome5/mau/db"
)

// version 是宿主版本（前端 __V__ cache-busting 用；C# 侧取程序集版本三段的等价物）。
//
// 版本单点 = 仓库根 version.txt（构建时经 -ldflags "-X main.version=<版本>" 注入；
// 不带 ldflags 时用本默认值——源码直跑与 CHANGELOG 对齐）。
var version = "0.28.0"

func main() {
	// [段0] 部署链子命令分派（cathome5 deploy prepare|deploy|relaunch）——不进宿主启动链
	if len(os.Args) > 1 && os.Args[1] == "deploy" {
		os.Exit(runDeploy(os.Args[2:]))
	}
	dataRootFlag := flag.String("data-root", "", "数据根覆盖（等价 CH4_DATA_ROOT）")
	htmlRootFlag := flag.String("html-root", "", "静态页根覆盖（等价 CAT5_HTML_ROOT）")
	bricksFlag := flag.String("bricks", "", "Bricks/index.json 路径覆盖（缺省从 workspace 的 mau 根推导）")
	portFlag := flag.Int("port", 0, "监听端口（0 = 按端口段解析）")
	areaFlag := flag.String("area", "", "区覆盖（dev / deploy）")
	pageFlag := flag.String("page", "", "页面覆盖（chat.html = 对话页实例）")
	corpusFlag := flag.String("corpus", "", "语料根覆盖（等价 CAT5_CORPUS；缺省从 exe 目录 / workspace 的 mau 根推导）")
	pushFlag := flag.String("majordomopush", "", "宿主启动回执注入串（重启链 T6——一次性 system 消息投给 Majordomo 会话）")
	versionFlag := flag.Bool("version", false, "打印版本后退出")
	runFlag := flag.String("run", "", "单指令模式：执行一条指令后退出（非交互——不监听端口）")
	flag.Parse()
	if *versionFlag {
		fmt.Println("cathome5 " + version)
		return
	}
	// [段0b] 参数面校验——非交互模式零容忍（调用方是程序，必须拿到失败退出码；对齐 C# ValidateHostArgs）
	runProvided := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "run" {
			runProvided = true
		}
	})
	if runProvided && strings.TrimSpace(*runFlag) == "" {
		fmt.Println("参数错误：--run 缺值（需指令）。")
		os.Exit(1)
	}
	if runProvided && strings.TrimSpace(*pushFlag) != "" {
		fmt.Println("参数错误：--majordomopush 仅用于常驻交互启动（不可与 --run 同用）。")
		os.Exit(1)
	}
	nonInteractive := runProvided

	// [段1] 区与端口段——显式覆盖优先
	if *areaFlag != "" {
		_ = os.Setenv("CAT5_AREA", *areaFlag)
	}
	band := config.ResolvePortBand(config.ResolveArea())

	// [段2] 数据根——flag 覆盖 > 环境变量 > 仓库根 > %LOCALAPPDATA%
	if *dataRootFlag != "" {
		_ = os.Setenv("CH4_DATA_ROOT", *dataRootFlag)
	}
	dataRoot, rootDegrades := config.ResolveDataRoot()
	configDir := filepath.Join(dataRoot, "config")
	// 非交互模式输出双写落盘（外壳件 3——对齐 C# EnableCmdCapture）：保留 stdout，增落盘副本
	var cmdCaptureFile *cmdCapture
	if nonInteractive {
		cmdCaptureFile = enableCmdCapture(dataRoot)
	}

	// [段3] 早期诊断 sink（配置与观测就绪前的基础落点）
	sink := newBootSink()
	sink.Init(dataRoot)
	sink.Add("S0", "数据根 | "+dataRoot)
	for _, d := range rootDegrades {
		sink.Add("S2", d.Code+" | "+d.Detail)
	}

	// [段4] 目录结构 + schema 同步 + 配置加载
	mustMkdir(configDir)
	syncResult := config.SyncSchema(configDir)
	sink.Add("S1", "schema | "+syncResult.Outcome+" | "+syncResult.Detail)
	schema, schemaErr := config.LoadSchema(filepath.Join(configDir, "schema.json"))
	if schemaErr != nil {
		sink.Add("S1", "schema 加载失败 | "+schemaErr.Error())
		schema = &config.Schema{}
	}
	// 声明通道锚点（config-set declare=true）：运行副本写回位置 + 模板双写位置
	if schema.SourcePath == "" {
		schema.SourcePath = filepath.Join(configDir, "schema.json")
	}
	schema.TemplatePath = syncResult.Template
	store := config.LoadStore(configDir)
	// schema 驱动段注册——键前缀段 → 文件归属（对齐 C# RegisterSchemaSegments；
	// 写面据此落对文件，未注册的段按 <前缀>.cfg 派生不出正确归属）
	if segmentNote := store.RegisterSchema(schema); segmentNote != "" {
		sink.Add("S1", segmentNote)
	}
	sink.Add("S1", "配置 | "+itoa(len(store.All()))+" 项落盘显式值")

	// [段5] workspace.json——受控根 + 注入清单
	workspace := config.LoadWorkspace(filepath.Join(configDir, "workspace.json"), dataRoot)
	sink.Add("S2", "受控根 | "+itoa(len(workspace.Roots))+" 根 / 注入 "+itoa(len(workspace.Inject))+" 条")

	// [段6] 日志存储——运行目录四文件的三件（log_all / oa_all / err_all）
	runDir := filepath.Join(dataRoot, "runs", time.Now().Format("20060102_150405"))
	logs := observe.NewLogStore(observe.DefaultLogCapacity)
	boot := observe.NewBootRegistry()
	if errText := logs.ConfigureRuns(runDir); errText != "" {
		// 运行目录不可写——回落早期 sink 落点重试（仍失败则仅内存 + 降级）
		fallback := sink.Dir()
		if fallback != "" && !strings.EqualFold(fallback, runDir) {
			if retryErr := logs.ConfigureRuns(fallback); retryErr == "" {
				runDir = fallback
				boot.Report("OBS_FALLBACK", observe.KindEnv, "观测日志", observe.StateFallback,
					"运行目录不可写，已回落："+fallback, "检查数据根权限或改用可写位置（CH4_DATA_ROOT）", "S3")
			}
		}
		if logs.LastConfigureError() != "" {
			boot.Report("OBS_UNWRITABLE", observe.KindEnv, "观测日志", observe.StateEmpty,
				"运行目录不可写："+runDir+"（"+errText+"）", "检查数据根权限或改用可写位置（CH4_DATA_ROOT）", "S3")
		}
	}
	logs.SetConsoleSink(func(line string) { outln(line) })
	sink.Add("S3", "运行目录 | "+runDir)

	// [段7] 降级登记——数据根 / schema / 受控根
	boot.SetSink(sink.Dir())
	for _, d := range rootDegrades {
		boot.Report(d.Code, observe.KindEnv, "数据根", mapDegradeState(d.State), d.Detail,
			"设置 CH4_DATA_ROOT 指向可写目录，或修正 %LOCALAPPDATA% 权限", "S2")
	}
	if syncResult.Outcome == "failed" {
		boot.Report("SCHEMA_SYNC_FAILED", observe.KindConfig, "配置面（schema）", observe.StateFallback,
			"schema 同步失败："+syncResult.Detail, "检查 config/schema.json.example 与运行区写权限", "S1")
	}
	if syncResult.Outcome == "template-missing" {
		boot.Report("SCHEMA_TEMPLATE_MISSING", observe.KindConfig, "配置面（schema）", observe.StateReadonly,
			syncResult.Detail, "在运行区或仓库根放置 config/schema.json.example", "S1")
	}
	if schemaErr != nil {
		boot.Report("SCHEMA_LOAD_FAILED", observe.KindConfig, "配置面（schema）", observe.StateEmpty,
			"schema 读取失败："+schemaErr.Error(), "检查 Data/config/schema.json（坏档备份后重新同步）", "S1")
	}
	for _, d := range workspace.LoadDegrades {
		if code, state, detail, ok := splitDegrade(d); ok {
			boot.Report(code, observe.KindEnv, "文件工具（受控根）", mapDegradeState(state), detail,
				"管理面板 → 工作区 → 修正 workspace.json 根路径", "S2")
		}
	}
	for _, w := range workspace.LoadWarnings {
		logs.Add("CatHome5", observe.LevelWarn, w, "WORKSPACE", "", "", 0, 0, false)
	}

	// [段8] 早期诊断回灌（sink 行按序并入观测时间线）
	for _, line := range sink.Drain() {
		logs.Add("CatHome5", observe.LevelInfo, line, "BOOT", "", "", 0, 0, false)
	}
	logs.Add("CatHome5", observe.LevelInfo, "config.boot | schema="+syncResult.Outcome+" | 受控根 "+itoa(len(workspace.Roots))+" 根", "CONFIG", "", "", 0, 0, false)
	logs.Add("CatHome5", observe.LevelInfo, "roots.boot | "+rootsAuditNote(workspace), "WORKSPACE", "", "", 0, 0, false)
	// [段8b] 会话层接线——日志出口 + pending 落盘目录（Data/runtime；与 C# ConfigurePendingStore 同址）
	session.Logf = func(module string, level int, message, category string) {
		logs.Add(module, level, message, category, "", "", 0, 0, false)
	}
	session.ConfigurePendingStore(filepath.Join(dataRoot, "runtime"))

	// [段8c] 组数据面接线——Data/group/<组名>/{group.json, messages.jsonl}
	// 对齐 C# Program.BootSteps：GroupStore.Configure(dataRoot/group) → Load()（重启扫描续接）
	groups := group.New(filepath.Join(dataRoot, "group"), func(level int, message, category string) {
		logs.Add("CatHome5", level, message, category, "", "", 0, 0, false)
	})
	groups.Load()

	// [段9] 观测面装配——帧号 / 快照 / 依赖探测
	frame := &observe.FrameCounter{}
	env := observe.DepEnv{
		ConfigGet: func(key string) string { return store.Resolve(schema, key).Value },
		Roots:     toRootPaths(workspace),
		RepoRoot:  repoRootOf(workspace),
		Log: func(module string, level int, message, category string) {
			logs.Add(module, level, message, category, "", "", 0, frame.Current(), false)
		},
	}
	deps := observe.NewDepProbe(env)
	deps.Start()
	builder := &observe.Builder{
		Logs:  logs,
		Boot:  boot,
		Deps:  deps,
		Frame: frame.Current,
	}

	// [段9b] 工具面装配——定义聚合（Bricks/TOOLS 源码常量 + 内置定义源）→ 池 → 注册表
	bricksIndex := resolveBricksIndex(*bricksFlag, workspace)
	toolsResult := toolpool.Build(bricksIndex)
	builder.Tools = func() []observe.Tool { return snapshotTools(toolsResult.Registry) }
	if toolsResult.BrickError != "" {
		boot.Report("TOOLS_BRICKS_MISSING", observe.KindConfig, "工具面（定义聚合）", observe.StateFallback,
			toolsResult.BrickError, "确认 workspace.json 的 mau 根可达且 Bricks/index.json 存在，或用 --bricks 指定索引路径", "S2")
	}
	logs.Add("CatHome5", observe.LevelInfo, "tools.boot | 组 "+itoa(toolsResult.BrickGroups)+" | 池 "+itoa(toolsResult.Pool.MemberCount())+" 件 | 注册表 "+itoa(len(toolsResult.Registry.GetAllNames()))+" 件 | 缺陷 "+itoa(len(toolsResult.Pool.Defects())), "TOOL", "", "", 0, frame.Current(), false)

	// [段9c] 会话装配——cat.cfg + 前文 + 视图 + 工具面 + LLM 客户端（P3-c）
	wiring := wireSession(dataRoot, workspace, toolsResult, schema, store)
	if wiring.Err != "" {
		boot.Report("SESSION_DEGRADED", observe.KindConfig, "会话层", observe.StateFallback,
			wiring.Err, "检查 sessions/<id>/cat.cfg 是否存在且为合法 JSON", "S4")
	}
	logs.Add("CatHome5", observe.LevelInfo, "session.boot | cat="+wiring.CatID+" | 前文 "+itoa(wiring.Session.Context.Count())+" 条 | 工具面 "+itoa(len(wiring.Authorized))+" 件 | 视图 "+itoa(len(wiring.Session.Views.Blocks()))+" 块", "CHAT", "", "", 0, frame.Current(), false)
	builder.Sessions = func() []any {
		entry := wiring.Session.StateEntry()
		entry["id"] = wiring.CatID
		entry["name"] = wiring.DisplayName
		entry["lastContextChangeAt"] = wiring.Session.Context.LastChangeAt()
		return []any{entry}
	}

	// [段9d] 机制面装配——DataBox / OA / 帧序 Runner / 积木表 / 工具组注册（P4 c-1）
	visionSvc := llm.NewVisionService(store, schema, wiring.LLMStore, func(level int, message string) {
		logs.Add("VISION", level, message, "TOOL", "", "", 0, frame.Current(), false)
	})
	// 宿主指令服务（catcfg.bridge 的执行底座）——router 在 [段12a] 构造，用晚绑定闭包占位
	var commandRouterRef *commandRouter
	hostCommand := func(line string) string {
		if commandRouterRef == nil {
			return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪"
		}
		return commandRouterRef.Execute(line)
	}
	configSvc := &configServiceAdapter{store: store, schema: schema}
	// 群组服务面——GroupCat 八件积木的执行底座（组数据面 + 会话指令路由晚绑定：router 在 [段12a] 构造）
	groupSvc := &hostGroupService{
		Store:  groups,
		Router: func() *commandRouter { return commandRouterRef },
		Logf: func(level int, message, category string) {
			logs.Add("CatHome5", level, message, category, "", "", 0, frame.Current(), false)
		},
	}
	toolRT := buildToolRuntime(workspace, bricksIndex, resolveCorpusDir(*corpusFlag, workspace), dataRoot, &webConfigAdapter{store: store, schema: schema, llm: wiring.LLMStore}, visionSvc, func(key string) string {
		return store.Resolve(schema, key).Value
	}, configSvc, hostCommand, func(level int, message, category string) {
		logs.Add("CatHome5", level, message, category, "", "", 0, frame.Current(), false)
	}, &llmStreamAdapter{client: wiring.Client}, groupSvc)
	logs.Add("CatHome5", observe.LevelInfo,
		"toolset.boot | 组 "+itoa(len(toolRT.Groups))+" | 跳过 "+itoa(len(toolRT.Skipped)), "TOOL", "", "", 0, frame.Current(), false)
	// [段9d-0] info 的 db 段数据源回填——会话层先于机制面构造，服务实例此处才存在（闭包按调用时刻读取）
	wiring.DBSvc = toolRT.DBSvc
	// [段9d-1] 库级授权登记——默认猫全放，其余按 cat.cfg dbLibs 白名单（空集合 = 无库权限）
	if toolRT.DBSvc != nil {
		dbLibs := []string{}
		if wiring.CatCfg != nil {
			dbLibs = wiring.CatCfg.DBLibs
		}
		db.RegisterCat(wiring.CatID, strings.EqualFold(wiring.CatID, "majordomo"), dbLibs)
		logs.Add("CatHome5", observe.LevelInfo,
			"db.boot | cat="+wiring.CatID+" | 库根 "+toolRT.DBSvc.Root()+" | dbLibs="+itoa(len(dbLibs)), "DB", "", "", 0, frame.Current(), false)
	}
	// 工具工单面接线——非内置工具 Post → 组 Flow 认领执行 → 100ms Tick 收回执
	wiring.Session.SetToolOffice(&oaToolOffice{oa: toolRT.OA})
	// 轮末总结工单面接线——SVC/LLMSimpleRequest（挂单方 #2；开关与 LLM 配置在 wireSession 已装配）
	wiring.Session.SetRoundSummaryOffice(&oaRoundSummaryOffice{oa: toolRT.OA})
	// 轮末总结播报面接线（A247/A265）——kokoro 合成 → audio.Join 拼接 → Enqueue 播放（失败只记日志）
	wiring.Session.SetRoundSummarySpeaker(newSummarySpeaker(toolRT.Py, toolRT.Audio, func(key string) string {
		return store.Resolve(schema, key).Value
	}, func(level int, message string) {
		logs.Add("CatHome5", level, message, "CHAT", "", "", 0, frame.Current(), false)
	}))
	// 浏览器生命周期接线——timeback 开锚预热（browser_vision）/ 域回收关闭（有头实例保活）
	wiring.Session.Timeback.Browser = toolRT.Browser
	// 图片注入路径规范化——`<rootId>:<rel>` → 绝对路径（与 text-* / cs-* 同一根解析实现）
	if toolRT.FileService != nil {
		fileService := toolRT.FileService
		wiring.Session.SetImagePathResolver(func(path string) string {
			full, err := fileService.Resolve(path, false)
			if err != nil {
				return ""
			}
			return full
		})
	}

	// [段9e] 可观测面接线——cats 段（Flow 注册表 + self_desc 自述）/ boxes 段（DataBox 截面）
	builder.Cats = func() []observe.Cat { return snapshotCats(toolRT.Runner, toolRT.DB) }
	builder.Boxes = func() []observe.Box { return snapshotBoxes(toolRT.DB) }

	// [段10] 端口解析——flag > http.port 配置 > 端口段主端口
	listenPort := band.MainPort
	if configured := store.Resolve(schema, "http.port").Value; configured != "" {
		if parsed, err := strconv.Atoi(configured); err == nil && parsed > 0 {
			listenPort = parsed
		}
	}
	if *portFlag > 0 {
		listenPort = *portFlag
	}

	// [段11] 静态页根
	htmlRoot := resolveHTMLRoot(*htmlRootFlag, workspace)

	// [段12] HTTP 外观层——路由装配 → 起服 → 250ms Pump
	server := cathomehttp.New(cathomehttp.Options{
		Port:      listenPort,
		DataRoot:  dataRoot,
		ConfigDir: configDir,
		HTMLRoot:  htmlRoot,
		Version:   version,
		ChatPage:  *pageFlag,
		Area:      band.Area,
		// FrontendTestPort 区段值——GET /api/v1/frontend-test 转发目标（P3-e 最后一块）
		FrontendTestPort: band.FrontendTestPort,
	})
	server.BindConfig(schema, store)
	server.BindSnapshot(builder.Build)
	server.BindLogs(logs.LogsArrayJSON, logs.LogsResponseJSON)
	server.BindLogIncrement(logs.IncrementArrayJSON)
	server.BindPatch(builder.BuildPatch)
	server.BindDepsRefresh(deps.Refresh)
	server.BindTick(func() { frame.Next() })
	// [段12a] 指令路由——Chat 投递 / session.new 重建（P3-d d3；与 C# InputRoutes 前缀分派同形）
	router := &commandRouter{
		DataRoot:    dataRoot,
		Workspace:   workspace,
		Registry:    toolsResult.Registry,
		Pool:        toolsResult.Pool,
		Session:     wiring.Session,
		CatID:       wiring.CatID,
		DisplayName: wiring.DisplayName,
		Running:     true,
		Port:        listenPort,
		Logf: func(level int, message, category string) {
			logs.Add("CatHome5", level, message, category, "", "", 0, frame.Current(), false)
		},
	}
	server.BindCommand(router.Handle)
	server.BindCats(router.catsJSON)
	server.BindFrame(frame.Current)
	// [段12c] 视图推送面——chat 页唯一数据通道（P3-e）：首帧全量（连接建立补发）+ 每帧增量（无变化零字节）
	server.BindView(wiring.Session.BuildViewFullJSON)
	server.BindViewFrame(wiring.Session.TakeViewFrameJSON)
	wiring.Session.AttachView()
	wiring.Session.AttachConnInfo(server.ClientCount)

	// [段12b] 管理端路由（P3-d 段 1/2/4）——与请求面共用端点池存储（写入即时可见）
	// 宿主重启登记面——构造即把停机态接到会话层钩子（新需求 / 视图推送据此拒收）
	restartState := admin.NewRestartState(func(level int, message string) {
		logs.Add("CatHome5", level, message, "CONFIG", "", "", 0, frame.Current(), false)
	})
	// [段12b-2] 重启链会话侧钩子——T1 请求载荷（DataBox 全局盒）与登记面（置停机态）
	session.RestartRequestPayload = func() string {
		return toolRT.DB.GetString("global", "host_restart_request")
	}
	session.RestartRequestRegister = func(payload string) { restartState.RequestFromPayload(payload) }
	adminDeps := &admin.Deps{
		DataRoot:    dataRoot,
		Schema:      schema,
		Store:       store,
		Workspace:   workspace,
		Registry:    toolsResult.Registry,
		Pool:        toolsResult.Pool,
		Groups:      groups,
		LLMStore:    wiring.LLMStore,
		Session:     wiring.Session,
		CatID:       wiring.CatID,
		DisplayName: wiring.DisplayName,
		Running:     true,
		Port:        listenPort,
		DB:          toolRT.DBSvc,
		Logf: func(category string, level int, message string) {
			logs.Add("CatHome5", level, message, category, "", "", 0, frame.Current(), false)
		},
		Apply: func(catKey string, cfg *config.CatCfg) {
			// 授权面实时——名单落盘即改本次运行的可调工具集（注入面仍新会话刷新）
			applySessionAuth(toolsResult.Registry, wiring.Session, catKey, cfg.ToolNames)
			// 端点面实时——apiConfigId / reasoningEffort 经同步访问器切换（猫级覆盖，不改端点池）
			if wiring.Client != nil {
				wiring.Client.SetBinding(cfg.APIConfigID, cfg.ReasoningEffort)
			}
			// 库白名单实时——dbLibs 落盘即改本猫可访问库集合
			db.RegisterCat(catKey, strings.EqualFold(catKey, "majordomo"), cfg.DBLibs)
		},
		Restart: restartState,
		Dispatch: func(command string) bool {
			return router.Handle(command)
		},
	}
	server.BindAdmin(func(mux *http.ServeMux) { admin.Register(mux, adminDeps) })
	// [段12d] 对话页条件面（d-2）——按 C# HttpHostOptions 构造期条件注册：
	// 仅对话页实例（--page chat.html）注入；管理面板实例（index.html）保持 404（与 C# 管理端口同构）。
	if *pageFlag == "chat.html" {
		server.BindContext(wiring.Session.BuildContextView)
		server.BindKeyInfo(wiring.Session.BuildKeyInfoView)
		server.BindFullContext(wiring.Session.BuildFullContextView)
		server.BindEffort(
			func() string { return adminDeps.BuildEffortJSON(wiring.CatID) },
			func(value string) string { return adminDeps.SetEffortJSON(wiring.CatID, value) },
		)
		server.BindAPIRole(func() string {
			return admin.BuildAPIRoleJSON(wiring.Session, wiring.LLMStore, wiring.CatCfg.APIConfigID)
		})
		server.BindAPIRoleToggle(func() string {
			// 角色状态只有一份（Session 与 Client 共享 EndpointRole）——切换即全链生效
			role := wiring.Session.ToggleAPIRole()
			return `{"ok":true,"role":` + session.JSONString(role) + `}`
		})
		server.BindAPIModels(func() string {
			return admin.BuildAPIModelsJSON(wiring.LLMStore, wiring.CatCfg.APIConfigID, wiring.Session.APIRoleBackup(), wiring.Session.ModelOverride())
		})
		server.BindAPIModel(func(model string) string {
			value := wiring.Session.SetModelOverride(model)
			if wiring.Client != nil {
				wiring.Client.SetModelOverride(value)
			}
			return `{"ok":true,"model":` + session.JSONString(value) + `}`
		})
		server.BindInject(
			adminDeps.InjectProjectsJSON,
			adminDeps.InjectProjectJSON,
			adminDeps.InjectAutoJSON,
			adminDeps.InjectAutoSetJSON,
		)
	}
	// [段12e] mountAuto 消费面——消息里「上工/开工 <项目>」即时挂载（复用管理面挂载执行体，
	// 清单装配仍是 buildInjectListWithMount 单点；禁第二份规则）
	// 同段接线：catcfg.bridge 的宿主指令服务 + cat.cfg.set 的生效 / 校验面（与管理面写面同一实现）
	commandRouterRef = router
	router.Apply = adminDeps.Apply
	router.ValidateToolNames = adminDeps.ValidateToolNames
	router.ValidateEnabledRoots = adminDeps.ValidateEnabledRoots
	router.FilterInjectPaths = adminDeps.FilterInjectPaths
	router.AutoMount = adminDeps.TryAutoMountOnMessage

	if htmlRoot == "" {
		boot.Report("HTML_ROOT_MISSING", observe.KindEnv, "外观层（静态页）", observe.StateEmpty,
			"未定位 html 根（源码区 CatHome4/html 或 --html-root）", "用 --html-root 指定静态页目录，或确认 workspace.json 的 mau 根可达", "S6")
	}

	// 组订阅巡检（p4-3）——静默窗口到期且订阅者有未读时汇总推送（主循环每帧调用，内部 1 秒节流；对应 C# GroupAutoForward.PumpIdle）
	groupForward := &groupForwarder{svc: groupSvc}
	// [段12f] 机制 ticker——100ms 帧序（对齐 C# 主循环）：组 Flow 驱动 → OA 结算 → 会话推进 → 重启闸门。
	// 快照 / SSE 仍走 250ms Pump（观测面节拍与机制节拍分离）。
	gate := newRestartGate(restartState, wiring.Session, toolRT.Runner, toolRT.OA, workspace,
		dataRoot, listenPort, band.Area, *pageFlag, func(level int, message, category string) {
			logs.Add("CatHome5", level, message, category, "", "", 0, frame.Current(), false)
		})
	// 自启面（外壳件 2）——仅交互模式启用（非交互探针不得改写 HKCU Run）；托盘切换经 channel 回主线程执行
	var autoStart autoStartPoller
	var trayToggle chan struct{}
	if !nonInteractive {
		if note := autoStart.init(store, schema); note != "" {
			level := observe.LevelInfo
			if strings.Contains(note, "失败") {
				level = observe.LevelWarn
			}
			logs.Add("CatHome5", level, note, "AUTOSTART", "", "", 0, frame.Current(), false)
		}
		trayToggle = make(chan struct{}, 1)
	}
	restartCh := make(chan struct{}, 1)
	tickStop := make(chan struct{})
	tickerDone := make(chan struct{})
	if nonInteractive {
		// --run 模式不启 ticker——单指令驱动在主 goroutine 串行推进（见 [段12g-1]）
		close(tickerDone)
	} else {
		go func() {
			defer close(tickerDone)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			ticks := 0
			for {
				select {
				case <-tickStop:
					return
				case <-trayToggle:
					handleAutoStartToggle(store, schema, logs)
				case <-ticker.C:
					toolRT.Runner.Tick()
					wiring.Session.Pump()
					// 延迟队列泵（c-2）——到点条目转 sys 注入队（停机态保留；对齐 C# ChatBridge.PumpDelays）
					wiring.PumpDelays()
					// 组订阅巡检（p4-3）——静默窗口到期且订阅者有未读时汇总推送（对齐 C# RunInteractive 的 PumpIdle 位点）
					groupForward.Pump()
					// 按猫新会话请求（cat.new-session）——仅 Idle 时执行（轮内不重建）
					router.ConsumePendingNewSession()
					// 重启闸门（T3/T4）——通过后接力已拉起，主循环走退出通道
					if gate.Tick() {
						restartCh <- struct{}{}
					}
					// 自启配置轮询（5 s 粒度——覆盖 config-* / 前端写路径；C# CheckAutoStartPoll 同义）
					ticks++
					if ticks%50 == 0 {
						if note := autoStart.poll(store, schema); note != "" {
							level := observe.LevelInfo
							if strings.Contains(note, "失败") {
								level = observe.LevelWarn
							}
							logs.Add("CatHome5", level, note, "AUTOSTART", "", "", 0, frame.Current(), false)
						}
					}
				}
			}
		}()
	}

	// [段12g] 重启回执注入（T6）——上一宿主经 --majordomopush 传来的部署结论（一次性 system 消息）
	if push := strings.TrimSpace(*pushFlag); push != "" {
		if reply := wiring.Session.PostSystemMessage(session.SysKindRestart, push, ""); reply != "OK" {
			logs.Add("CatHome5", observe.LevelWarn, "宿主启动回执注入未受理："+reply, "RESTART", "", "", 0, frame.Current(), false)
		} else {
			logs.Add("CatHome5", observe.LevelInfo, "宿主启动回执已登记（重启链 T6——注入 Majordomo 会话）", "RESTART", "", "", 0, frame.Current(), false)
		}
	}

	// [段12g-1] 单指令模式（--run）——非交互：不监听端口 / 不拉前端测试 / 不起托盘；
	// 投递一条指令 → 单 goroutine 驱动至全局 Idle → 输出 → 收尾退出（对齐 C# RunSingle）
	if nonInteractive {
		driver := runDriver{
			router: router,
			gate:   gate,
			tick: func() {
				toolRT.Runner.Tick()
				wiring.Session.Pump()
				wiring.PumpDelays()
				router.ConsumePendingNewSession()
			},
			state: func() (string, int, int) {
				runState, _, _ := wiring.Session.GetRunState()
				return runState, wiring.Session.Context.Count(), len(wiring.Session.Views.Blocks())
			},
		}
		exitCode := runSingleCommand(driver, *runFlag)
		if cmdCaptureFile != nil {
			outln("[CMD] 输出已落盘: " + cmdCaptureFile.Path())
		}
		// 收尾——不启 HTTP server / 前端测试服务（headless），只关外部子服务与观测写者
		close(tickStop)
		<-tickerDone
		shutdownHost(toolRT, logs)
		if cmdCaptureFile != nil {
			cmdCaptureFile.Close()
		}
		os.Exit(exitCode)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.Pump()
	}()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()

	startLine := fmt.Sprintf("CatHome5 宿主 | version=%s | area=%s | port=%d | dataRoot=%s | html=%s",
		version, band.Area, listenPort, dataRoot, htmlRoot)
	outln(startLine)
	logs.Add("CatHome5", observe.LevelInfo, startLine, "SYS", "", "", 0, 0, false)
	// [段12d-2] 托盘常驻（外壳件 1）——交互模式专属；失败记降级、宿主继续跑（托盘不可用不拖垮服务）
	var tray *trayController
	if !nonInteractive {
		tray = &trayController{
			exitCh:      make(chan struct{}, 1),
			toggleCh:    trayToggle,
			version:     version,
			area:        band.Area,
			dataRoot:    dataRoot,
			panelURL:    "http://127.0.0.1:" + strconv.Itoa(listenPort) + "/",
			autoStartOn: func() bool { return autoStartOn(store, schema) },
			logf: func(level int, message, category string) {
				logs.Add("CatHome5", level, message, category, "", "", 0, frame.Current(), false)
			},
		}
		if started, err := startTray(tray); err != nil {
			tray = nil
			logs.Add("CatHome5", observe.LevelWarn, "托盘未启用："+err.Error(), "TRAY", "", "", 0, frame.Current(), false)
			boot.Report("TRAY_UNAVAILABLE", observe.KindEnv, "外壳（托盘）", observe.StateUnavailable,
				"托盘初始化失败："+err.Error(), "重启宿主重试；不影响宿主服务", "S6")
		} else {
			tray = started
			logs.Add("CatHome5", observe.LevelInfo, "托盘已就绪（左键开面板 / 右键菜单）", "TRAY", "", "", 0, frame.Current(), false)
		}
	}
	// [段12d] 前端测试服务附属——本区段端口未监听则拉起 html/tests/server.js（P3-e 最后一块）
	// 对齐 C# StageS7「前端测试服务拉起」：已监听跳过（多实例保护）；依赖缺失 / 拉起失败记降级。
	feResult := fetest.Ensure(fetest.Config{
		Port:    band.FrontendTestPort,
		Root:    os.Getenv("CAT5_FE_TEST_ROOT"),
		MauRoot: repoRootOf(workspace),
		ExeDir:  config.ExeDir(),
	})
	if feResult.Service != nil {
		defer feResult.Service.Stop()
	}
	switch feResult.State {
	case fetest.StateStarted:
		logs.Add("CatHome5", observe.LevelInfo, "前端测试服务已启动："+feResult.Detail+"（cwd="+os.TempDir()+"）", "CONFIG", "", "", 0, frame.Current(), false)
	case fetest.StateListening:
		logs.Add("CatHome5", observe.LevelInfo, "前端测试服务已在监听，跳过拉起："+feResult.Detail, "CONFIG", "", "", 0, frame.Current(), false)
	case fetest.StateNoDir:
		logs.Add("CatHome5", observe.LevelWarn, "前端测试服务未拉起：未找到含 server.js 的测试目录（可用 CAT5_FE_TEST_ROOT 指定）", "CONFIG", "", "", 0, frame.Current(), false)
	case fetest.StateMissingDeps:
		logs.Add("CatHome5", observe.LevelWarn, "前端测试服务未拉起："+feResult.Detail+"——请在该目录执行 npm install 后重启", "CONFIG", "", "", 0, frame.Current(), false)
		boot.Report("FE_TEST_SERVICE_FAILED", observe.KindEnv, "前端测试服务", observe.StateUnavailable,
			"前端测试服务依赖缺失："+feResult.Detail, "在测试目录执行 npm install 后重启宿主", "S7")
	case fetest.StateFailed:
		logs.Add("CatHome5", observe.LevelErr, "前端测试服务拉起失败："+feResult.Detail, "CONFIG", "", "", 0, frame.Current(), false)
		boot.Report("FE_TEST_SERVICE_FAILED", observe.KindEnv, "前端测试服务", observe.StateUnavailable,
			"前端测试服务拉起失败："+feResult.Detail, "开发期手动执行 node server.js（html/tests 目录）后重启", "S7")
	}
	if boot.HasAny() {
		logs.Add("CatHome5", observe.LevelWarn, boot.BuildSummaryLine(), "SYS", "", "", 0, 0, false)
	}

	// [段13] 退出——信号 / 托盘退出 / 重启交接 / HTTP 异常 → 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	var trayExitCh <-chan struct{}
	if tray != nil {
		trayExitCh = tray.exitCh
	}
	select {
	case sig := <-sigCh:
		logs.Add("CatHome5", observe.LevelInfo, "收到退出信号："+sig.String(), "SYS", "", "", 0, 0, false)
	case <-trayExitCh:
		logs.Add("CatHome5", observe.LevelInfo, "收到托盘退出请求——优雅收尾", "TRAY", "", "", 0, frame.Current(), false)
	case <-restartCh:
		logs.Add("CatHome5", observe.LevelInfo, "重启交接完成——宿主收尾退出（接力者已接管部署）", "RESTART", "", "", 0, frame.Current(), false)
	case err := <-errCh:
		if err != nil {
			logs.Add("CatHome5", observe.LevelErr, "HTTP 服务异常退出："+err.Error(), "SYS", "", "", 0, 0, false)
			fmt.Fprintln(os.Stderr, "HTTP 服务异常退出: "+err.Error())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	close(tickStop)
	<-tickerDone
	wg.Wait()
	shutdownHost(toolRT, logs)
}

// shutdownHost 宿主退出收尾——外部子服务关停 + 观测写者关闭（--run 与常驻共用）。
func shutdownHost(toolRT *toolRuntime, logs *observe.LogStore) {
	// 浏览器实例收尾——有头实例保活（人工登录窗口等待人的动作），其余 CDP Browser.close 清理
	if toolRT.Browser != nil {
		toolRT.Browser.Shutdown()
	}
	// ts 服务收尾——关 stdin 让 node 退出（超时强杀进程树），不留孤儿
	if toolRT.Ts != nil {
		toolRT.Ts.Shutdown()
	}
	// cs 桥收尾——关停自管 MauCsBridge 子进程（复用到的外部实例不动）
	if toolRT.Cs != nil {
		toolRT.Cs.Shutdown()
	}
	// py 服务收尾——停止全部已拉起服务进程（stdio 服务池），不留孤儿
	if toolRT.Py != nil {
		toolRT.Py.Shutdown()
	}
	logs.Close()
}

// bootSink 是启动早期诊断 sink——三级落点：数据根 runs/<ts> → %TEMP%/CatHome5/runs/<ts> → 仅内存。
type bootSink struct {
	mu     sync.Mutex
	lines  []string
	dir    string
	mode   string
	failed bool
}

// newBootSink 创建空 sink。
func newBootSink() *bootSink { return &bootSink{mode: "memory"} }

// Init 解析落点并写一行头部（幂等）。
func (b *bootSink) Init(dataRoot string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dir != "" || b.mode != "memory" {
		return
	}
	stamp := time.Now().Format("20060102_150405")
	if dataRoot != "" {
		normal := filepath.Join(dataRoot, "runs", stamp)
		if tryOpenDir(normal) {
			b.dir = normal
			b.mode = "normal"
			b.appendHeadLocked(stamp, "normal")
			return
		}
	}
	temp := filepath.Join(os.TempDir(), "CatHome5", "runs", stamp)
	if tryOpenDir(temp) {
		b.dir = temp
		b.mode = "temp"
		b.appendHeadLocked(stamp, "temp")
		return
	}
	b.lines = append(b.lines, "==== CatHome5 启动诊断 | "+stamp+" | sink=memory | version="+version+" ====")
}

// Add 记一条早期诊断——内存必留，磁盘尽力写（失败置标志，不抛）。
func (b *bootSink) Add(stage, message string) {
	line := time.Now().Format("2006-01-02 15:04:05.000") + " | " + stage + " | " + message
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if b.dir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(b.dir, "startup.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		b.failed = true
		return
	}
	_, writeErr := f.WriteString(line + "\r\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		b.failed = true
	}
}

// Drain 取走全部缓冲行并清空内存（回灌日志总账用）。
func (b *bootSink) Drain() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	b.lines = nil
	return out
}

// Dir 返回实际落点目录（空 = 仅内存）。
func (b *bootSink) Dir() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dir
}

// appendHeadLocked 写头部行（调用方持锁）。
func (b *bootSink) appendHeadLocked(stamp, mode string) {
	head := "==== CatHome5 启动诊断 | " + stamp + " | sink=" + mode + " | version=" + version + " ===="
	b.lines = append(b.lines, head)
	f, err := os.OpenFile(filepath.Join(b.dir, "startup.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		b.failed = true
		return
	}
	_, _ = f.WriteString(head + "\r\n")
	_ = f.Close()
}

// tryOpenDir 目录可用性探测——建目录 + 试写一行探针。
func tryOpenDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, "startup.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// mustMkdir 建目录（失败不阻断——后续写面各自降级出声）。
func mustMkdir(dir string) {
	_ = os.MkdirAll(dir, 0o755)
}

// resolveHTMLRoot 解析静态页根：flag > 环境变量 > workspace 的 mau 根 > 可执行目录。
func resolveHTMLRoot(flagValue string, workspace *config.WorkspaceConfig) string {
	candidates := make([]string, 0, 4)
	if flagValue != "" {
		candidates = append(candidates, flagValue)
	}
	if env := os.Getenv("CAT5_HTML_ROOT"); env != "" {
		candidates = append(candidates, env)
	}
	if root := repoRootOf(workspace); root != "" {
		candidates = append(candidates, filepath.Join(root, "CatHome4", "html"))
	}
	candidates = append(candidates, filepath.Join(config.ExeDir(), "html"))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil {
			if abs, absErr := filepath.Abs(candidate); absErr == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

// repoRootOf 取 workspace 里 id=mau 的根路径（空 = 未配置）。
func repoRootOf(workspace *config.WorkspaceConfig) string {
	if workspace == nil {
		return ""
	}
	for _, root := range workspace.Roots {
		if strings.EqualFold(root.ID, "mau") {
			return root.Path
		}
	}
	return ""
}

// resolveBricksIndex 解析 Bricks 索引路径：flag > 环境变量 CAT5_BRICKS > workspace 的 mau 根。
func resolveBricksIndex(flagValue string, workspace *config.WorkspaceConfig) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("CAT5_BRICKS"); env != "" {
		return env
	}
	if dir := config.ExeDir(); dir != "" {
		candidate := filepath.Join(dir, "Bricks", "index.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if root := repoRootOf(workspace); root != "" {
		return filepath.Join(root, "Bricks", "index.json")
	}
	return ""
}

// resolveCorpusDir 解析语料根：flag > 环境变量 CAT5_CORPUS > <exeDir>/corpus/ch4 > workspace 的 mau 根。
//
// 显式 flag 原样返回（不可读由 LoadGroups 出声）；其余候选按目录存在判定——
// 部署实例自持 corpus/ch4，施工仓实例回落 mau 仓。
func resolveCorpusDir(flagValue string, workspace *config.WorkspaceConfig) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("CAT5_CORPUS"); env != "" {
		return env
	}
	if dir := config.ExeDir(); dir != "" {
		candidate := filepath.Join(dir, "corpus", "ch4")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	if root := repoRootOf(workspace); root != "" {
		candidate := filepath.Join(root, "corpus", "ch4")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// snapshotTools 把注册表投影为快照 tools 段（C# BuildToolsJson 同口径：name / group / builtin）。
func snapshotTools(reg *toolpool.Registry) []observe.Tool {
	entries := reg.All()
	out := make([]observe.Tool, 0, len(entries))
	for _, entry := range entries {
		out = append(out, observe.Tool{Name: entry.Name, Group: entry.OwnerFlow, Builtin: entry.IsBuiltin})
	}
	return out
}

// toRootPaths 转观测面根表视图。
func toRootPaths(workspace *config.WorkspaceConfig) []observe.RootPath {
	if workspace == nil {
		return nil
	}
	out := make([]observe.RootPath, 0, len(workspace.Roots))
	for _, root := range workspace.Roots {
		out = append(out, observe.RootPath{ID: root.ID, Path: root.Path, Writable: root.Writable})
	}
	return out
}

// rootsAuditNote 受控根对账文本——生效根 + 同 path 多根检出。
func rootsAuditNote(workspace *config.WorkspaceConfig) string {
	if workspace == nil || len(workspace.Roots) == 0 {
		return "无受控根"
	}
	var ids []string
	seen := make(map[string]bool)
	var conflicts []string
	for _, root := range workspace.Roots {
		ids = append(ids, root.ID+"="+root.Path)
		key := strings.ToLower(root.Path)
		if seen[key] {
			conflicts = append(conflicts, root.ID)
		}
		seen[key] = true
	}
	note := "生效 " + itoa(len(workspace.Roots)) + " 根：" + strings.Join(ids, " · ")
	if len(conflicts) > 0 {
		note += " | 同 path 多根：" + strings.Join(conflicts, ", ")
	}
	return note
}

// splitDegrade 拆分 `CODE|state|detail` 形态的降级行。
func splitDegrade(raw string) (code, state, detail string, ok bool) {
	parts := strings.SplitN(raw, "|", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// mapDegradeState 把配置面降级状态映射为 boot 形态词。
func mapDegradeState(state string) string {
	switch state {
	case "fallback", "use-subdir", "rebuilt", "unavailable", "empty", "readonly":
		return state
	default:
		return observe.StateFallback
	}
}

// itoa 整数转字符串。
func itoa(v int) string { return strconv.Itoa(v) }
