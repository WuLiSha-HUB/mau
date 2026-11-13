package main

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"cathome5/host/config"
	"cathome5/host/llm"
	"cathome5/host/observe"
	"cathome5/host/session"
	"cathome5/mau/bricks"
	maudb "cathome5/mau/db"
	"cathome5/mau/fs"
	"cathome5/mau/runtime"
	"cathome5/mau/toolset"
)

// toolOwnerID 是工具工单的挂单方 ID（C# ToolOwnerId 同值）。
const toolOwnerID = int64(1)

// oaToolOffice 把运行时 OA 适配为会话侧工具工单面（组合根适配——会话层不依赖运行时实现）。
type oaToolOffice struct {
	// oa 工单撮合器
	oa *runtime.OA
}

// Post 上架一单（type=TOOL / name / args 载荷）。
func (a *oaToolOffice) Post(name, argsJSON string, timeoutFrames int64) (int64, string) {
	if a.oa == nil {
		return 0, "ERR|OA_POST_FAIL|工单提交失败（OA 不可用）: " + name
	}
	id := a.oa.Post(toolOwnerID, "TOOL", name, timeoutFrames)
	if !a.oa.SetStr(id, toolOwnerID, "args", argsJSON) {
		return 0, "ERR|OA_POST_FAIL|工单提交失败（载荷写入被拒）: " + name
	}
	return id, ""
}

// State 读工单观测态。
func (a *oaToolOffice) State(officeID int64) session.ToolOfficeState {
	state := session.ToolOfficeState{}
	if a.oa == nil {
		return state
	}
	office := a.oa.GetOffice(officeID)
	state.ClaimFrame = office.ClaimFrame
	switch office.Status {
	case runtime.OfficeOpen:
		state.Status = 0
	case runtime.OfficeWork:
		state.Status = 1
	case runtime.OfficeClosed:
		state.Status = 2
	case runtime.OfficeTimeout:
		state.Status = 3
	}
	if office.Status == runtime.OfficeClosed {
		if value, ok := office.Result["result"]; ok {
			state.Result = value
			state.HasResult = true
		}
	}
	return state
}

// oaRoundSummaryOffice 把运行时 OA 适配为会话侧轮末总结工单面（挂单方 #2——独立于工具工单）。
type oaRoundSummaryOffice struct {
	// oa 工单撮合器
	oa *runtime.OA
}

// Post 上架一单（type=SVC / name=LLMSimpleRequest）。
func (a *oaRoundSummaryOffice) Post(officeName string, timeoutFrames int64) int64 {
	if a.oa == nil {
		return 0
	}
	return a.oa.Post(session.RoundSummaryOwnerID, "SVC", officeName, timeoutFrames)
}

// SetStr 写工单载荷。
func (a *oaRoundSummaryOffice) SetStr(officeID int64, key, value string) bool {
	if a.oa == nil {
		return false
	}
	return a.oa.SetStr(officeID, session.RoundSummaryOwnerID, key, value)
}

// State 读工单观测态（0=Open 1=Work 2=Closed 3=TimeOut；result 键存在即带回——C# 同判据不按状态）。
func (a *oaRoundSummaryOffice) State(officeID int64) session.ToolOfficeState {
	state := session.ToolOfficeState{}
	if a.oa == nil {
		return state
	}
	office := a.oa.GetOffice(officeID)
	state.ClaimFrame = office.ClaimFrame
	switch office.Status {
	case runtime.OfficeOpen:
		state.Status = 0
	case runtime.OfficeWork:
		state.Status = 1
	case runtime.OfficeClosed:
		state.Status = 2
	case runtime.OfficeTimeout:
		state.Status = 3
	}
	if value, ok := office.Result["result"]; ok {
		state.Result = value
		state.HasResult = true
	}
	return state
}

// RemoveByOwner 回收本挂单方全部工单。
func (a *oaRoundSummaryOffice) RemoveByOwner() {
	if a.oa == nil {
		return
	}
	a.oa.RemoveByOwner(session.RoundSummaryOwnerID)
}

