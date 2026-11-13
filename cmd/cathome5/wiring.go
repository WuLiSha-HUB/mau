package main

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cathome5/host/admin"
	"cathome5/host/config"
	"cathome5/host/llm"
	"cathome5/host/observe"
	"cathome5/host/session"
	"cathome5/host/toolpool"
	maudb "cathome5/mau/db"
	"cathome5/mau/fs"
)

// sessionWiring 是会话装配结果——宿主主流程需要的全部句柄。
type sessionWiring struct {
	// Session 会话实体
	Session *session.Session
	// CatID 会话 key（猫 key）
	CatID string
	// DisplayName 显示名
	DisplayName string
	// Authorized 当前授权工具名（实时口径快照）
	Authorized []string
	// Err 装配降级原因（空 = 正常）
	Err string
	// Client LLM 客户端（端点切换的运行时句柄）
	Client *llm.Client
	// LLMStore 端点池存储（管理面与请求面共用同一实例——写入即时可见）
	LLMStore *config.LlmAPIStore
	// CatCfg 本猫配置（dbLibs 等库级授权字段的数据源）
	CatCfg *config.CatCfg
	// DBSvc 条目库服务（info 的 db 段数据源）——nil = 服务未绑定，info 如实出声
	//
	// 装配顺序：会话层先于机制面构造（机制面要用会话句柄），故本字段在 buildToolRuntime
	// 之后由 main 回填；info 数据源闭包按调用时刻读取，回填即生效。
	DBSvc *maudb.Service
	// Delays 延迟队列（timer 排程注入 + state.delay 段数据源）
	Delays *session.DelayQueue
}

