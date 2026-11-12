package bricks

// MauService —— Mau 工具链执行面（MauCat 四条认领线消费：mau-verify / mau-gen / mau-proj / mau-setup）。
//
// 形态 A1（2026-11-12 拍板）：
//   - verify：Go 原生 front（Lex → Parse → Validate → Analyze），诊断 / 报告按 BRIK-MAU-001 逐字格式化；
//     差分门以桥的 verify_raw（C# MauCompilerV3 全链）为 oracle 做逐字对拍。
//   - gen / proj：经 cs 桥（MauToolchainBridge 进程内直调 MauProjFile + MauGroupBuilder）；桥只回
//     结构化数据（steps / diagnostics / error / target），A214 头与正文由本服务拼装（BRIK-MAU-002/003 格式）。
//   - setup：起仓库根 SetUp.exe 子进程（cwd=仓库根，900000 ms 看门狗 + 超时杀树）→ 读报告 JSON 提炼摘要。
//
// 字节保真（对齐 C# BRIK 出口）：头与正文之间是单个 "\n"；正文内部行是 CRLF（Environment.NewLine）；
// 头走 jsonString 口径（非 ASCII → \uXXXX）；超时 / 坏启动路径无 A214 头（纯正文）。
//
// 参数面零容忍（JsonArgs 口径）：未知键 / 缺必填 / 非法枚举一律 ERR|BAD_ARGS；服务层错误 ERR|MAU_*。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cathome5/mau/front"
	"cathome5/mau/fs"
)

// mauSetupWatchdogMs 是 SetUp 进程的内建看门狗（对齐 C# MauSetupBrick 的 WatchdogMs）。
const mauSetupWatchdogMs = 900000

// MauService 是 Mau 工具链的进程宿主——仓库根 / 积木索引 / cs 桥三件依赖。
type MauService struct {
	// roots 受控根表（verify / gen / proj 的受控根前缀解析面）
	roots []fs.Root
	// root 仓库根（受控根 id=mau 的绝对路径；空 = 未定位——verify / setup 报 NO_REPO）
	root string
	// index 积木索引（Bricks/index.json；nil = 未加载——校验按空表处理，所有积木引用报 E400）
	index *front.BrickTable
	// bridge cs 桥执行面（gen / proj 的 C# 库调用通道；nil = 报 MAU_NO_BRIDGE）
	bridge *CsService
	// logf 日志出口（level 0=debug 1=info 2=warn 3=err；nil = 不出声）
	logf func(level int, message string)
}

// NewMauService 构造 Mau 工具链宿主。
//
// 仓库根取受控根 id=mau；积木索引取 <根>/Bricks/index.json（加载失败出声并降级为空表）。
// bridge 为 cs 桥执行面（gen / proj 用；nil = 该两件报 MAU_NO_BRIDGE）。
func NewMauService(roots []fs.Root, bridge *CsService, logf func(level int, message string)) *MauService {
	svc := &MauService{roots: roots, bridge: bridge, logf: logf}
	for _, root := range roots {
		if strings.EqualFold(root.ID, "mau") && root.Path != "" {
			svc.root = root.Path
			break
		}
	}
	if svc.root == "" {
		svc.log(2, "Mau 工具链 | 未定位仓库根：受控根 id=mau 缺失——verify / gen / proj / setup 将报 NO_REPO")
		return svc
	}
	indexPath := filepath.Join(svc.root, "Bricks", "index.json")
	table, err := front.LoadBrickIndex(indexPath)
	if err != nil {
		svc.log(2, "Mau 工具链 | 积木索引不可读："+indexPath+"（"+err.Error()+"）——语义校验降级（积木引用将报 E400）")
		return svc
	}
	svc.index = table
	return svc
}

