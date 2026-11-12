// llmprobe 是一次性 LLM 请求链路验证器——起 LLMSimpleService + LLMSimpleRequest 组 + SVC 工单全链，
// 打印回执文本；另可拉快照打印 cats 概要（组注册面核对）。
//
// 用法（cwd = 工程根）：
//
//	bin\llmprobe.exe -data-root <数据副本> -corpus <语料根>/ch4
//	bin\llmprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -endpoint http://127.0.0.1:18100/v1 -apikey sk-x -model high
//	bin\llmprobe.exe -snapshot http://127.0.0.1:8079
//
// 与 tools/csprobe · tools/dbprobe 同定位：链路复杂到单测覆盖不了时的一次实机取证位点（可复跑）。
// 与两者的差别：本组走 **SVC 服务工单**（officeType="SVC" / officeName="LLMSimpleRequest"），
// 参数是五个 str 键（endpoint / apikey / model / system / content），不是 TOOL 工单的 args 载荷。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"cathome5/host/config"
	"cathome5/host/llm"
	"cathome5/mau/bricks"
	"cathome5/mau/runtime"
	"cathome5/mau/toolset"
)

// previewLimit 是回执预览上限（UTF-16 码元）。
const previewLimit = 800

// officeOwnerID 是 SVC 工单挂单方（与工具工单同值：1）。
const officeOwnerID = int64(1)