// wireCatSession 装配一只猫的会话——cat.cfg + 前文 + 视图 + 工具面 + LLM 客户端。
//
// 失败面全部降级出声：cat.cfg 缺失按默认值、前文缺失按空、LLM 端点缺失由请求期诚实报错。
// llmStore / delays 为共享实例（nil = 自建——单猫测试 / 直达场景）；多猫共用同一实例
// （管理面写入即时可见 / 延迟队列单文件单写面，Pump 按 catKey 分发）。
func wireCatSession(dataRoot string, workspace *config.WorkspaceConfig, toolsResult *toolpool.BuildResult, schema *config.Schema, store *config.Store, llmStore *config.LlmAPIStore, delays *session.DelayQueue, catKey string) *sessionWiring {
	wiring := &sessionWiring{CatID: catKey, DisplayName: catKey}
	catID := wiring.CatID
	catDir := filepath.Join(dataRoot, "sessions", catID)
	cfgPath := filepath.Join(catDir, "cat.cfg")
	cfg, err := config.LoadCatCfg(cfgPath)
	if err != nil {
		wiring.Err = "cat.cfg 读取失败（按默认值）: " + err.Error()
		cfg = &config.CatCfg{ID: catID, DisplayName: catID}
	}
	if cfg.ID != "" {
		catID = cfg.ID
		wiring.CatID = catID
	}
	if cfg.DisplayName != "" {
		wiring.DisplayName = cfg.DisplayName
	}
	wiring.CatCfg = cfg

	sessionStore := session.NewSessionStore(filepath.Join(dataRoot, "sessions", catID, catID+".jsonl"))
	// 元数据落点——sessions/<id>/<id>.session.json（P3-c 单会话；多猫归 P3-d）
	session.SessionMetaPathProvider = func(key string) string {
		return filepath.Join(dataRoot, "sessions", key, key+".session.json")
	}
	messages, _ := sessionStore.TryLoad()
	ctx := session.NewContext()
	if len(messages) > 0 {
		ctx.ReplaceMessages(messages)
	}
	views := session.NewViewStore(filepath.Join(dataRoot, "sessions", catID, catID+".view.jsonl"))
	views.Load()
	// 载入期受限重建（design-ch4-protocol §10.2 / §10.3）——顶尾补差 + 对账哨兵：
	// 视图层为展示面权威，前文修复不级联重建；缺口由补差（append-only、不改已有条目）
	// 与对账（只读出声、受限抽样）处置。
	reconcileViewOnLoad(views, messages)
	sess := session.NewSession(catID, ctx, sessionStore, views)
	sess.SetDisplayName(wiring.DisplayName)
	// 桌宠种类——外观池解析（cat.cfg appearanceId 零值 = 跟随默认外观；池缺失 / 引用失效 = 空 = 无桌宠）
	appearancePool := config.LoadAppearancePool(filepath.Join(dataRoot, "config", "appearance.json"))
	sess.SetPetKind(resolvePetKind(appearancePool, cfg.AppearanceID))
	wiring.Session = sess

	// 延迟队列（c-2）——timer 排程注入 + state.delay 段数据源（落盘 runtime/delays.json，跨重启保留）
	// 多猫共享单实例（C# 静态类 + 全局表等价物）：条目自持 cat 归属，Pump 由注册表按 catKey 分发。
	if delays == nil {
		delays = session.NewDelayQueue(filepath.Join(dataRoot, "runtime", "delays.json"))
		delays.Load()
	}
	sess.SetDelayQueue(delays)
	wiring.Delays = delays
	// 加载包数据面（c-2）——池解析三道门（config）+ 受控根路径展开 + 文本读取（与前文注入同源）
	packFS := &fs.Service{Roots: fsRoots(workspace)}
	sess.SetPackProvider(
		func(catKey, key string) string { return config.BuildPackPayload(dataRoot, catKey, key) },
		func(path string) (string, error) { return packFS.Resolve(path, false) },
		func(absPath string) (string, error) { return packFS.ReadTextAuto(absPath) },
	)

	// 工具面——授权集实时口径快照 → 声明面裁剪 → 会话快照优先（A221：重启不改面，仅 session.new 换新）
	includePrivileged := toolpool.IsDefaultCatKey(catID)
	authorized := toolsResult.Registry.ResolveToolNames(cfg.ToolNames, includePrivileged)
	wiring.Authorized = authorized
	defs := toolsResult.Registry.FilterToolSpecs(authorized, includePrivileged)
	poolSpecs := make([]session.ToolSpec, 0, len(defs))
	for _, def := range defs {
		poolSpecs = append(poolSpecs, session.ToolSpec{Name: def.Name, Description: def.Description, ParametersJSON: def.ParametersJSON})
	}
	// 持久化快照优先（旧档 / 首建回落池面；退役剔除出声）——与 C# ChatSession.ResolveSessionTools 同序
	// 特权面判定先接线：非默认会话的注入面过滤读注册面组级标记（默认会话永久激活，行为不变）
	session.IsPrivilegedTool = toolsResult.Registry.IsPrivilegedName
	sess.SetToolSpecs(session.ResolveSessionTools(catID, poolSpecs))

	// LLM 客户端——端点池 + 密钥 + 全局配置读取（存储与管理面共用同一实例；多猫共享传入实例）
	if llmStore == nil {
		llmStore = config.LoadLlmAPIStore(
			filepath.Join(dataRoot, "config", "llm-api.json"),
			filepath.Join(dataRoot, "secrets", "llm-api.cfg"),
		)
		if llmStore.LastError() != "" {
			wiring.Err = llmStore.LastError()
		}
	}
	wiring.LLMStore = llmStore
	pool := llm.NewPoolFromStore(llmStore)
	// 轮末总结——开关取外观池条目（cat.cfg appearanceId 零值跟随默认外观）；LLM 配置取 apiConfigId → 端点池。
	// 工单面（OA）与播报执行体（py / audio）在机制面构造之后建立，由 main 回填（对齐 DBSvc 的回填口径）。
	sess.SetRoundSummary(config.ResolveRoundSummary(appearancePool, cfg.AppearanceID))
	sess.SetRoundSummaryLlm(func() []string {
		return resolveRoundSummaryLlm(llmStore, cfg.APIConfigID)
	})
	// 总结出声配置（A247）——外观池同一套生效解析：[启用标志, 音色槽位]（与 C# SummaryAudioProvider 同形）
	sess.SetRoundSummaryAudioProvider(func(catKey string) []string {
		enabled, voice := config.ResolveSummaryAudio(appearancePool, cfg.AppearanceID)
		flag := "0"
		if enabled {
			flag = "1"
		}
		return []string{flag, voice}
	})
	// 会话端点角色（c-3）——会话与客户端共享同一实例：手动对调 / 自动故障转移窗口都只改这一份状态
	apiRole := &llm.EndpointRole{}
	sess.SetAPIRole(apiRole)
	client := &llm.Client{
		Pool:            pool,
		APIConfigID:     cfg.APIConfigID,
		ReasoningEffort: cfg.ReasoningEffort,
		Role:            apiRole,
		Settings: func(key string) string {
			return store.Resolve(schema, key).Value
		},
	}
	llm.Logf = func(level int, message string) {
		session.Logf("LLM", level, message, "LLM")
	}
	sess.SetStreamer(&llm.SessionStreamer{Client: client})
	wiring.Client = client
	sess.SetCacheUserID(resolveCacheIsolationKey(store, schema, catID))
	// 入站自报身份——自家 apis（回环 + 18100）请求附加头数据源；挂载项按 cat.cfg 初值
	client.SelfTag = sess.SelfTag
	sess.SetClientID("cat-home5/" + version)
	sess.SetSelfTagMount(cfg.ProjectMount, admin.MountModeText(cfg.ProjectMount, cfg.ProjectMountExpert), admin.BuildJobList(cfg.InjectList))

	// 授权面——特权 + 注册表 + 当前名单（与 C# IsToolAllowed 同序）
	authorizedSet := make(map[string]bool, len(authorized))
	for _, name := range authorized {
		authorizedSet[name] = true
	}
	sess.SetAuthorizer(func(name string) bool {
		if toolsResult.Registry.IsPrivilegedName(name) && !sess.IsDefaultSession() {
			return false
		}
		if _, ok := toolsResult.Registry.Find(name); !ok {
			return false
		}
		return authorizedSet[name]
	})
	sess.SetBuiltinExecutor(func(name string) bool {
		entry, ok := toolsResult.Registry.Find(name)
		return ok && entry.IsBuiltin
	}, sess.ExecuteBuiltin)
	// 非内置执行体——P3-c 未接 OA/积木执行面：显式 nil → 诚实 ERR（不静默降级）
	sess.SetToolExecutor(nil)

	// 环境自省——info 内置工具数据源（只给可计算段，不造值）；db 段数据源按调用时刻读取（装配期回填）
	sess.SetInfoProvider(func() string {
		return buildInfoJSON(sess, workspace, catID, wiring.DisplayName, wiring.DBSvc)
	})
	return wiring
}

