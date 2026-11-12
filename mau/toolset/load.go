// Package toolset 把语料组（corpus/ch4/<组>/<组>.mau）装配成可驱动的运行时图：
// 工具积木映射（75 件）+ 公共五积木（oa.* / self.desc）+ 组加载门禁。
//
// 分层：bricks（工具执行体）← 本包（积木表 + 组装配）→ graph（图）/ runtime（帧序）。
// 门禁口径：组内引用的积木全部在本包积木表里才注册；缺件组逐条出声跳过（不静默）。
package toolset

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cathome5/mau/bricks"
	"cathome5/mau/db"
	"cathome5/mau/front"
	"cathome5/mau/fs"
	"cathome5/mau/graph"
	"cathome5/mau/runtime"
)

// Group 是一个已装配的语料组（目录名 + 图）。
type Group struct {
	// Name 组名（语料目录名，如 TextCat）
	Name string
	// Path 语料文件绝对路径
	Path string
	// Graph 运行时图（导线表序即文档序）
	Graph *graph.Graph
}

// BrickOptions 是组装配的可选依赖（配置类——nil 即按「未配置」口径回 ERR）。
type BrickOptions struct {
	// PwshPath 读配置 ps.pwsh_path（powershell7 线；nil = 未配置）
	PwshPath func() string
	// Git git 命令执行面（nil = 未注入——git 工具报 GIT_NO_SERVICE）
	Git *bricks.GitService
	// Web web 类积木的配置面（search.* / LLM 池；nil = 全部按未配置口径）
	Web bricks.WebConfig
	// WebFetch 下载服务（nil = web.fetch 报 DOWNLOAD_NO_SERVICE）
	WebFetch *bricks.WebFetchService
	// WebLogf web 类积木的日志出口（nil = 不出声）
	WebLogf func(level int, message string)
	// Browser 浏览器服务（nil = browser-* 报 BROWSER_NO_SERVICE）
	Browser *bricks.BrowserService
	// Vision 视觉识别服务（nil = vision.analyze 报 VISION_NO_SERVICE）
	Vision bricks.VisionService
	// Ts ts 服务执行面（nil = ts-* 报 TS_NO_SERVICE）
	Ts *bricks.TsService
	// Proc 进程槽服务执行面（nil = proc-* 报 PROC_NO_SERVICE）
	Proc *bricks.ProcService
	// Audio 音频播放服务（nil = audio-* 报 AUDIO_NO_SERVICE）
	Audio *bricks.AudioService
	// AudioTTS audio.speak 的依赖面（音色 / 产物目录 / py 服务；nil 字段按未配置口径）
	AudioTTS bricks.AudioTTSConfig
	// Config 配置面（config.bridge 的宿主依赖；nil = config-* 报 CONFIG_NO_STORE）
	Config bricks.ConfigService
	// HostCommand 宿主指令服务（catcfg.bridge 的执行底座；nil = 报 HOSTCMD_NO_SERVICE）
	HostCommand func(string) string
	// Cs cs 桥执行面（csharp.bridge 的宿主依赖；nil = 报 CSHARP_NO_BRIDGE）
	Cs *bricks.CsService
	// DB 条目库服务（db-* / dbAdmin-* 八件的执行面；nil = 报 DB_NO_SERVICE）
	DB *db.Service
	// LLM 一次性 LLM 请求服务（llm.simple_* 三件的执行面；nil = 报 LLM_NO_SERVICE）
	LLM *bricks.LLMSimpleService
	// Py py 执行服务（py-* 三件的执行面；nil = 报 PY_NO_SERVICE）
	Py *bricks.PyService
	// Mau Mau 工具链执行面（mau-* 四件的执行面；nil = 报 MAU_NO_SERVICE）
	Mau *bricks.MauService
}