// Verify 执行 mau.verify——Go 原生 front 全链检查（零产出）。
//
// 成功：A214 头（flow）+ `<file> | 验证通过 · N 报告` + 每条 `报告: …`；
// 失败：A214 头（flow + errors）+ 失败摘要行 + `FAIL|VALIDATE|<file>` + 每条 `<file>:<line>: <code>: <msg>`。
func (s *MauService) Verify(argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "file", "file", "", ""); bad != "" {
		return bad, false
	}
	file := Get(argsJSON, "file")
	if file == "" || file == ParseFail {
		return "ERR|BAD_ARGS|缺少参数 file", false
	}
	if s == nil || s.root == "" {
		return "ERR|NO_REPO|未找到仓库根（Mau.sln 向上探测）", false
	}
	abs, pathErr := s.resolveRepoPath(file)
	if pathErr != "" {
		return pathErr, false
	}
	if _, err := os.Stat(abs); err != nil {
		return "ERR|NOT_FOUND|文件不存在: " + abs, false
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "ERR|" + mauErrorName(err) + "|" + err.Error(), false
	}
	source := string(data)
	flowName := mauFlowNameFromPath(abs)
	tokens, lexDiags := front.Lex(source)
	doc := front.Parse(tokens)
	doc.Diags = append(lexDiags, doc.Diags...)
	front.Validate(doc, s.index)
	var reports []string
	var diags []front.Diag
	if len(doc.Diags) > 0 {
		diags = doc.Diags
	} else {
		analysis := front.Analyze(doc)
		if len(analysis.Errors) > 0 {
			diags = analysis.Errors
		} else {
			reports = analysis.Reports
		}
	}
	if len(diags) == 0 {
		return renderVerify(file, flowName, reports, nil), true
	}
	curated := make([]mauVerifyDiag, 0, len(diags))
	for _, diag := range diags {
		curated = append(curated, mauVerifyDiag{Line: diag.Line, Code: diag.Code, Msg: diag.Msg})
	}
	return renderVerify(file, flowName, reports, curated), false
}

// mauVerifyDiag 是一条验证诊断（渲染面统一——原生与 oracle 同格式）。
type mauVerifyDiag struct {
	// Line 行号
	Line int
	// Code 诊断码（E0xx–E4xx）
	Code string
	// Msg 诊断消息
	Msg string
}

// renderVerify 按 BRIK-MAU-001 渲染验证回执——成功 / 失败两形。
//
// 成功：A214 头（flow）+ `<file> | 验证通过 · N 报告` + 每条 `报告: …`；
// 失败：A214 头（flow + errors）+ 失败摘要行 + `FAIL|VALIDATE|<file>` + 每条 `<file>:<line>: <code>: <msg>`。
func renderVerify(file, flowName string, reports []string, diags []mauVerifyDiag) string {
	if len(diags) == 0 {
		var body strings.Builder
		body.WriteString(file + " | 验证通过 · " + strconv.Itoa(len(reports)) + " 报告")
		for _, report := range reports {
			body.WriteString("\r\n")
			body.WriteString("报告: " + report)
		}
		head := MetaHeadFields("mau-verify", true, file, len(reports), [][2]any{{"flow", "FL_" + flowName}})
		return head + "\n" + body.String()
	}
	var body strings.Builder
	body.WriteString(file + " | 验证失败 · " + strconv.Itoa(len(diags)) + " 错误")
	body.WriteString("\r\n")
	body.WriteString("FAIL|VALIDATE|" + file)
	for _, diag := range diags {
		body.WriteString("\r\n")
		body.WriteString(file + ":" + strconv.Itoa(diag.Line) + ": " + diag.Code + ": " + diag.Msg)
	}
	head := MetaHeadFields("mau-verify", false, file, -1, [][2]any{{"flow", "FL_" + flowName}, {"errors", len(diags)}})
	return head + "\n" + body.String()
}

// Gen 执行 mau.gen——组翻译中间产物（不编译）。
func (s *MauService) Gen(argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "proj", "proj", "", ""); bad != "" {
		return bad, false
	}
	return s.runGroupBuild(argsJSON, false, "gen", "mau-gen")
}