// toolRuntime 是机制面装配结果（OA / 帧序 Runner / 已注册组）。
type toolRuntime struct {
	// OA 工单撮合器
	OA *runtime.OA
	// Runner 帧序宿主
	Runner *runtime.Runner
	// DB 数据中台（快照 boxes 段数据源）
	DB *runtime.DataBox
	// Groups 已注册组（目录名升序）
	Groups []toolset.Group
	// Skipped 跳过组与原因（已出声）
	Skipped []string
	// FileService 文件工具服务（nil = 受控根不可用——工具积木仍注册，执行报根错误）
	FileService *fs.Service
	// WebFetch 下载服务（全局单一下载器——任务表落盘 + 通知泵）
	WebFetch *bricks.WebFetchService
	// Browser 浏览器服务（BrowserCat 六件 + web.search 浏览器两档）
	Browser *bricks.BrowserService
	// Ts ts 服务宿主（TsCat 五件——node stdio 服务，按需拉起）
	Ts *bricks.TsService
	// Proc 进程槽服务（ProcCat 四件——同步执行 / 脱手起进程 / 台账）
	Proc *bricks.ProcService
	// Audio 音频播放服务（AudioCat 两件——per-Key 串行 + 组间并发）
	Audio *bricks.AudioService
	// Cs cs 桥执行面（CsCat 十三件——首次调用按需拉起 MauCsBridge.exe）
	Cs *bricks.CsService
	// DBSvc 条目库服务（DbCat 五件 + DbAdmin 三件——库根取受控根 db）
	DBSvc *maudb.Service
	// LLMSimple 一次性 LLM 请求服务（LLMSimpleRequest 三条认领线——endpoint/apikey/model 由工单给）
	LLMSimple *bricks.LLMSimpleService
	// Py py 执行服务（PyCat 三件——短命脚本 + 常驻服务池；audio.speak 的合成面同源）
	Py *bricks.PyService
	// Mau Mau 工具链执行面（MauCat 四件——verify 原生 front / gen·proj 经 cs 桥 / setup SetUp.exe）
	Mau *bricks.MauService
}