// main 执行一次链路验证。
func main() {
	dataRoot := flag.String("data-root", "", "数据根（读 config/workspace.json + 端点池）")
	corpusDir := flag.String("corpus", "", "语料根（corpus/ch4）")
	endpoint := flag.String("endpoint", "", "LLM 端点（空 = 取数据根端点池默认条目）")
	apikey := flag.String("apikey", "", "LLM 密钥（空 = 取端点池默认条目的密钥）")
	model := flag.String("model", "", "模型名（空 = 取端点池默认条目的 defaultModel）")
	system := flag.String("system", "你是一个简洁的助手。", "系统提示词")
	content := flag.String("content", "只回复两个字：收到", "用户内容")
	apiID := flag.String("api", "", "端点池 apiConfigId（空 = 默认条目）")
	secretPath := flag.String("secret-path", "", "密钥文件覆盖（空 = <data-root>/secrets/llm-api.cfg；副本无密钥时指生产只读路径）")
	maxFrames := flag.Int("frames", 6000, "最大 tick 次数（默认 6000 × 10ms ≈ 60s）")
	snapshotURL := flag.String("snapshot", "", "若给出，先拉该地址的 /api/v1/snapshot 打印 cats 概要")
	flag.Parse()

	if *snapshotURL != "" {
		if err := printCats(*snapshotURL); err != nil {
			fmt.Println("snapshot 失败：" + err.Error())
		}
	}
	if *dataRoot == "" {
		return
	}
	resolvedEndpoint, resolvedKey, resolvedModel := *endpoint, *apikey, *model
	if resolvedEndpoint == "" || resolvedKey == "" || resolvedModel == "" {
		poolEndpoint, poolKey, poolModel := resolvePool(*dataRoot, *secretPath, *apiID)
		if resolvedEndpoint == "" {
			resolvedEndpoint = poolEndpoint
		}
		if resolvedKey == "" {
			resolvedKey = poolKey
		}
		if resolvedModel == "" {
			resolvedModel = poolModel
		}
	}
	if resolvedEndpoint == "" || resolvedKey == "" || resolvedModel == "" {
		fmt.Println("端点不全：endpoint / apikey / model 需显式传或端点池可解析")
		return
	}
	fmt.Printf("endpoint=%s model=%s keyLen=%d\n", resolvedEndpoint, resolvedModel, len(resolvedKey))

	// [段1] 机制面——OA / DataBox / Runner / 积木表（含一次性 LLM 服务）/ 目标组注册
	box := runtime.NewDataBox()
	oa := runtime.NewOA(nil)
	oa.Log = func(level int, message string) {
		fmt.Printf("[oa %d] %s\n", level, message)
	}
	runner := runtime.NewRunner(box, oa, nil)
	oa.FrameProvider = runner.Frame
	llmSvc := bricks.NewLLMSimpleService(box, func(request *http.Request, target string) {
		llm.ApplySelfTag(request, target, nil, "brick-simple")
	}, func(level int, message string) {
		fmt.Printf("[llm %d] %s\n", level, message)
	})
	set := toolset.BuildBrickSet(nil, oa, box, toolset.BrickOptions{LLM: llmSvc})
	groups, skipped := toolset.LoadGroups(*corpusDir, "", set, func(level int, message string) {
		fmt.Printf("[group %d] %s\n", level, message)
	})
	fmt.Printf("groups=%d skipped=%d\n", len(groups), len(skipped))
	if !registerGroup(runner, groups, "LLMSimpleRequest") {
		fmt.Println("目标组未注册：LLMSimpleRequest（看上方缺件日志）")
		return
	}

	// [段2] SVC 工单——Post → SetStr 五键 → Tick → 认领执行 → complete_str
	officeID := oa.Post(officeOwnerID, "SVC", "LLMSimpleRequest", int64(*maxFrames))
	if officeID == 0 {
		fmt.Println("工单提交失败")
		return
	}
	for key, value := range map[string]string{
		"endpoint": resolvedEndpoint,
		"apikey":   resolvedKey,
		"model":    resolvedModel,
		"system":   *system,
		"content":  *content,
	} {
		if !oa.SetStr(officeID, officeOwnerID, key, value) {
			fmt.Println("工单参数写入失败：" + key)
			return
		}
	}
	fmt.Printf("工单 #%d 已提交：SVC/LLMSimpleRequest\n", officeID)
	for tick := 0; tick < *maxFrames; tick++ {
		runner.Tick()
		office := oa.GetOffice(officeID)
		switch office.Status {
		case runtime.OfficeClosed:
			fmt.Printf("flow ok=true ticks=%d\n%s\n", tick, preview(valueText(office.Result["result"])))
			printBoxes(box, officeID)
			return
		case runtime.OfficeTimeout:
			fmt.Printf("flow ok=false ticks=%d（工单超时）\n%s\n", tick, preview(valueText(office.Result["result"])))
			printBoxes(box, officeID)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println("flow 未在限定 tick 内完成（可加大 -frames）")
	printBoxes(box, officeID)
}

// resolvePool 从数据根解析端点池默认（或指定 id）条目的 endpoint / key / model。
//
// secretPath 为空时取 <data-root>/secrets/llm-api.cfg；数据副本常不含密钥（拷贝时被排除），
// 实机取证可指生产只读路径（只读，不落副本）。
func resolvePool(dataRoot, secretPath, apiID string) (string, string, string) {
	if secretPath == "" {
		secretPath = filepath.Join(dataRoot, "secrets", "llm-api.cfg")
	}
	store := config.LoadLlmAPIStore(filepath.Join(dataRoot, "config", "llm-api.json"), secretPath)
	if err := store.LastError(); err != "" {
		fmt.Println("端点池读取告警：" + err)
	}
	configs := store.Configs()
	for _, item := range configs {
		if apiID != "" {
			if !strings.EqualFold(item.APIConfigID, apiID) {
				continue
			}
		} else if !item.IsDefault {
			continue
		}
		return item.Endpoint, store.Secret(item.APIConfigID), item.DefaultModel
	}
	return "", "", ""
}

// registerGroup 注册指定组，返回是否命中。
func registerGroup(runner *runtime.Runner, groups []toolset.Group, name string) bool {
	for _, group := range groups {
		if group.Name != name {
			continue
		}
		runner.RegisterFlow(group.Graph, group.Name)
		return true
	}
	return false
}

// printBoxes 打印本单的段盒（reply / done）——后台结算面直接取证。
func printBoxes(box *runtime.DataBox, officeID int64) {
	seg := itoa64(officeID)
	fmt.Printf("box reply=%q done=%q\n", box.GetString("global", "llm_simple_reply:"+seg), box.GetString("global", "llm_simple_done:"+seg))
}

// valueText 工单结果值 → 文本。
func valueText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// printCats 拉快照并打印 cats 概要（组名 / 状态机 / 导线数 / 自述）。
func printCats(baseURL string) error {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/snapshot?logs=0"
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var snapshot struct {
		Cats []struct {
			Name   string `json:"name"`
			ID     int64  `json:"id"`
			Type   string `json:"type"`
			Status struct {
				StateLines []string `json:"stateLines"`
				Wires      []struct {
					Name string `json:"name"`
				} `json:"wires"`
			} `json:"status"`
			Desc []string `json:"desc"`
		} `json:"cats"`
		Boxes []json.RawMessage `json:"boxes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return err
	}
	fmt.Printf("cats=%d boxes=%d\n", len(snapshot.Cats), len(snapshot.Boxes))
	for _, cat := range snapshot.Cats {
		fmt.Printf("  %-16s id=%-2d type=%-6s states=%-2d wires=%-2d desc=%v\n",
			cat.Name, cat.ID, cat.Type, len(cat.Status.StateLines), len(cat.Status.Wires), cat.Desc)
	}
	return nil
}

// preview 截断回执预览（UTF-16 码元口径）。
func preview(text string) string {
	trimmed := strings.TrimSpace(text)
	units := 0
	for _, r := range trimmed {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	if units <= previewLimit {
		return trimmed
	}
	count := 0
	for index, r := range trimmed {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if count+width > previewLimit {
			return trimmed[:index] + "…（共 " + itoa(units) + " 字）"
		}
		count += width
	}
	return trimmed
}

// itoa 十进制渲染。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// itoa64 十进制渲染（int64）。
func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	var buf [20]byte
	i := len(buf)
	for v != 0 {
		digit := v % 10
		if digit < 0 {
			digit = -digit
		}
		i--
		buf[i] = byte('0' + digit)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