// Proj 执行 mau.proj——组翻译 + 编译（build 参数决定是否 dotnet build）。
func (s *MauService) Proj(argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "proj build", "proj", "build", "true|false"); bad != "" {
		return bad, false
	}
	buildRaw := Get(argsJSON, "build")
	doBuild := buildRaw == "true" || buildRaw == "True" || buildRaw == "1"
	return s.runGroupBuild(argsJSON, doBuild, "proj", "mau-proj")
}

// Root 返回仓库根（受控根 id=mau 的绝对路径；空 = 未定位）——探针与差分门用。
func (s *MauService) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// VerifyRaw 直调桥的 verify_raw（差分门 oracle——不作工具暴露）。
func (s *MauService) VerifyRaw(argsJSON string) (string, bool) {
	if s == nil || s.bridge == nil {
		return "ERR|MAU_NO_BRIDGE|宿主未注入 cs 桥（verify_raw 需 MauCsBridge）", false
	}
	return s.bridge.Invoke("verify_raw", argsJSON)
}

// VerifyOracle 差分门 oracle——桥 verify_raw 的结构化结果按同一格式渲染（与 Verify 逐字比较）。
//
// 只作差分取证（不作工具暴露）：格式化路径与原生 Verify 完全同源，差异只来自诊断内容本身。
func (s *MauService) VerifyOracle(argsJSON string) (string, bool) {
	if s == nil || s.bridge == nil {
		return "ERR|MAU_NO_BRIDGE|宿主未注入 cs 桥（verify_raw 需 MauCsBridge）", false
	}
	file := Get(argsJSON, "file")
	if file == ParseFail {
		file = ""
	}
	raw, _ := s.bridge.Invoke("verify_raw", argsJSON)
	if strings.HasPrefix(raw, "ERR|") {
		return raw, false
	}
	var payload struct {
		OK          bool     `json:"ok"`
		Kind        string   `json:"kind"`
		Target      string   `json:"target"`
		Flow        string   `json:"flow"`
		Reports     []string `json:"reports"`
		Diagnostics []struct {
			Line    int    `json:"line"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"diagnostics"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "ERR|MAU_BRIDGE_IO|桥回执不可解析：" + csPreview(raw, 300), false
	}
	if payload.Kind != "" {
		switch payload.Kind {
		case "BAD_ARGS":
			return "ERR|BAD_ARGS|" + payload.Error, false
		case "NO_REPO":
			return "ERR|NO_REPO|" + payload.Error, false
		case "PATH_ESCAPE":
			return "ERR|PATH_ESCAPE|" + payload.Error, false
		case "NOT_FOUND":
			return "ERR|NOT_FOUND|" + payload.Error, false
		default:
			return "ERR|" + payload.Kind + "|" + payload.Error, false
		}
	}
	target := payload.Target
	if target == "" {
		target = file
	}
	curated := make([]mauVerifyDiag, 0, len(payload.Diagnostics))
	for _, diag := range payload.Diagnostics {
		curated = append(curated, mauVerifyDiag{Line: diag.Line, Code: diag.Code, Msg: diag.Message})
	}
	return renderVerify(target, strings.TrimPrefix(payload.Flow, "FL_"), payload.Reports, curated), payload.OK
}

// Setup 执行 mau.setup——SetUp.exe 子进程（prepare / sync-html）+ 报告 JSON 摘要。
//
// 超时 / 启动失败路径无 A214 头（纯正文，对齐 BRIK-MAU-004）。
func (s *MauService) Setup(argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "mode target report", "", "mode", "prepare|sync-html"); bad != "" {
		return bad, false
	}
	mode := Get(argsJSON, "mode")
	if mode == "" || mode == ParseFail {
		mode = "prepare"
	}
	if mode != "prepare" && mode != "sync-html" {
		return "ERR|BAD_ARGS|mode 仅支持 prepare/sync-html（当前: " + mode + "）", false
	}
	target := Get(argsJSON, "target")
	if target == ParseFail {
		target = ""
	}
	if s == nil || s.root == "" {
		return "ERR|NO_REPO|未找到仓库根（Mau.sln 向上探测/MAU_ROOT）——一键部署需仓库根内 SetUp.exe", false
	}
	exe := filepath.Join(s.root, "SetUp.exe")
	if _, err := os.Stat(exe); err != nil {
		return "ERR|NOT_FOUND|SetUp.exe 不存在: " + exe, false
	}
	report := Get(argsJSON, "report")
	if report == "" || report == ParseFail {
		report = filepath.Join(s.root, "CatTemp", "setup_report.json")
	}
	if dir := filepath.Dir(report); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	logPath := report + ".log"
	cmdArgs := []string{mode}
	if mode == "sync-html" && target != "" {
		cmdArgs = append(cmdArgs, "--target", target)
	}
	cmdArgs = append(cmdArgs, "--report", report)

	var body strings.Builder
	body.WriteString("SetUp " + mode + " 执行中（仓库根 " + s.root + "）")
	body.WriteString("\r\n")
	body.WriteString("报告: " + report)
	body.WriteString("\r\n")
	exitCode, failed := s.runSetup(exe, cmdArgs, &body)
	if failed {
		return body.String(), false
	}
	summary, stepCount, stepOk, artifactCount := readSetupReport(report, exitCode, logPath)
	body.WriteString(summary)
	targetLabel := mode
	if target != "" {
		targetLabel = target
	}
	fields := [][2]any{
		{"mode", mode},
		{"exit", exitCode},
		{"stepsOk", stepOk},
		{"artifacts", artifactCount},
	}
	summaryLine := targetLabel + " | " + mode + " · 步 " + strconv.Itoa(stepOk) + "/" + strconv.Itoa(stepCount) + " · 产物 " + strconv.Itoa(artifactCount) + " 件"
	head := MetaHeadFields("mau-setup", exitCode == 0, targetLabel, stepCount, fields)
	return head + "\n" + summaryLine + "\r\n" + body.String(), exitCode == 0
}

// runGroupBuild 共享执行面——走桥的 gen / proj，按 BRIK-MAU-002/003 格式化。
//
// 桥回结构化 JSON（ok / target / steps / diagnostics / error）；失败面 kind 分派为纯 ERR 文本
// （BAD_ARGS / NO_REPO / PATH_ESCAPE / NOT_FOUND / MAUPROJ / 异常类名）。
func (s *MauService) runGroupBuild(argsJSON string, doBuild bool, bridgeMethod, tool string) (string, bool) {
	projParam := Get(argsJSON, "proj")
	if projParam == "" || projParam == ParseFail {
		return "ERR|BAD_ARGS|缺少参数 proj", false
	}
	if s == nil || s.bridge == nil {
		return "ERR|MAU_NO_BRIDGE|宿主未注入 cs 桥（gen/proj 需 MauCsBridge）", false
	}
	raw, _ := s.bridge.Invoke(bridgeMethod, argsJSON)
	if strings.HasPrefix(raw, "ERR|") {
		return raw, false
	}
	var payload struct {
		OK          bool     `json:"ok"`
		Kind        string   `json:"kind"`
		Target      string   `json:"target"`
		Steps       []string `json:"steps"`
		Diagnostics []string `json:"diagnostics"`
		Error       string   `json:"error"`
		DLL         string   `json:"dll"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "ERR|MAU_BRIDGE_IO|桥回执不可解析：" + csPreview(raw, 300), false
	}
	if payload.Kind != "" {
		switch payload.Kind {
		case "BAD_ARGS":
			return "ERR|BAD_ARGS|" + payload.Error, false
		case "NO_REPO":
			return "ERR|NO_REPO|" + payload.Error, false
		case "PATH_ESCAPE":
			return "ERR|PATH_ESCAPE|" + payload.Error, false
		case "NOT_FOUND":
			return "ERR|NOT_FOUND|" + payload.Error, false
		case "MAUPROJ":
			return "FAIL|MAUPROJ|" + payload.Error, false
		default:
			return "ERR|" + payload.Kind + "|" + payload.Error, false
		}
	}
	var body strings.Builder
	body.WriteString(payload.Target + " | " + strconv.Itoa(len(payload.Steps)) + " 步")
	for _, step := range payload.Steps {
		body.WriteString("\r\n")
		body.WriteString(step)
	}
	fields := [][2]any{{"errors", len(payload.Diagnostics)}, {"build", doBuild}}
	if payload.OK {
		head := MetaHeadFields(tool, true, payload.Target, len(payload.Steps), fields)
		return head + "\n" + body.String(), true
	}
	for _, diag := range payload.Diagnostics {
		body.WriteString("\r\n")
		body.WriteString(diag)
	}
	if payload.Error != "" {
		body.WriteString("\r\n")
		body.WriteString("FAIL|GEN|" + payload.Error)
	}
	head := MetaHeadFields(tool, false, payload.Target, len(payload.Steps), fields)
	return head + "\n" + body.String(), false
}

// runSetup 起 SetUp 进程并阻塞等待——看门狗超时杀树。
//
// 返回 (退出码, 是否失败)；失败（启动失败 / 超时）时正文已写进 body，调用方直接回纯正文。
func (s *MauService) runSetup(exe string, args []string, body *strings.Builder) (int, bool) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = s.root
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		body.WriteString("FAIL|START|进程启动失败（Process.Start 返回空）")
		body.WriteString("\r\n")
		return 0, true
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		return exitCodeOf(cmd, waitErr), false
	case <-time.After(mauSetupWatchdogMs * time.Millisecond):
		killTree(cmd)
		<-done
		body.WriteString("FAIL|TIMEOUT|SetUp 超时（" + strconv.Itoa(mauSetupWatchdogMs) + " ms）已强杀")
		body.WriteString("\r\n")
		return 0, true
	}
}