// reconcileViewOnLoad 载入期受限重建——顶尾补差 + 对账哨兵（design-ch4-protocol §10.2 / §10.3）。
//
// 视图层为展示面权威，前文修复不级联重建：前文有而视图缺的条目按补差口径顺补（append-only、
// 幂等、不改已有条目，返回补齐数）；已有条目按受限样本核对（索引越界 / 角色不符出声，不修改）。
// 补差数即视图缺口规模——非零出声（修复动作必须可见）。
func reconcileViewOnLoad(views *session.ViewStore, messages []session.Message) (int, int) {
	added := views.AppendTailMissing(messages)
	if added > 0 {
		session.Logf(session.LogModuleViewStore, 1, "载入顶尾补差：补齐 "+strconv.Itoa(added)+" 条视图块（前文 "+strconv.Itoa(len(messages))+" 条）", session.LogCategorySys)
	}
	return added, views.AuditEntries(messages, 5)
}

// resolvePetKind 解析猫的桌宠种类——cat.cfg appearanceId 零值 / 非法 = 跟随默认外观；
// 池缺失或引用失效 = 空串（无桌宠——前端按空值不渲染，不回落默认素材）。
func resolvePetKind(pool []config.AppearanceConfig, appearanceID string) string {
	if len(pool) == 0 {
		return ""
	}
	target := config.ParseGUID(appearanceID)
	if target == "" || config.IsZeroGUID(target) {
		for _, entry := range pool {
			if entry.IsDefault {
				return entry.PetKind
			}
		}
		return ""
	}
	for _, entry := range pool {
		if entry.AppearanceID == target {
			return entry.PetKind
		}
	}
	return ""
}

