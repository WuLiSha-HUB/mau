package session

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SetInfoProvider 接线 info 内置工具的数据源（nil = 明示不可用）。
func (s *Session) SetInfoProvider(fn func() string) {
	r := s.roundState()
	r.mu.Lock()
	r.infoProvider = fn
	r.mu.Unlock()
}

// SetFlowRuntime 接线语料级热重载面（host-reload / host-flows 的数据源，nil = 明示不可用）。
//
// 会话层不依赖运行时实现（分层纪律）——真正的换图动作在组合根注入的闭包里。
func (s *Session) SetFlowRuntime(reload func(name, mode string) string, list func() string) {
	r := s.roundState()
	r.mu.Lock()
	r.flowReloader = reload
	r.flowLister = list
	r.mu.Unlock()
}

// ExecuteBuiltin 内置工具统一执行入口——会话内直执（Note / time / random / info / timeback）。
//
// 作用域锁定：Note / sleep / timer 在 timeback 作用域存活期一律拒绝（与区间删除语义冲突）。
func (s *Session) ExecuteBuiltin(name, argsJSON string) string {
	if gate := s.Timeback.CheckBuiltinTool(name); gate != "" {
		return gate
	}
	switch name {
	case "Note":
		return s.Note.Execute(argsJSON)
	case "time":
		nowText := time.Now().Format("2006-01-02 15:04:05")
		return metaHead("time", true, "", -1, []metaField{{"ts", nowText}}, "时间 | "+nowText)
	case "random":
		return s.executeRandom(argsJSON)
	case "pack":
		return s.executePack(argsJSON)
	case "sleep":
		return s.executeSleep(argsJSON)
	case "timer":
		return s.executeTimer(argsJSON)
	case "info":
		r := s.roundState()
		r.mu.Lock()
		provider := r.infoProvider
		r.mu.Unlock()
		if provider == nil {
			return "ERR|INFO_UNAVAILABLE|信息面未接线"
		}
		return provider()
	case "timeback-start":
		return s.Timeback.ExecuteStart(argsJSON)
	case "timeback-back":
		return s.Timeback.ExecuteBack(argsJSON)
	case "host-reload":
		return s.executeHostReload(argsJSON)
	case "host-flows":
		return s.executeHostFlows()
	}
	return "ERR|UNKNOWN_BUILTIN|未知内置工具: " + name
}