// BuildBrickSet 构造积木表——75 件工具积木（text 7 / file 9 / ps 1 / git 6 / http 3 / web 4 / browser 6 / vision 2 / ts 5 / proc 4 / audio 2 / config 2 / cs 1 / db 8 / llm 3 / py 3 / mau 4 / temp 2 / host 3）+ 公共五积木。
//
// 工具积木名与语料一致（text.read / file.tree / ps.exec …），参数是语料求值后的字符串切片：
// 单动作积木取 args[0]（整包工具参数 JSON）；公共积木按各自签名取参。
func BuildBrickSet(svc *fs.Service, oa *runtime.OA, db *runtime.DataBox, opts BrickOptions) graph.BrickSet {
	// 运行模式标记——host.restart 的闸门数据源（本宿主恒常驻交互形态；无常驻主循环的形态不调用）
	bricks.SetInteractiveRunMode(db)
	set := graph.BrickSet{
		// ── OA 公共积木（语料每组必用）──
		"oa.is_open": func(args []string) (string, bool) {
			return bricks.OaIsOpen(oa, argAt(args, 0), argAt(args, 1))
		},
		"oa.claim_next_simple": func(args []string) (string, bool) {
			return bricks.OaClaimNextSimple(oa, runtime.CurrentFlowID, argAt(args, 0), argAt(args, 1))
		},
		"oa.get_str": func(args []string) (string, bool) {
			return bricks.OaGetStr(oa, argAt(args, 0), argAt(args, 1))
		},
		"oa.complete_str": func(args []string) (string, bool) {
			return bricks.OaCompleteStr(oa, runtime.CurrentFlowID, argAt(args, 0), argAt(args, 1))
		},
		"self.desc": func(args []string) (string, bool) {
			return bricks.SelfDesc(db, runtime.CurrentFlowID, args...)
		},
		// ── HOST（Majordomo 五条认领线——重启登记 / 管理指令 / 全猫统计）──
		// 特权面由工具定义积木组级 privileged 声明承担；host.restart 只落盒不执行（进程动作归宿主闸门）
		"host.restart": func(args []string) (string, bool) {
			return bricks.HostRestart(db, argAt(args, 0), argAt(args, 1))
		},
		"host.command": func(args []string) (string, bool) {
			return bricks.HostCommand(opts.HostCommand, argAt(args, 0))
		},
		"host.catinfo": func(args []string) (string, bool) {
			return bricks.HostCatInfo(opts.HostCommand, argAt(args, 0))
		},
		// ── TEMP（TempToolCat 两件——临时工具注册表生产空表；Key 分发 str→str，异常回收 ERR|TEMP_EXEC）──
		"temp.exec": func(args []string) (string, bool) {
			return bricks.TempExec(bricks.DefaultTempRegistry, argAt(args, 0))
		},
		"temp.keys": func(args []string) (string, bool) {
			return bricks.TempKeys(bricks.DefaultTempRegistry)
		},
		// ── Config（ConfigCat 六条认领线——配置自改 + 每猫配置读写）──
		"config.bridge": func(args []string) (string, bool) {
			return bricks.ConfigBridge(opts.Config, argAt(args, 0), argAt(args, 1))
		},
		"catcfg.bridge": func(args []string) (string, bool) {
			return bricks.CatCfgBridge(opts.HostCommand, argAt(args, 0), argAt(args, 1))
		},
		// ── CSHARP（CsCat 13 条认领线共用——method / args 原样透传桥，语义归服务层）──
		"csharp.bridge": func(args []string) (string, bool) {
			return bricks.CSharpBridge(opts.Cs, argAt(args, 0), argAt(args, 1))
		},
		// ── DB（DbCat 五件 + DbAdmin 三件——授权判定与落盘归服务层）──
		"db.put": func(args []string) (string, bool) {
			return bricks.DbPut(opts.DB, argAt(args, 0))
		},
		"db.query": func(args []string) (string, bool) {
			return bricks.DbQuery(opts.DB, argAt(args, 0))
		},
		"db.get": func(args []string) (string, bool) {
			return bricks.DbGet(opts.DB, argAt(args, 0))
		},
		"db.update": func(args []string) (string, bool) {
			return bricks.DbUpdate(opts.DB, argAt(args, 0))
		},
		"db.delete": func(args []string) (string, bool) {
			return bricks.DbDelete(opts.DB, argAt(args, 0))
		},
		"dbAdmin.libs": func(args []string) (string, bool) {
			return bricks.DbAdminLibs(opts.DB, argAt(args, 0))
		},
		"dbAdmin.create": func(args []string) (string, bool) {
			return bricks.DbAdminCreate(opts.DB, argAt(args, 0))
		},
		"dbAdmin.drop": func(args []string) (string, bool) {
			return bricks.DbAdminDrop(opts.DB, argAt(args, 0))
		},
		// ── LLM（LLMSimpleRequest 三条认领线——一次性调用：HTTP 在服务层后台，主线程零阻塞）──
		"llm.simple_request": func(args []string) (string, bool) {
			return bricks.LLMSimpleRequest(opts.LLM, argAt(args, 0), argAt(args, 1), argAt(args, 2), argAt(args, 3), argAt(args, 4), argAt(args, 5))
		},
		"llm.simple_ready": func(args []string) (string, bool) {
			return bricks.LLMSimpleReady(opts.LLM, argAt(args, 0))
		},
		"llm.simple_read": func(args []string) (string, bool) {
			return bricks.LLMSimpleRead(opts.LLM, argAt(args, 0))
		},
		// ── PowerShell（PsCat 两条解释器线共用——shell 由语料第二参给）──
		"ps.exec": func(args []string) (string, bool) {
			return bricks.PsExec(svc, opts.PwshPath, argAt(args, 0), argAt(args, 1))
		},
		// ── Git（GitCat 六条认领线共用——进程外 git.exe，路径边界在服务层）──
		"git.status": func(args []string) (string, bool) {
			return bricks.GitStatus(opts.Git, argAt(args, 0))
		},
		"git.log": func(args []string) (string, bool) {
			return bricks.GitLog(opts.Git, argAt(args, 0))
		},
		"git.diff": func(args []string) (string, bool) {
			return bricks.GitDiff(opts.Git, argAt(args, 0))
		},
		"git.show": func(args []string) (string, bool) {
			return bricks.GitShow(opts.Git, argAt(args, 0))
		},
		"git.commit": func(args []string) (string, bool) {
			return bricks.GitCommit(opts.Git, argAt(args, 0))
		},
		"git.push": func(args []string) (string, bool) {
			return bricks.GitPush(opts.Git, argAt(args, 0))
		},
		// ── Web（WebCat 七条认领线共用——HTTP 执行面 + 搜索 + 下载）──
		"http.get": func(args []string) (string, bool) {
			return bricks.HTTPGet(argAt(args, 0))
		},
		"http.post": func(args []string) (string, bool) {
			return bricks.HTTPPost(argAt(args, 0))
		},
		"http.probe": func(args []string) (string, bool) {
			return bricks.HTTPProbe(argAt(args, 0))
		},
		"web.ds.search": func(args []string) (string, bool) {
			return bricks.WebDSSearch(opts.Web, argAt(args, 0))
		},
		"web.fetch": func(args []string) (string, bool) {
			return bricks.WebFetch(opts.WebFetch, argAt(args, 0))
		},
		"web.fetch.jobs": func(args []string) (string, bool) {
			return bricks.WebFetchJobs(opts.WebFetch, argAt(args, 0))
		},
		// ── Browser（BrowserCat 六条认领线共用——本地浏览器 CDP 执行面）──
		"browser.open": func(args []string) (string, bool) {
			return bricks.BrowserOpen(opts.Browser, argAt(args, 0))
		},
		"browser.read": func(args []string) (string, bool) {
			return bricks.BrowserRead(opts.Browser, argAt(args, 0))
		},
		"browser.eval": func(args []string) (string, bool) {
			return bricks.BrowserEval(opts.Browser, argAt(args, 0))
		},
		"browser.shot": func(args []string) (string, bool) {
			return bricks.BrowserShot(opts.Browser, argAt(args, 0))
		},
		"browser.tabs": func(args []string) (string, bool) {
			return bricks.BrowserTabs(opts.Browser, argAt(args, 0))
		},
		"browser.headful": func(args []string) (string, bool) {
			return bricks.BrowserHeadful(opts.Browser, argAt(args, 0))
		},
		// ── Vision（VisionCat 两条认领线——识图 + 图片插入登记）──
		"vision.analyze": func(args []string) (string, bool) {
			return bricks.ImageAnalyze(opts.Vision, argAt(args, 0))
		},
		"image.inject": func(args []string) (string, bool) {
			return bricks.ImageInject(argAt(args, 0))
		},
		// ── TS（TsCat 五条认领线——node stdio 服务，进程与协议归服务层）──
		"ts.check": func(args []string) (string, bool) {
			return bricks.TsCheck(svc, opts.Ts, argAt(args, 0))
		},
		"ts.list": func(args []string) (string, bool) {
			return bricks.TsList(svc, opts.Ts, argAt(args, 0))
		},
		"ts.read": func(args []string) (string, bool) {
			return bricks.TsRead(svc, opts.Ts, argAt(args, 0))
		},
		"ts.findRef": func(args []string) (string, bool) {
			return bricks.TsFindRef(svc, opts.Ts, argAt(args, 0))
		},
		"ts.patch": func(args []string) (string, bool) {
			return bricks.TsPatch(svc, opts.Ts, argAt(args, 0))
		},
		// ── Proc（ProcCat 四条认领线——进程槽：参数面 / 路径边界 / 台账归服务层）──
		"proc.run": func(args []string) (string, bool) {
			return bricks.ProcRun(opts.Proc, argAt(args, 0))
		},
		"proc.start": func(args []string) (string, bool) {
			return bricks.ProcStart(opts.Proc, argAt(args, 0))
		},
		"proc.stop": func(args []string) (string, bool) {
			return bricks.ProcStop(opts.Proc, argAt(args, 0))
		},
		"proc.list": func(args []string) (string, bool) {
			return bricks.ProcList(opts.Proc, argAt(args, 0))
		},
		// ── Audio（AudioCat 两条认领线——播放服务 + py 合成；参数面与回执归积木层）──
		"audio.play": func(args []string) (string, bool) {
			return bricks.AudioPlay(opts.Audio, argAt(args, 0))
		},
		"audio.speak": func(args []string) (string, bool) {
			return bricks.AudioSpeak(opts.AudioTTS, opts.Audio, argAt(args, 0))
		},
		// ── PY（PyCat 三条认领线——短命脚本 / 脚本发现 / 常驻服务启停查；进程归服务层）──
		"py.run": func(args []string) (string, bool) {
			return bricks.PyRun(opts.Py, argAt(args, 0))
		},
		"py.list": func(args []string) (string, bool) {
			return bricks.PyList(opts.Py, argAt(args, 0))
		},
		"py.service": func(args []string) (string, bool) {
			return bricks.PyServiceControl(opts.Py, argAt(args, 0))
		},
		// ── MAU（MauCat 四条认领线——verify 走原生 front / gen·proj 经 cs 桥 / setup 起 SetUp.exe）──
		"mau.verify": func(args []string) (string, bool) {
			return bricks.MauVerify(opts.Mau, argAt(args, 0))
		},
		"mau.gen": func(args []string) (string, bool) {
			return bricks.MauGen(opts.Mau, argAt(args, 0))
		},
		"mau.proj": func(args []string) (string, bool) {
			return bricks.MauProj(opts.Mau, argAt(args, 0))
		},
		"mau.setup": func(args []string) (string, bool) {
			return bricks.MauSetup(opts.Mau, argAt(args, 0))
		},
	}
	// web.search 需要进程级节流状态——构造执行体后闭包捕获
	searchRunner := bricks.NewWebSearchRunner(opts.Web, opts.Browser, opts.WebLogf)
	set["web.search"] = func(args []string) (string, bool) {
		return searchRunner.WebSearch(argAt(args, 0))
	}
	if svc == nil {
		return set
	}
	// ── 工具积木（text 7 + file 9）——单参：整包工具参数 JSON ──
	tools := map[string]func(string) (string, bool){
		"text.read":         func(a string) (string, bool) { return bricks.TextRead(svc, a) },
		"text.write":        func(a string) (string, bool) { return bricks.TextWrite(svc, a) },
		"text.append":       func(a string) (string, bool) { return bricks.TextAppend(svc, a) },
		"text.replace":      func(a string) (string, bool) { return bricks.TextReplace(svc, a) },
		"text.read_lines":   func(a string) (string, bool) { return bricks.TextReadLines(svc, a) },
		"text.read_between": func(a string) (string, bool) { return bricks.TextReadBetween(svc, a) },
		"text.grep":         func(a string) (string, bool) { return bricks.TextGrep(svc, a) },
		"file.tree":         func(a string) (string, bool) { return bricks.FileTree(svc, a) },
		"file.find":         func(a string) (string, bool) { return bricks.FileFind(svc, a) },
		"file.move":         func(a string) (string, bool) { return bricks.FileMove(svc, a) },
		"file.delete":       func(a string) (string, bool) { return bricks.FileDelete(svc, a) },
		"file.copy":         func(a string) (string, bool) { return bricks.FileCopy(svc, a) },
		"file.version":      func(a string) (string, bool) { return bricks.FileVersion(svc, a) },
		"file.stat":         func(a string) (string, bool) { return bricks.FileStat(svc, a) },
		"file.hash":         func(a string) (string, bool) { return bricks.FileHash(svc, a) },
		"file.diff":         func(a string) (string, bool) { return bricks.FileDiff(svc, a) },
	}
	for name, fn := range tools {
		run := fn
		set[name] = func(args []string) (string, bool) {
			if len(args) < 1 {
				return "ERR|BAD_ARGS|缺参数 args（工具载荷缺失）", false
			}
			return run(args[0])
		}
	}
	return set
}