// readSetupReport 读报告 JSON——提炼 Ok / 退出码 / 步明细 / 产物件数（读失败回落退出码结论）。
func readSetupReport(reportPath string, exitCode int, logPath string) (string, int, int, int) {
	stepCount := 0
	stepOk := 0
	artifactCount := 0
	var body strings.Builder
	body.WriteString("exit=" + strconv.Itoa(exitCode))
	body.WriteString("\r\n")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		body.WriteString("FAIL|NO_REPORT|报告未生成（进程异常退出？）——日志: " + logPath)
		body.WriteString("\r\n")
		return body.String(), stepCount, stepOk, artifactCount
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		body.WriteString("WARN|REPORT_PARSE|JsonException|" + err.Error())
		body.WriteString("\r\n")
		body.WriteString("日志: " + logPath)
		return body.String(), stepCount, stepOk, artifactCount
	}
	okText := "?"
	ts := ""
	if raw, exists := root["Ok"]; exists {
		var flag bool
		if json.Unmarshal(raw, &flag) == nil {
			okText = strconv.FormatBool(flag)
		}
	}
	if raw, exists := root["Ts"]; exists {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			ts = text
		}
	}
	body.WriteString("Ok=" + okText)
	if ts != "" {
		body.WriteString(" | 报告时间 " + ts)
	}
	body.WriteString("\r\n")
	if raw, exists := root["Steps"]; exists {
		var steps []json.RawMessage
		if json.Unmarshal(raw, &steps) == nil {
			for _, step := range steps {
				line := setupStepText(step)
				if line != "" {
					body.WriteString("  " + line)
					body.WriteString("\r\n")
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(step, &fields) == nil {
					if flagRaw, ok := fields["Ok"]; ok {
						var flag bool
						if json.Unmarshal(flagRaw, &flag) == nil && flag {
							stepOk++
						}
					}
				}
			}
			stepCount = len(steps)
			body.WriteString("步数: " + strconv.Itoa(stepCount))
			body.WriteString("\r\n")
		}
	}
	if raw, exists := root["Artifacts"]; exists {
		var artifacts []json.RawMessage
		if json.Unmarshal(raw, &artifacts) == nil {
			artifactCount = len(artifacts)
			body.WriteString("产物: " + strconv.Itoa(artifactCount) + " 件（时间戳见报告 JSON）")
			body.WriteString("\r\n")
		}
	}
	body.WriteString("日志: " + logPath)
	return body.String(), stepCount, stepOk, artifactCount
}

