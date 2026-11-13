// hostprobe 是 Majordomo 链路验证器——起 Majordomo 组 + OA 工单全链，或直调积木层；
// 另可拉快照打印 cats / boxes 概要（组注册面核对）。
//
// 用法（cwd = 工程根）：
//
//	bin\hostprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool majordomo-cmd -args {"cmd":"cat.list"}
//	bin\hostprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool majordomo-catinfo
//	bin\hostprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool group-info -args {"group":"g1","catId":"majordomo"}
//	bin\hostprobe.exe -corpus D:\Mau\Mau\corpus\ch4 -tool restart-host -args {"target":"D:\\Mau\\MauOut5"}
//	bin\hostprobe.exe -snapshot http://127.0.0.1:8079
//
// 与 tools/tempprobe 同定位：链路取证位点（可复跑）。宿主指令服务用探针替身（记录指令 + 固定回执）——
// 验证积木层与语料链；真实内核（commandRouter.Execute）由 cmd/cathome5 单测覆盖。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cathome5/mau/bricks"
	"cathome5/mau/runtime"
	"cathome5/mau/toolset"
)

// hostCommandStub 探针替身——记录最后一次指令并返回固定回执。
type hostCommandStub struct {
	mu   sync.Mutex
	last string
}

// Execute 实现宿主指令服务面（探针替身）。
func (s *hostCommandStub) Execute(line string) string {
	s.mu.Lock()
	s.last = line
	s.mu.Unlock()
	if line == "cat.info" {
		return "{\r\n  \"ok\": true,\r\n  \"tool\": \"cat.info\",\r\n  \"count\": 1\r\n}"
	}
	return "cat.list | 1 只猫（探针替身）"
}

// Last 取最近一次执行的指令。
func (s *hostCommandStub) Last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// groupProbeStub 是组服务探针替身——记录最后调用的方法与参数，按 A214 形态回执。
//
// 定位与 hostCommandStub 相同：只验「GroupCat 语料链 + 八件积木接线」；
// 真实权限 / 组数据语义由 cmd/cathome5 单测（hostGroupService）与宿主实机覆盖。
type groupProbeStub struct {
	mu     sync.Mutex
	method string
	args   string
}

// record 记录调用并回 A214 形态回执。
func (s *groupProbeStub) record(method, argsJSON string) string {
	s.mu.Lock()
	s.method = method
	s.args = argsJSON
	s.mu.Unlock()
	return `{"ok":true,"tool":"group-` + method + `","target":"probe","items":1}` + "\n" + "probe | " + method + " 已执行"
}

// Last 取最近一次调用的方法与参数。
func (s *groupProbeStub) Last() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.method, s.args
}

// Info 记录 group.info。
func (s *groupProbeStub) Info(argsJSON string) string { return s.record("info", argsJSON) }

// Inbox 记录 group.inbox。
func (s *groupProbeStub) Inbox(argsJSON string) string { return s.record("inbox", argsJSON) }

// Say 记录 group.say。
func (s *groupProbeStub) Say(argsJSON string) string { return s.record("post", argsJSON) }

// Send 记录 group.send。
func (s *groupProbeStub) Send(argsJSON string) string { return s.record("delegate", argsJSON) }

// NewSession 记录 group.new_session。
func (s *groupProbeStub) NewSession(argsJSON string) string { return s.record("newsession", argsJSON) }

// CatInfo 记录 group.catinfo。
func (s *groupProbeStub) CatInfo(argsJSON string) string { return s.record("catinfo", argsJSON) }

// CatCfgGet 记录 group.catcfg_get。
func (s *groupProbeStub) CatCfgGet(argsJSON string) string { return s.record("catcfg-get", argsJSON) }

// CatCfgSet 记录 group.catcfg_set。
func (s *groupProbeStub) CatCfgSet(argsJSON string) string { return s.record("catcfg-set", argsJSON) }