// buildToolRuntime 装配机制面——DataBox / OA / Runner / 积木表 / 组注册。
//
// 失败面一律出声（组跳过 / 语料目录缺失 / 受控根为空），不静默降级。
func buildToolRuntime(workspace *config.WorkspaceConfig, bricksIndex, corpusDir, dataRoot string, webCfg bricks.WebConfig, vision bricks.VisionService, cfgGet func(key string) string, configSvc bricks.ConfigService, hostCmd func(string) string, logf func(level int, message, category string), llmStream bricks.LLMStreamRuntime) *toolRuntime {
	report := func(level int, message string) {
		if logf != nil {
			logf(level, message, "TOOL")
		}
	}
	result := &toolRuntime{}
	db := runtime.NewDataBox()
	result.DB = db
	result.OA = runtime.NewOA(nil)
	result.OA.Log = func(level int, message string) {
		if logf != nil {
			logf(level, message, "OA")
		}
	}
	result.Runner = runtime.NewRunner(db, result.OA, nil)
	result.OA.FrameProvider = result.Runner.Frame

	svc, svcErr := buildFileService(workspace)
	result.FileService = svc
	if svcErr != "" {
		report(2, "文件工具服务未就绪："+svcErr)
	}
	// 条目库服务——库根取受控根 db（A267 同口径：根缺失即不绑定；服务自身负责建目录）
	dbRoot := ""
	if workspace != nil {
		for _, root := range workspace.Roots {
			if strings.EqualFold(root.ID, "db") {
				dbRoot = root.Path
				break
			}
		}
	}
	if dbRoot != "" {
		dbSvc, dbErr := maudb.New(dbRoot)
		if dbErr != nil {
			report(2, "条目库服务未就绪："+dbErr.Error())
		} else {
			dbSvc.SetLogger(func(level int, message string) {
				report(level, message)
			})
			result.DBSvc = dbSvc
		}
	} else {
		report(2, "db 服务 | 未绑定：受控根 db 缺失")
	}
	// git 执行面：受控根取文件服务同一份表；相对路径基准取进程工作目录（对齐 C# Environment.CurrentDirectory）
	var gitRoots []fs.Root
	if svc != nil {
		gitRoots = svc.Roots
	}
	gitBase, _ := os.Getwd()
	gitSvc := bricks.NewGitService(gitRoots, gitBase)
	// 下载服务——全局单一下载器：任务表落 data 根（跨重启续传），产物落 workspace 的 tools/Downloads
	webFetch := bricks.NewWebFetchService()
	webFetch.SetLogger(func(level int, message string) {
		report(level, message)
	})
	downloadsDir := ""
	if svc != nil && svc.Base != "" {
		downloadsDir = filepath.Join(svc.Base, "tools", "Downloads")
	}
	webFetch.Configure(filepath.Join(dataRoot, "downloads.json"), downloadsDir, nil)
	webFetch.Start()
	result.WebFetch = webFetch
	// 浏览器服务——browser-* 六件 + web.search 浏览器两档；内核路径读配置 browser.kernel_path
	browserSvc := bricks.NewBrowserService(dataRoot, func() string {
		if cfgGet == nil {
			return ""
		}
		return cfgGet("browser.kernel_path")
	}, func(level int, message string) {
		report(level, message)
	})
	result.Browser = browserSvc
	// ts 服务宿主——TsCat 五件（ts.check / ts.list / ts.read / ts.findRef / ts.patch）经 node stdio 服务执行；
	// 解释器 / 脚本 / 参数 / 超时全读配置（node.node_path · node.service.ts.*），首次调用按需拉起。
	var tsRoots []fs.Root
	if svc != nil {
		tsRoots = svc.Roots
	}
	tsSvc := bricks.NewTsService(tsRoots, cfgGet, func(level int, message string) {
		report(level, message)
	})
	result.Ts = tsSvc
	// 进程槽服务——ProcCat 四件（proc.run / proc.start / proc.stop / proc.list）：
	// 可执行文件限可写受控根内；台账落 Data/runtime/procs.json（脱手进程不随宿主退出清理）
	var procRoots []fs.Root
	if svc != nil {
		procRoots = svc.Roots
	}
	procSvc := bricks.NewProcService(procRoots, filepath.Join(dataRoot, "runtime"), func(level int, message string) {
		report(level, message)
	})
	result.Proc = procSvc
	// 音频播放服务——AudioCat 两件（audio.play / audio.speak）：per-Key 串行 + 组间并发；
	// 音量读 audio.volume；根寻址展开走文件服务同一实现（失败原样，最终由存在性判定）
	expandPath := func(path string) string {
		if svc == nil {
			return ""
		}
		full, err := svc.Resolve(path, false)
		if err != nil {
			return ""
		}
		return full
	}
	audioSvc := bricks.NewAudioService(expandPath, func(key, def string) string {
		if cfgGet == nil {
			return def
		}
		if value := cfgGet(key); value != "" {
			return value
		}
		return def
	}, func(level int, message string) {
		report(level, message)
	})
	result.Audio = audioSvc
	// py 执行服务——PyCat 三件（py.run / py.list / py.service）：短命脚本起-跑-退 + 常驻服务池
	// （stdio 严格协议）；解释器读 py.python_path（服务级 py.service.<name>.python 可覆盖），
	// 脚本 / 扫描目录只认可写受控根。池归服务持有（宿主退出统一 Shutdown）。
	var pyRoots []fs.Root
	if svc != nil {
		pyRoots = svc.Roots
	}
	var cfgValues func() [][2]string
	if configSvc != nil {
		cfgValues = configSvc.Values
	}
	pySvc := bricks.NewPyService(pyRoots, cfgGet, cfgValues, func(level int, message string) {
		report(level, message)
	})
	result.Py = pySvc
	// audio.speak 合成面——py 服务（kokoro）同源注入；未配 py.service.kokoro.script 时按服务层 ERR 明示
	audioTTS := bricks.AudioTTSConfig{
		Voice: func() string {
			if cfgGet == nil {
				return ""
			}
			return cfgGet("audio.voice")
		},
		OutDir: func() string {
			if cfgGet == nil {
				return ""
			}
			return cfgGet("py.service.kokoro.out_dir")
		},
		Resolve: expandPath,
		// Py py 服务（kokoro 合成）——audio.speak 的合成面；nil 时按 PY_NO_SERVICE 明示
		Py: pySvc,
	}
	// cs 桥执行面——CsCat 十三件（csharp.bridge）：首次调用探 127.0.0.1:8130/health，
	// 不通则按候选路径拉起 MauCsBridge.exe（--root 逐根拼），成功缓存复用；退出关停自管进程
	var csRoots []fs.Root
	if svc != nil {
		csRoots = svc.Roots
	}
	csSvc := bricks.NewCsService(csRoots, func(level int, message string) {
		report(level, message)
	})
	result.Cs = csSvc
	// Mau 工具链执行面——MauCat 四件（mau.verify / mau.gen / mau.proj / mau.setup）：
	// verify 走 Go 原生 front（Lex→Parse→Validate→Analyze）；gen/proj 经 cs 桥（MauToolchainBridge
	// 进程内直调 MauGroupBuilder）；setup 起仓库根 SetUp.exe（900000 ms 看门狗）；仓库根取受控根 id=mau
	var mauRoots []fs.Root
	if svc != nil {
		mauRoots = svc.Roots
	}
	mauSvc := bricks.NewMauService(mauRoots, csSvc, func(level int, message string) {
		report(level, message)
	})
	result.Mau = mauSvc
	// 一次性 LLM 请求服务——LLMSimpleRequest 三条认领线（llm.simple_request / ready / read）：
	// endpoint/apikey/model 由工单参数给（不复用端点池）；自报头只发通道（tag=nil，C# 积木出口同口径）
	llmSvc := bricks.NewLLMSimpleService(db, func(request *http.Request, endpoint string) {
		llm.ApplySelfTag(request, endpoint, nil, "brick-simple")
	}, func(level int, message string) {
		if logf != nil {
			logf(level, message, "LLM")
		}
	})
	result.LLMSimple = llmSvc
	if cfgGet != nil && cfgGet("node.service.ts.follow") == "true" {
		if startErr := tsSvc.Start(); startErr != "" {
			report(2, "ts 服务自启失败："+startErr)
		} else {
			report(1, "ts 服务已随宿主拉起（node.service.ts.follow=true）")
		}
	}
	// py 服务自启——按 py.service.<name>.follow=true 拉起（服务清单自配置声明扫描；C# Autostart 同口径）
	if reply := pySvc.Autostart(); strings.HasPrefix(reply, "ERR|") {
		report(2, "py 服务自启失败："+reply)
	} else if reply != `{"started":[],"skipped":[],"errors":[]}` {
		report(1, "py 服务自启："+reply)
	}
	set := toolset.BuildBrickSet(svc, result.OA, db, toolset.BrickOptions{
		PwshPath: func() string {
			if cfgGet == nil {
				return ""
			}
			return cfgGet("ps.pwsh_path")
		},
		Git:      gitSvc,
		Web:      webCfg,
		WebFetch: webFetch,
		WebLogf: func(level int, message string) {
			report(level, message)
		},
		Browser:     browserSvc,
		Vision:      vision,
		Ts:          tsSvc,
		Proc:        procSvc,
		Audio:       audioSvc,
		AudioTTS:    audioTTS,
		Config:      configSvc,
		HostCommand: hostCmd,
		Log:         logf,
		Cs:          csSvc,
		DB:          result.DBSvc,
		LLM:         llmSvc,
		LLMStream:   llmStream,
		Py:          pySvc,
		Mau:         mauSvc,
	})

	if corpusDir == "" {
		report(2, "语料目录未定位（--corpus / CAT5_CORPUS / exe 目录 / workspace 的 mau 根均不可达）——工具组零注册")
		return result
	}
	groups, skipped := toolset.LoadGroups(corpusDir, bricksIndex, set, func(level int, message string) {
		report(level, message)
	})
	result.Groups = groups
	result.Skipped = skipped
	for _, group := range groups {
		result.Runner.RegisterFlow(group.Graph, group.Name)
	}
	return result
}