// executeHostReload host-reload 执行体——参数 {cat|name:"组名", mode:"fresh|carry|drain"}。
//
// 回执是热重载的唯一事实面：组 / gen / 语料哈希 / 规模 / 在途 / 耗时（失败带 ERR 码）。
func (s *Session) executeHostReload(argsJSON string) string {
	r := s.roundState()
	r.mu.Lock()
	reload := r.flowReloader
	r.mu.Unlock()
	if reload == nil {
		return "ERR|FLOW_RELOAD_UNAVAILABLE|语料热重载面未接线"
	}
	name := ""
	mode := ""
	if strings.HasPrefix(strings.TrimSpace(argsJSON), "{") {
		var root map[string]json.RawMessage
		if err := json.Unmarshal([]byte(argsJSON), &root); err != nil {
			return "ERR|BAD_ARGS|host-reload 参数损坏"
		}
		if raw, exists := root["cat"]; exists {
			_ = json.Unmarshal(raw, &name)
		}
		if name == "" {
			if raw, exists := root["name"]; exists {
				_ = json.Unmarshal(raw, &name)
			}
		}
		if raw, exists := root["mode"]; exists {
			_ = json.Unmarshal(raw, &mode)
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "ERR|BAD_ARGS|host-reload 缺 cat（组名——先用 host-flows 查清单）"
	}
	return reload(name, strings.TrimSpace(mode))
}

// executeHostFlows host-flows 执行体——注册表 + 语料代 + 在途 + pending。
func (s *Session) executeHostFlows() string {
	r := s.roundState()
	r.mu.Lock()
	list := r.flowLister
	r.mu.Unlock()
	if list == nil {
		return "ERR|FLOW_LIST_UNAVAILABLE|Flow 观测面未接线"
	}
	return list()
}

// executeRandom random 执行体——生成 [min, max) 随机整数。
func (s *Session) executeRandom(argsJSON string) string {
	min := 0
	max := 0
	hasMin := false
	hasMax := false
	if strings.HasPrefix(argsJSON, "{") {
		var root map[string]json.RawMessage
		if err := json.Unmarshal([]byte(argsJSON), &root); err != nil {
			return "ERR|BAD_ARGS|random 参数损坏"
		}
		if raw, exists := root["min"]; exists {
			var value int
			if err := json.Unmarshal(raw, &value); err == nil {
				min = value
				hasMin = true
			}
		}
		if raw, exists := root["max"]; exists {
			var value int
			if err := json.Unmarshal(raw, &value); err == nil {
				max = value
				hasMax = true
			}
		}
	}
	if !hasMin || !hasMax || min >= max {
		return "ERR|BAD_ARGS|random 需要 min < max 的整数参数"
	}
	picked := min + rand.Intn(max-min)
	return metaHead("random", true, "", -1, []metaField{
		{"min", min},
		{"max", max},
		{"value", picked},
	}, "随机 | "+itoa(min)+".."+itoa(max)+" = "+itoa(picked))
}

// 三件内置工具常量——sleep 同轮等待口径与 timer 排程上限（C# ChatSession.Builtin.cs 同值）。
const (
	// maxSleepWaitSeconds sleep 同轮等待上限（秒）——100ms/帧口径下 99 秒 = 990 帧；超过即属跨轮排程，走 timer
	maxSleepWaitSeconds = 99
	// maxTimerSeconds timer 时长上限（秒）——24 小时（排程语义允许长周期；与 sleep 的挂起防呆不同）
	maxTimerSeconds = 86400
	// framesPerSecond 每秒帧数——主循环节拍 100ms/帧
	framesPerSecond = 10
	// sleepSuspendMark sleep 挂起标记——返回它表示「工具单保持未闭合，由帧等待补齐」
	sleepSuspendMark = "SUSPEND|SLEEP"
	// toolBatchWaitFrames 工具批等待上限（帧）——1500 帧 = 150 秒（C# ToolBatchWaitFrames = 1200 + 300）
	toolBatchWaitFrames = int64(1500)
)

// executeSleep sleep 执行体——同轮等待（design-ch4-delay §5.3 语义按 C# 现行实现）：
// 本轮正常续跑（不结束轮次），工具单保持未闭合，由 pumpSleepWait 帧推进到点补结果。
//
// 参数面：hours/minutes/seconds（非负整数，合计 1..99 秒；未知参数拒绝——零容忍）。
func (s *Session) executeSleep(argsJSON string) string {
	var hours, minutes, seconds int64
	if strings.HasPrefix(argsJSON, "{") {
		fields, err := parseOrderedObject(argsJSON)
		if err != nil {
			return "ERR|BAD_ARGS|sleep 参数解析失败: " + err.Error()
		}
		for _, field := range fields {
			if field.Key == "catId" {
				continue
			}
			if field.Key != "hours" && field.Key != "minutes" && field.Key != "seconds" {
				return "ERR|BAD_ARGS|未知参数: " + field.Key + "（支持 hours / minutes / seconds）"
			}
			value, ok := jsonInt64(field.Value)
			if !ok {
				return "ERR|BAD_ARGS|" + field.Key + " 需为整数"
			}
			if value < 0 {
				return "ERR|BAD_ARGS|" + field.Key + " 不能为负"
			}
			switch field.Key {
			case "hours":
				hours = value
			case "minutes":
				minutes = value
			default:
				seconds = value
			}
		}
	}
	// 时长校验——合计 1..99 秒（超过 99 秒属跨轮排程语义，走 timer）
	total := hours*3600 + minutes*60 + seconds
	if total < 1 || total > maxSleepWaitSeconds {
		return "ERR|BAD_ARGS|sleep 时长需在 1.." + itoa(maxSleepWaitSeconds) + " 秒之间（当前 " + itoa64(total) + " 秒；更长等待请用 timer）"
	}
	r := s.roundState()
	r.mu.Lock()
	if r.sleepDueFrame >= 0 {
		remaining := r.sleepDueFrame
		r.mu.Unlock()
		return "ERR|BAD_ARGS|本批已有 sleep 在等待——同批只允许一个（剩余 " + itoa64(remaining) + " 帧）"
	}
	waitFrames := total * framesPerSecond
	r.sleepDueFrame = waitFrames
	r.sleepReply = metaHead("sleep", true, "", -1, []metaField{
		{"hours", hours},
		{"minutes", minutes},
		{"seconds", seconds},
		{"waitFrames", waitFrames},
	}, "等待 | "+itoa64(total)+" 秒（"+itoa64(waitFrames)+" 帧）——到点即回本批，本轮继续，不结束轮次。")
	r.mu.Unlock()
	return sleepSuspendMark
}

// executeTimer timer 执行体——登记延迟指令注入（design-ch4-delay §5.4）：
// 本轮正常继续（不挂起、不阻塞），到点由延迟队列泵注入登记内容。
//
// 参数面：content（必填，非空）+ hours/minutes/seconds（合计 1..86400 秒）+ loop（可缺省=false）。
func (s *Session) executeTimer(argsJSON string) string {
	content := ""
	var hours, minutes, seconds int64
	loop := false
	if strings.HasPrefix(argsJSON, "{") {
		fields, err := parseOrderedObject(argsJSON)
		if err != nil {
			return "ERR|BAD_ARGS|timer 参数解析失败: " + err.Error()
		}
		for _, field := range fields {
			if field.Key == "catId" {
				continue
			}
			if field.Key == "content" {
				var text string
				if err := json.Unmarshal(field.Value, &text); err != nil {
					return "ERR|BAD_ARGS|content 需为字符串"
				}
				content = text
				continue
			}
			if field.Key == "loop" {
				var flag bool
				if err := json.Unmarshal(field.Value, &flag); err != nil {
					return "ERR|BAD_ARGS|loop 需为布尔"
				}
				loop = flag
				continue
			}
			if field.Key != "hours" && field.Key != "minutes" && field.Key != "seconds" {
				return "ERR|BAD_ARGS|未知参数: " + field.Key + "（支持 content / hours / minutes / seconds / loop）"
			}
			value, ok := jsonInt64(field.Value)
			if !ok {
				return "ERR|BAD_ARGS|" + field.Key + " 需为整数"
			}
			if value < 0 {
				return "ERR|BAD_ARGS|" + field.Key + " 不能为负"
			}
			switch field.Key {
			case "hours":
				hours = value
			case "minutes":
				minutes = value
			default:
				seconds = value
			}
		}
	}
	if strings.TrimSpace(content) == "" {
		return "ERR|BAD_ARGS|timer 内容为空（content 必填——到点注入的指令文本）"
	}
	total := hours*3600 + minutes*60 + seconds
	if total < DelayMinSleepSeconds || total > maxTimerSeconds {
		return "ERR|BAD_ARGS|timer 时长需在 " + itoa(DelayMinSleepSeconds) + ".." + itoa(maxTimerSeconds) + " 秒之间（当前 " + itoa64(total) + " 秒）"
	}
	if s.Delays == nil {
		return "ERR|DELAY_NOT_WIRED|延迟队列未接线（宿主装配缺失）"
	}
	dueAt := s.Delays.Now() + total*1000
	added := s.Delays.Add(s.CatKey, content, SysKindTimer, dueAt, total*1000, loop)
	if strings.HasPrefix(added, "ERR|") {
		return added
	}
	loopText := "单次"
	if loop {
		loopText = "循环（每 " + itoa64(total) + " 秒）"
	}
	body := "排程 | " + FormatDelayTime(dueAt) + "（" + itoa64(total) + " 秒后 · " + loopText + "）——到点自动注入本指令，本轮正常继续。"
	return metaHead("timer", true, "", -1, []metaField{
		{"hours", hours},
		{"minutes", minutes},
		{"seconds", seconds},
		{"loop", loop},
		{"dueAt", dueAt},
	}, body)
}

// executePack pack 执行体——加载包注入：池定义与猫级挂载经数据面 provider 取，路径解析与前文注入同源。
//
// 语义：任一件失败整包不注入（半个包比没有包更危险——部分成功与完全成功不可区分）。
// 参数面：key 必填；未知参数拒绝（catId 宿主保留键放行）。
func (s *Session) executePack(argsJSON string) string {
	packKey := ""
	if strings.TrimSpace(argsJSON) != "" {
		var probe any
		if err := json.Unmarshal([]byte(argsJSON), &probe); err != nil {
			return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		if _, isObject := probe.(map[string]any); !isObject {
			return "ERR|BAD_ARGS|参数必须是 JSON 对象"
		}
		fields, err := parseOrderedObject(argsJSON)
		if err != nil {
			return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
		}
		for _, field := range fields {
			if field.Key == "catId" {
				continue
			}
			if field.Key != "key" {
				return "ERR|BAD_ARGS|未知参数: " + field.Key + "（支持 key）"
			}
			var text string
			if err := json.Unmarshal(field.Value, &text); err == nil {
				packKey = text
			}
		}
	}
	if packKey == "" {
		return "ERR|BAD_ARGS|缺少参数 key（pack 需要包 key）"
	}
	if s.packPayload == nil {
		return "ERR|PACK_NO_PROVIDER|加载包数据面未注入（宿主未接线）"
	}
	payload := s.packPayload(s.CatKey, packKey)
	var resolved struct {
		OK    bool     `json:"ok"`
		Key   string   `json:"key"`
		Desc  string   `json:"desc"`
		Error string   `json:"error"`
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(payload), &resolved); err != nil {
		return "ERR|PACK_BAD_PAYLOAD|包数据面返回解析失败: " + err.Error()
	}
	if !resolved.OK {
		if resolved.Error == "" {
			return "ERR|PACK_RESOLVE|包解析失败: " + packKey
		}
		return resolved.Error
	}
	if s.packResolver == nil || s.packReader == nil {
		return "ERR|PACK_NO_WORKSPACE|工作区配置不可用（受控根解析面缺失）"
	}
	labels := make([]string, 0, len(resolved.Paths))
	targets := make([]string, 0, len(resolved.Paths))
	failures := make([]string, 0, 2)
	for _, entryPath := range resolved.Paths {
		absPath, err := s.packResolver(entryPath)
		if err != nil {
			failures = append(failures, entryPath+" | 根解析失败: "+err.Error())
			continue
		}
		info, statErr := os.Stat(absPath)
		if statErr == nil && info.IsDir() {
			entries, readErr := os.ReadDir(absPath)
			if readErr != nil {
				failures = append(failures, entryPath+" | 目录读取失败: "+readErr.Error())
				continue
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				if entry.IsDir() || !hasMDSuffix(entry.Name()) {
					continue
				}
				names = append(names, entry.Name())
			}
			sort.SliceStable(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
			prefix := strings.TrimRight(entryPath, "/\\")
			for _, name := range names {
				labels = append(labels, prefix+"/"+name)
				targets = append(targets, filepath.Join(absPath, name))
			}
			if len(names) == 0 {
				failures = append(failures, entryPath+" | 目录内无 .md 文件")
			}
			continue
		}
		if statErr != nil {
			failures = append(failures, entryPath+" | 文件不存在")
			continue
		}
		labels = append(labels, entryPath)
		targets = append(targets, absPath)
	}
	contents := make([]string, 0, len(targets))
	totalChars := 0
	for i, target := range targets {
		text, err := s.packReader(target)
		if err != nil {
			failures = append(failures, labels[i]+" | 读取失败: "+err.Error())
			continue
		}
		contents = append(contents, text)
		totalChars += utf16Len(text)
	}
	if len(failures) > 0 {
		var b strings.Builder
		b.WriteString("ERR|PACK_LOAD_FAILED|")
		b.WriteString(packKey)
		b.WriteString(" 加载失败（整包未注入）:")
		for _, failure := range failures {
			b.WriteString("\n  - ")
			b.WriteString(failure)
		}
		return b.String()
	}
	var body strings.Builder
	body.WriteString("✅ PACK ")
	body.WriteString(packKey)
	if resolved.Desc != "" {
		body.WriteString(" | ")
		body.WriteString(resolved.Desc)
	}
	body.WriteString(" | ")
	body.WriteString(itoa(len(targets)))
	body.WriteString(" 件 / ")
	body.WriteString(itoa(totalChars))
	body.WriteString(" 字符\n")
	for i := range targets {
		body.WriteString("===== ")
		body.WriteString(labels[i])
		body.WriteString(" =====\n")
		body.WriteString(contents[i])
		body.WriteString("\n")
	}
	logAt(LogModuleHost, 1, "pack 注入: cat="+s.CatKey+" | key="+packKey+" | "+itoa(len(targets))+" 件 / "+itoa(totalChars)+" 字符", "INJECT")
	return metaHead("pack", true, packKey, len(targets), []metaField{{"chars", totalChars}},
		packKey+" | "+itoa(len(targets))+" 件 · "+itoa(totalChars)+" 字符"+"\n"+body.String())
}

// hasMDSuffix 判定 .md 后缀（大小写不敏感，对齐 Windows 通配 *.md）。
func hasMDSuffix(name string) bool {
	return len(name) >= 3 && strings.EqualFold(name[len(name)-3:], ".md")
}

// jsonInt64 取 JSON 整数值——非整数形态返回 false（对齐 C# JsonValueKind.Number + GetInt64 的拒绝面）。
func jsonInt64(raw json.RawMessage) (int64, bool) {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}