// main 执行一次链路验证。
func main() {
	corpusDir := flag.String("corpus", "", "语料根（corpus/ch4）")
	toolName := flag.String("tool", "majordomo-cmd", "工具名（restart-full / restart-incr / restart-host / majordomo-cmd / majordomo-catinfo / group-info / group-inbox / group-post / group-delegate / group-newsession / group-catinfo / group-catcfg-get / group-catcfg-set）")
	args := flag.String("args", "{}", "工具参数 JSON")
	viaFlow := flag.Bool("flow", true, "走 OA + Flow 全链（false = 直调积木层）")
	maxFrames := flag.Int("frames", 3000, "Flow 模式最大 tick 次数")
	snapshotURL := flag.String("snapshot", "", "若给出，先拉该地址的 /api/v1/snapshot 打印 cats / boxes 概要")
	flag.Parse()

	if *snapshotURL != "" {
		if err := printCats(*snapshotURL); err != nil {
			fmt.Println("snapshot 失败：" + err.Error())
		}
	}
	if *corpusDir == "" {
		return
	}
	stub := &hostCommandStub{}
	groupStub := &groupProbeStub{}

	if !*viaFlow {
		box := runtime.NewDataBox()
		bricks.SetInteractiveRunMode(box)
		reply, ok := executeDirect(box, stub, groupStub, *toolName, *args)
		fmt.Printf("direct ok=%v\n%s\n", ok, reply)
		printRestartBox(box)
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
	set := toolset.BuildBrickSet(nil, oa, box, toolset.BrickOptions{
		HostCommand: stub.Execute,
		Group:       groupStub,
	})
	groups, skipped := toolset.LoadGroups(*corpusDir, "", set, func(level int, message string) {
		fmt.Printf("[group %d] %s\n", level, message)
	})
	fmt.Printf("groups=%d skipped=%d\n", len(groups), len(skipped))
	// 目标组——group-* 走 GroupCat，其余走 Majordomo（探针一次只注册一条目标组）
	targetGroup := "Majordomo"
	if strings.HasPrefix(*toolName, "group-") {
		targetGroup = "GroupCat"
	}
	registered := false
	for _, group := range groups {
		if group.Name != targetGroup {
			continue
		}
		runner.RegisterFlow(group.Graph, group.Name)
		registered = true
	}
	if !registered {
		fmt.Println("目标组未注册：" + targetGroup)
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
			if last := stub.Last(); last != "" {
				fmt.Println("hostcommand.last=" + last)
			}
			printRestartBox(box)
			return
		case runtime.OfficeTimeout:
			fmt.Printf("flow ok=false ticks=%d（工单超时）\n%s\n", tick, valueText(office.Result["result"]))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println("flow 未在限定 tick 内完成")
}

// executeDirect 直调积木层——HOST 三件 + GROUP 八件（与语料消费同一实现）。
func executeDirect(box *runtime.DataBox, stub *hostCommandStub, groupStub *groupProbeStub, tool, argsJSON string) (string, bool) {
	switch tool {
	case "restart-full", "restart-incr", "restart-host":
		return bricks.HostRestart(box, argsJSON, strings.TrimPrefix(tool, "restart-"))
	case "majordomo-cmd":
		return bricks.HostCommand(stub.Execute, argsJSON)
	case "majordomo-catinfo":
		return bricks.HostCatInfo(stub.Execute, argsJSON)
	case "group-info":
		return bricks.GroupInfo(groupStub, argsJSON)
	case "group-inbox":
		return bricks.GroupInbox(groupStub, argsJSON)
	case "group-post":
		return bricks.GroupSay(groupStub, argsJSON)
	case "group-delegate":
		return bricks.GroupSend(groupStub, argsJSON)
	case "group-newsession":
		return bricks.GroupNewSession(groupStub, argsJSON)
	case "group-catinfo":
		return bricks.GroupCatInfo(groupStub, argsJSON)
	case "group-catcfg-get":
		return bricks.GroupCatCfgGet(groupStub, argsJSON)
	case "group-catcfg-set":
		return bricks.GroupCatCfgSet(groupStub, argsJSON)
	}
	return "ERR|BAD_ARGS|未知工具: " + tool, false
}

// printRestartBox 打印重启请求盒（有值才打）。
func printRestartBox(box *runtime.DataBox) {
	if payload := box.GetString("global", "host_restart_request"); payload != "" {
		fmt.Println("host_restart_request=" + payload)
	}
}

// valueText 工单结果值 → 文本。
func valueText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// printCats 拉快照并打印 cats / boxes 概要（组注册面核对）。
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
		Boxes []struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
		} `json:"boxes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return err
	}
	fmt.Printf("cats=%d boxes=%d\n", len(snapshot.Cats), len(snapshot.Boxes))
	for _, cat := range snapshot.Cats {
		fmt.Printf("  %-18s id=%-2d type=%-6s states=%-2d wires=%-2d desc=%v\n",
			cat.Name, cat.ID, cat.Type, len(cat.Status.StateLines), len(cat.Status.Wires), cat.Desc)
	}
	scopes := make([]string, 0, len(snapshot.Boxes))
	for _, box := range snapshot.Boxes {
		scopes = append(scopes, box.Scope+"/"+box.Key)
	}
	fmt.Printf("boxes: %s\n", strings.Join(scopes, ", "))
	return nil
}