// LoadGroups 扫描语料目录并装配组——只注册「语法零诊断 + 引用积木全齐」的组。
//
// 返回值：groups 按目录名升序（注册顺序稳定）；skipped 是逐条跳过原因（已出声）。
// brickIndexPath 为空时跳过语义校验（只用运行时积木表做可执行门禁）。
func LoadGroups(corpusDir, brickIndexPath string, set graph.BrickSet, logf func(level int, message string)) (groups []Group, skipped []string) {
	report := func(level int, message string) {
		if logf != nil {
			logf(level, message)
		}
	}
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		report(2, "语料目录不可读："+corpusDir+"（"+err.Error()+"）")
		return nil, []string{corpusDir}
	}
	var table *front.BrickTable
	if brickIndexPath != "" {
		loaded, indexErr := front.LoadBrickIndex(brickIndexPath)
		if indexErr != nil {
			report(2, "积木索引不可读："+brickIndexPath+"（"+indexErr.Error()+"）——语义校验降级为运行时门禁")
		} else {
			table = loaded
		}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := findCorpusFile(filepath.Join(corpusDir, name))
		if path == "" {
			skipped = append(skipped, name+"（目录内无 .mau 语料）")
			report(1, "语料组跳过 "+name+"：目录内无 .mau 语料")
			continue
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			skipped = append(skipped, name+"（语料不可读）")
			report(2, "语料组跳过 "+name+"：读取失败 "+readErr.Error())
			continue
		}
		tokens, lexDiags := front.Lex(string(src))
		doc := front.Parse(tokens)
		doc.Diags = append(lexDiags, doc.Diags...)
		if table != nil {
			front.Validate(doc, table)
		}
		if len(doc.Diags) > 0 {
			diag := doc.Diags[0]
			skipped = append(skipped, name+"（诊断 "+diag.Code+" 行 "+itoa(diag.Line)+"）")
			report(2, "语料组跳过 "+name+"：诊断 "+diag.Code+" 行 "+itoa(diag.Line)+" "+diag.Msg)
			continue
		}
		if missing := missingBricks(doc, set); len(missing) > 0 {
			skipped = append(skipped, name+"（缺积木 "+strings.Join(missing, ",")+"）")
			report(1, "语料组跳过 "+name+"：缺积木 "+strings.Join(missing, ", "))
			continue
		}
		groups = append(groups, Group{Name: name, Path: path, Graph: graph.Build(doc, set)})
	}
	return groups, skipped
}