// buildFileService 从受控根表构造文件工具服务——Base = workspace 根，RecycleRoot 固定 tools/CatTemp/fs_recycle。
func buildFileService(workspace *config.WorkspaceConfig) (*fs.Service, string) {
	if workspace == nil || len(workspace.Roots) == 0 {
		return nil, "受控根表为空"
	}
	svc := &fs.Service{}
	for _, root := range workspace.Roots {
		svc.Roots = append(svc.Roots, fs.Root{ID: root.ID, Path: root.Path, Writable: root.Writable})
		if svc.Base == "" && strings.EqualFold(root.ID, "workspace") {
			svc.Base = root.Path
		}
	}
	if svc.Base == "" {
		svc.Base = workspace.Roots[0].Path
	}
	svc.RecycleRoot = filepath.Join(svc.Base, "tools", "CatTemp", "fs_recycle")
	return svc, ""
}

// snapshotCats 投影 Flow 注册表为快照 cats 段——desc 取 DataBox 的 self_desc（对齐 C# GetSelfDesc）。
func snapshotCats(runner *runtime.Runner, db *runtime.DataBox) []observe.Cat {
	if runner == nil {
		return []observe.Cat{}
	}
	entries := runner.Entries()
	cats := make([]observe.Cat, 0, len(entries))
	for _, entry := range entries {
		status := &observe.CatStatus{
			StateLines: append([]string{}, entry.Status.StateLines...),
			Sensors:    []observe.SensorValue{},
			Slots:      []observe.SlotValue{},
			Wires:      make([]observe.WireStatus, 0, len(entry.Status.Wires)),
		}
		for _, wire := range entry.Status.Wires {
			status.Wires = append(status.Wires, observe.WireStatus{
				Name:             wire.Name,
				Busy:             wire.Busy,
				LastTriggerFrame: wire.LastTrigger,
				TimedOut:         wire.TimedOut,
			})
		}
		cats = append(cats, observe.Cat{
			Name:        entry.Name,
			ID:          entry.ID,
			Type:        entry.TypeName,
			Kind:        entry.Kind,
			Faulted:     false,
			FaultReason: "",
			Status:      status,
			Desc:        describeSelf(db, entry.ID),
		})
	}
	return cats
}

