// tempprobe 是 TempToolCat 链路验证器——起 TempToolCat 组 + OA 工单全链，或直调积木层；
// 另可拉快照打印 cats 概要（组注册面核对）。
//
// 用法（cwd = 工程根）：
//
//	bin\tempprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool temp-info
//	bin\tempprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool temp-exec -args {"key":"probe.echo","content":"你好"}
//	bin\tempprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool temp-exec -args {"key":"nope","content":"x"}
//	bin\tempprobe.exe -snapshot http://127.0.0.1:8079
//
// 与 tools/pyprobe 同定位：链路复杂到单测覆盖不了时的一次实机取证位点（可复跑）。
// 生产注册表为空——探针默认注册 probe.echo（仅进程内），用于验证成功路径；-register "" 关闭。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cathome5/mau/bricks"
	"cathome5/mau/runtime"
	"cathome5/mau/toolset"
)

// main 执行一次链路验证。
func main() {
	corpusDir := flag.String("corpus", "", "语料根（corpus/ch4）")
	toolName := flag.String("tool", "temp-info", "工具名（temp-info / temp-exec）")
	args := flag.String("args", "{}", "工具参数 JSON")
	viaFlow := flag.Bool("flow", true, "走 OA + Flow 全链（false = 直调积木层）")
	maxFrames := flag.Int("frames", 3000, "Flow 模式最大 tick 次数")
	register := flag.String("register", "probe.echo", "探针注册的临时工具 Key（空 = 不注册；仅进程内生效）")
	snapshotURL := flag.String("snapshot", "", "若给出，先拉该地址的 /api/v1/snapshot 打印 cats 概要")
	flag.Parse()

	if *snapshotURL != "" {
		if err := printCats(*snapshotURL); err != nil {
			fmt.Println("snapshot 失败：" + err.Error())
		}
	}
	if *corpusDir == "" {
		return
	}
	// 探针进程内注册一个 echo handler——验证 temp.exec 成功路径（生产空表不变）
	if *register != "" {
		bricks.DefaultTempRegistry.Register(*register, func(content string) string {
			return "echo:" + content
		})
		fmt.Printf("已在探针进程内注册临时工具：%s\n", *register)
	}

	if !*viaFlow {
		reply, ok := executeDirect(*toolName, *args)
		fmt.Printf("direct ok=%v\n%s\n", ok, reply)
		return
	}

	// [段1] 机制面——OA / DataBox / Runner / 积木表 / 目标组注册
	box := runtime.NewDataBox()
	oa := runtime.NewOA(nil)
	oa.Log = func(level int, message string) {
		fmt.Printf("[oa %d] %s\n", level, message)
	}
	runner := runtime.NewRunner(box, oa, nil)
	oa.FrameProvider = runner.Frame
	set := toolset.BuildBrickSet(nil, oa, box, toolset.BrickOptions{})
	groups, skipped := toolset.LoadGroups(*corpusDir, "", set, func(level int, message string) {
		fmt.Printf("[group %d] %s\n", level, message)
	})
	fmt.Printf("groups=%d skipped=%d\n", len(groups), len(skipped))
	registered := false
	for _, group := range groups {
		if group.Name != "TempToolCat" {
			continue
		}
		runner.RegisterFlow(group.Graph, group.Name)
		registered = true
	}
	if !registered {
		fmt.Println("目标组未注册：TempToolCat")
		return
	}

	// [段2] 工单——Post → Tick → 认领执行 → complete_str
	officeID := oa.Post(1, "TOOL", *toolName, int64(*maxFrames))
	if officeID == 0 {
		fmt.Println("工单提交失败")
		return
	}
	oa.SetStr(officeID, 1, "args", *args)
	for tick := 0; tick < *maxFrames; tick++ {
		runner.Tick()
		office := oa.GetOffice(officeID)
		switch office.Status {
		case runtime.OfficeClosed:
			fmt.Printf("flow ok=true ticks=%d\n%s\n", tick, valueText(office.Result["result"]))
			return
		case runtime.OfficeTimeout:
			fmt.Printf("flow ok=false ticks=%d（工单超时）\n%s\n", tick, valueText(office.Result["result"]))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println("flow 未在限定 tick 内完成")
}

// executeDirect 直调积木层——temp 两件（与语料消费同一实现）。
func executeDirect(tool, argsJSON string) (string, bool) {
	switch tool {
	case "temp-info":
		return bricks.TempKeys(bricks.DefaultTempRegistry)
	case "temp-exec":
		return bricks.TempExec(bricks.DefaultTempRegistry, argsJSON)
	}
	return "ERR|BAD_ARGS|未知工具: " + tool, false
}

// valueText 工单结果值 → 文本。
func valueText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// printCats 拉快照并打印 cats 概要（组名 / 状态行数 / 导线数）。
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return err
	}
	fmt.Printf("cats=%d\n", len(snapshot.Cats))
	for _, cat := range snapshot.Cats {
		fmt.Printf("  %-18s id=%-2d type=%-6s states=%-2d wires=%-2d desc=%v\n",
			cat.Name, cat.ID, cat.Type, len(cat.Status.StateLines), len(cat.Status.Wires), cat.Desc)
	}
	return nil
}