// findCorpusFile 取目录内的语料文件——优先进同名 snake_case，其次目录内唯一 .mau。
func findCorpusFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	candidates := make([]string, 0, 2)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".mau") {
			continue
		}
		candidates = append(candidates, filepath.Join(dir, entry.Name()))
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	sort.Strings(candidates)
	for _, path := range candidates {
		if strings.EqualFold(filepath.Base(path), snakeCase(filepath.Base(dir))+".mau") {
			return path
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// missingBricks 收集组内引用但积木表缺的积木名（去重 + 升序）。
func missingBricks(doc *front.Doc, set graph.BrickSet) []string {
	seen := map[string]bool{}
	check := func(name string) {
		if name == "" || set[name] != nil {
			return
		}
		seen[name] = true
	}
	for _, wire := range doc.Wires {
		for _, action := range wire.Actions {
			check(action.BrickName)
		}
	}
	for _, sensor := range doc.Sensors {
		check(sensor.BrickName)
		for _, action := range sensor.TrueActions {
			check(action.BrickName)
		}
		for _, action := range sensor.FalseActions {
			check(action.BrickName)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// snakeCase 目录名转蛇形（TextCat → text_cat；LLMSimpleRequest → llm_simple_request）。
func snakeCase(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			prevLower := i > 0 && runes[i-1] >= 'a' && runes[i-1] <= 'z'
			prevUpper := i > 0 && runes[i-1] >= 'A' && runes[i-1] <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if i > 0 && (prevLower || (prevUpper && nextLower)) {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// argAt 取第 n 个参数（越界为空串）。
func argAt(args []string, n int) string {
	if n < len(args) {
		return args[n]
	}
	return ""
}

// itoa 十进制渲染（本包专用，避免引入 strconv 之外的依赖面歧义）。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