// describeSelf 读实体自述盒（scope=Flow ID / key=self_desc；缺失给空数组——对齐 C# 空自述）。
func describeSelf(db *runtime.DataBox, flowID int64) []string {
	if db == nil {
		return []string{}
	}
	value, ok := db.Get(strconv.FormatInt(flowID, 10), "self_desc")
	if !ok {
		return []string{}
	}
	lines, ok := value.([]string)
	if !ok || lines == nil {
		return []string{}
	}
	return append([]string{}, lines...)
}

// snapshotBoxes 投影 DataBox 数据区为快照 boxes 段（对齐 C# BuildBoxEntry：t 标记 + 复杂值摘要）。
func snapshotBoxes(db *runtime.DataBox) []observe.Box {
	if db == nil {
		return []observe.Box{}
	}
	entries := db.Capture()
	boxes := make([]observe.Box, 0, len(entries))
	for _, entry := range entries {
		t, value := projectBoxValue(entry.Value)
		boxes = append(boxes, observe.Box{Scope: entry.Scope, Key: entry.Key, T: t, Value: value})
	}
	return boxes
}

// projectBoxValue 把盒子值投影为 (t 标记, 协议值)——C# BuildBoxEntry 同口径：
// bool→b / 整数浮点→n / 字符串→s（原样） / 其它→o + 类型摘要。
func projectBoxValue(value any) (string, any) {
	switch v := value.(type) {
	case nil:
		return "o", "null"
	case bool:
		return "b", v
	case string:
		return "s", v
	case int:
		return "n", int64(v)
	case int32:
		return "n", int64(v)
	case int64:
		return "n", v
	case float32:
		return "n", float64(v)
	case float64:
		return "n", v
	}
	name := reflect.TypeOf(value).String()
	if len(name) > 40 {
		name = name[:40] + "..."
	}
	return "o", "[" + name + "]"
}

// webConfigAdapter 把全局配置与 LLM 端点池适配为 web 类积木的配置面。
type webConfigAdapter struct {
	// store 配置存储
	store *config.Store
	// schema 配置声明（nil = 只读落盘值）
	schema *config.Schema
	// llm 端点池（nil = 池不可用）
	llm *config.LlmAPIStore
}

// Config 读配置有效值（file → env → default）。
func (a *webConfigAdapter) Config(key string) string {
	if a == nil || a.store == nil {
		return ""
	}
	return a.store.Resolve(a.schema, key).Value
}

// LLMConfig 取端点池配置与密钥。
func (a *webConfigAdapter) LLMConfig(id string) (string, string, string, bool) {
	if a == nil || a.llm == nil {
		return "", "", "", false
	}
	entry, ok := a.llm.TryGet(id)
	if !ok {
		return "", "", "", false
	}
	return entry.Endpoint, entry.DefaultModel, a.llm.Secret(id), true
}