// resolveRoundSummaryLlm 解析轮末总结 LLM 配置——返回 [endpoint, apikey, model]；不可用 nil。
//
// 语义对齐 C# ResolveRoundSummaryLlm（`CatHostService.Cats.cs:97-130`）：apiConfigId 非零 → 池内该条；
// 零值 / 非法 → 默认标记条目（无默认不回落首条）；端点空 = 不可用（不静默放行）。
func resolveRoundSummaryLlm(store *config.LlmAPIStore, apiConfigID string) []string {
	if store == nil {
		return nil
	}
	id := config.ParseGUID(apiConfigID)
	var cfg config.LlmAPIConfig
	ok := false
	if id != "" && !config.IsZeroGUID(id) {
		cfg, ok = store.TryGet(id)
	} else {
		cfg, ok = store.ResolveDefault()
	}
	if !ok || cfg.Endpoint == "" {
		return nil
	}
	return []string{cfg.Endpoint, store.Secret(cfg.APIConfigID), cfg.DefaultModel}
}

// resolveCacheIsolationKey 缓存隔离键——llm.cache_isolation：cat=猫键 / off=空；其余按猫键。
func resolveCacheIsolationKey(store *config.Store, schema *config.Schema, catID string) string {
	mode := strings.TrimSpace(store.Resolve(schema, "llm.cache_isolation").Value)
	if mode == "off" {
		return ""
	}
	return catID
}