// setupStepText 步明细文本——字符串直接取；对象取 Name/name + [OK]/[FAIL] + Detail/detail。
func setupStepText(step json.RawMessage) string {
	var text string
	if json.Unmarshal(step, &text) == nil {
		return text
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(step, &fields) != nil {
		return string(step)
	}
	var sb strings.Builder
	for _, key := range []string{"Name", "name"} {
		if raw, exists := fields[key]; exists {
			var value string
			if json.Unmarshal(raw, &value) == nil && value != "" {
				sb.WriteString(value)
				break
			}
		}
	}
	if raw, exists := fields["Ok"]; exists {
		var flag bool
		if json.Unmarshal(raw, &flag) == nil {
			if flag {
				sb.WriteString(" [OK]")
			} else {
				sb.WriteString(" [FAIL]")
			}
		}
	}
	for _, key := range []string{"Detail", "detail"} {
		if raw, exists := fields[key]; exists {
			var value string
			if json.Unmarshal(raw, &value) == nil && value != "" {
				sb.WriteString(" " + value)
				break
			}
		}
	}
	if sb.Len() == 0 {
		return string(step)
	}
	return sb.String()
}

// resolveRepoPath 路径解析——受控根前缀按统一寻址展开，无前缀按仓库根拼接；统一验证在仓库根内。
//
// 越界 / 非法一律回 ERR|PATH_ESCAPE|（对齐 C# ResolveRepoPath 的失败面）。
func (s *MauService) resolveRepoPath(relPath string) (string, string) {
	escape := "ERR|PATH_ESCAPE|路径越界（仅允许仓库根内）: " + relPath
	var full string
	if sep := strings.Index(relPath, ":"); sep > 0 {
		base, _ := os.Getwd()
		resolved, _, _, _, ok := fs.ResolveOwnedRoot(relPath, s.roots, base)
		if !ok {
			return "", escape
		}
		full = resolved
	} else {
		abs, err := filepath.Abs(filepath.Join(s.root, relPath))
		if err != nil {
			return "", escape
		}
		full = abs
	}
	if !mauInRoot(s.root, full) {
		return "", escape
	}
	return full, ""
}

// mauInRoot 归属判定——full 在 root 内（含 root 自身；Windows 大小写不敏感）。
func mauInRoot(root, full string) bool {
	cleanRoot := filepath.Clean(root)
	if strings.EqualFold(filepath.Clean(full), cleanRoot) {
		return true
	}
	prefix := cleanRoot
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	return strings.HasPrefix(strings.ToLower(full), strings.ToLower(prefix))
}

// mauFlowNameFromPath 从文件路径推导流程名——mau_cat.mau → MauCat（对齐 C# FlowNameFromPath）。
func mauFlowNameFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var sb strings.Builder
	for _, part := range strings.Split(base, "_") {
		if part == "" {
			continue
		}
		runes := []rune(part)
		sb.WriteString(strings.ToUpper(string(runes[0])))
		if len(runes) > 1 {
			sb.WriteString(string(runes[1:]))
		}
	}
	return sb.String()
}

// mauErrorName 取 .NET 异常类名口径——文件不存在归 FileNotFoundException，其余归 Exception。
func mauErrorName(err error) string {
	if err == nil {
		return "Exception"
	}
	if os.IsNotExist(err) {
		return "FileNotFoundException"
	}
	return "Exception"
}

// log 日志出口——nil 安全。
func (s *MauService) log(level int, message string) {
	if s != nil && s.logf != nil {
		s.logf(level, message)
	}
}