// buildInfoJSON 构建 info 内置工具返回块——只输出可计算段（时间 / LLM / 根表 / 前文长度 / 库清单 / 工具面）。
func buildInfoJSON(sess *session.Session, workspace *config.WorkspaceConfig, catID, displayName string, dbSvc *maudb.Service) string {
	var roots []string
	if workspace != nil {
		for _, root := range workspace.Roots {
			roots = append(roots, `{"id":`+session.JSONString(root.ID)+
				`,"writable":`+strconv.FormatBool(root.Writable)+
				`,"path":`+session.JSONString(root.Path)+`}`)
		}
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	return "{\n" +
		`  "version": {"version": ` + session.JSONString(version) + `},` + "\n" +
		`  "time": {"now": ` + session.JSONString(now) + `},` + "\n" +
		`  "llm": {"cat": ` + session.JSONString(catID) + `, "name": ` + session.JSONString(displayName) + `},` + "\n" +
		`  "roots": [` + strings.Join(roots, ",") + `],` + "\n" +
		`  "tokens": {"context": ` + strconv.FormatInt(sess.ContextTokensKnown(), 10) + `},` + "\n" +
		`  "db": ` + buildInfoDBBlock(catID, dbSvc, workspace) + `,` + "\n" +
		`  "tools": {"declared": ` + strconv.Itoa(len(sess.ToolSpecs())) + `}` + "\n" +
		"}"
}

// buildInfoDBBlock 构建 info 的 db 段——授权库清单（对齐 C# `Program.cs` [段8b]）。
//
// 判据取 ListLibs（按 catID 授权过滤 + 摘要自证全集）：服务未绑定 / 读失败都写进 reason，
// 不静默省略该段。rootVisible 判受控根表里是否存在 id 等于 db 的根（大小写不敏感），
// 库根被裁剪成不可见时该值即 false——它就是「库权限与根表是否对得上」的自证字段。
func buildInfoDBBlock(catID string, dbSvc *maudb.Service, workspace *config.WorkspaceConfig) string {
	var b strings.Builder
	b.WriteString(`{"ok": `)
	if dbSvc == nil {
		b.WriteString(`false, "reason": `)
		b.WriteString(session.JSONString("数据库服务未绑定"))
	} else {
		result := dbSvc.ListLibs(catID)
		b.WriteString(strconv.FormatBool(result.OK))
		if result.OK {
			b.WriteString(`, "summary": `)
			b.WriteString(session.JSONString(result.Message))
			if libs := dbLibsPayload(result.Data); libs != "" {
				b.WriteString(`, "libs": `)
				b.WriteString(libs)
			}
		} else {
			b.WriteString(`, "reason": `)
			b.WriteString(session.JSONString(strings.TrimSpace(result.Code + " " + result.Message)))
		}
	}
	b.WriteString(`, "rootVisible": `)
	b.WriteString(strconv.FormatBool(hasDBRoot(workspace)))
	b.WriteString("}")
	return b.String()
}

// dbLibsPayload 从 ListLibs 载荷里取 libs 数组原文——缺失 / 非数组返回空串（该键不输出）。
//
// 载荷由库服务自造（`mau/db` objectJSON），此处只做形态校验；解析失败出声不静默吞
// （该键静默消失与「库里没库」在回执上不可区分）。
func dbLibsPayload(data string) string {
	if strings.TrimSpace(data) == "" {
		return ""
	}
	var payload struct {
		Libs json.RawMessage `json:"libs"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		session.Logf("CatHome5", 2, "info.db | 库清单载荷解析失败: "+err.Error(), session.LogCategoryTool)
		return ""
	}
	libs := strings.TrimSpace(string(payload.Libs))
	if len(libs) < 2 || libs[0] != '[' || libs[len(libs)-1] != ']' {
		return ""
	}
	return libs
}

// hasDBRoot 受控根表里是否有 db 根（大小写不敏感——根 id 小写归一前的旧配置也认）。
func hasDBRoot(workspace *config.WorkspaceConfig) bool {
	if workspace == nil {
		return false
	}
	for _, root := range workspace.Roots {
		if strings.EqualFold(root.ID, "db") {
			return true
		}
	}
	return false
}

// attachSessionRuntime 会话运行时后置接线——机制面（toolRT）构造之后可用的全部依赖回填。
//
// 装配顺序：会话层先于机制面构造（机制面要用会话句柄）→ 本函数把机制面句柄回填给会话；
// 默认猫启动、cat.start 补建与启动扫描共用同一接线点（单一实现，不复制）。
func attachSessionRuntime(w *sessionWiring, toolRT *toolRuntime, store *config.Store, schema *config.Schema, logs *observe.LogStore, frame *observe.FrameCounter) {
	if w == nil || w.Session == nil || toolRT == nil {
		return
	}
	sess := w.Session
	// info 的 db 段数据源回填——闭包按调用时刻读取，回填即生效
	w.DBSvc = toolRT.DBSvc
	// 工具工单面——非内置工具 Post → 组 Flow 认领执行 → 100ms Tick 收回执
	sess.SetToolOffice(&oaToolOffice{oa: toolRT.OA})
	// 轮末总结工单面（SVC/LLMSimpleRequest）与播报面（kokoro → audio.Join → Enqueue）
	sess.SetRoundSummaryOffice(&oaRoundSummaryOffice{oa: toolRT.OA})
	sess.SetRoundSummarySpeaker(newSummarySpeaker(toolRT.Py, toolRT.Audio, func(key string) string {
		return store.Resolve(schema, key).Value
	}, func(level int, message string) {
		logs.Add("CatHome5", level, message, "CHAT", "", "", 0, frame.Current(), false)
	}))
	// 浏览器生命周期——timeback 开锚预热（browser_vision）/ 域回收关闭（有头实例保活）
	sess.Timeback.Browser = toolRT.Browser
	// 图片注入路径规范化——`<rootId>:<rel>` → 绝对路径（与 text-* / cs-* 同一根解析实现）
	if toolRT.FileService != nil {
		fileService := toolRT.FileService
		sess.SetImagePathResolver(func(path string) string {
			full, err := fileService.Resolve(path, false)
			if err != nil {
				return ""
			}
			return full
		})
	}
	// 库级授权登记——默认猫全放，其余按 cat.cfg dbLibs 白名单（空集合 = 无库权限）
	dbLibs := []string{}
	if w.CatCfg != nil {
		dbLibs = w.CatCfg.DBLibs
	}
	maudb.RegisterCat(w.CatID, strings.EqualFold(w.CatID, "majordomo"), dbLibs)
}
