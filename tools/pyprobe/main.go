// pyprobe 是 py 链路验证器——起 PyService（解释器 / 服务声明读数据副本配置）+ PyCat 组 + OA 工单全链，
// 或直调积木层；另可拉快照打印 cats 概要（组注册面核对）。
//
// 用法（cwd = 工程根）：
//
//	bin\pyprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group PyCat -tool py-run -args {"script":"mau:tools/x.py"}
//	bin\pyprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group PyCat -tool py-list -args {}
//	bin\pyprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group PyCat -tool py-service -args {"name":"probe","action":"start"}
//	bin\pyprobe.exe -snapshot http://127.0.0.1:8079
//
// 与 tools/dbprobe 同定位：链路复杂到单测覆盖不了时的一次实机取证位点（可复跑）。
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
	"cathome5/mau/bricks"
	"cathome5/mau/fs"
	"cathome5/mau/runtime"
	"cathome5/mau/toolset"
)

// previewLimit 是回执预览上限（UTF-16 码元）。
const previewLimit = 600

// main 执行一次链路验证。
func main() {
	dataRoot := flag.String("data-root", "", "数据根（读 config/workspace.json 取受控根表 + config/*.cfg 取 py 配置）")
	corpusDir := flag.String("corpus", "", "语料根（corpus/ch4）")
	groupName := flag.String("group", "PyCat", "要注册的组名（PyCat）")
	toolName := flag.String("tool", "py-run", "工具名（py-run / py-list / py-service）")
	args := flag.String("args", "{}", "工具参数 JSON")
	viaFlow := flag.Bool("flow", true, "走 OA + Flow 全链（false = 直调积木层）")
	maxFrames := flag.Int("frames", 3000, "Flow 模式最大 tick 次数")
	snapshotURL := flag.String("snapshot", "", "若给出，先拉该地址的 /api/v1/snapshot 打印 cats 概要")
	serviceName := flag.String("service", "probe", "py 服务名（-invoke 直调用）")
	invokePayload := flag.String("invoke", "", "服务调用载荷 JSON（给出即直调 PyService.Invoke，不跑工具链）")
	flag.Parse()

	if *snapshotURL != "" {
		if err := printCats(*snapshotURL); err != nil {
			fmt.Println("snapshot 失败：" + err.Error())
		}
	}
	if *dataRoot == "" {
		return
	}
	configDir := filepath.Join(*dataRoot, "config")
	ws := config.LoadWorkspace(filepath.Join(configDir, "workspace.json"), "")
	store := config.LoadStore(configDir)
	schema, schemaErr := config.LoadSchema(filepath.Join(configDir, "schema.json"))
	if schemaErr != nil {
		fmt.Println("schema 加载失败：" + schemaErr.Error())
		schema = &config.Schema{}
	}
	cfgGet := func(key string) string {
		return store.Resolve(schema, key).Value
	}
	roots := make([]fs.Root, 0, len(ws.Roots))
	for _, root := range ws.Roots {
		roots = append(roots, fs.Root{ID: root.ID, Path: root.Path, Writable: root.Writable})
	}
	pySvc := bricks.NewPyService(roots, cfgGet, store.All, func(level int, message string) {
		fmt.Printf("[py %d] %s\n", level, message)
	})
	defer pySvc.Shutdown()
	fmt.Printf("pythonPath=%q roots=%d\n", cfgGet("py.python_path"), len(roots))

	// 服务层直调（py-* 三件之外的面：Invoke 不在工具定义里，只由 audio.speak 内部消费）
	if *invokePayload != "" {
		fmt.Printf("invoke ok\n%s\n", preview(pySvc.Invoke(*serviceName, *invokePayload)))
		return
	}

	if !*viaFlow {
		reply, ok := executeDirect(pySvc, *toolName, *args)
		fmt.Printf("direct ok=%v\n%s\n", ok, preview(reply))
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
	// fs 服务必须注入——file.* / text.* 积木依赖它；传 nil 会让对应组整组跳过（组数少 2，与宿主实测不符）
	fsSvc := &fs.Service{Roots: roots}
	set := toolset.BuildBrickSet(fsSvc, oa, box, toolset.BrickOptions{Py: pySvc})
	groups, skipped := toolset.LoadGroups(*corpusDir, "", set, func(level int, message string) {
		fmt.Printf("[group %d] %s\n", level, message)
	})
	fmt.Printf("groups=%d skipped=%d\n", len(groups), len(skipped))
	registered := false
	for _, group := range groups {
		if group.Name != *groupName {
			continue
		}
		runner.RegisterFlow(group.Graph, group.Name)
		registered = true
	}
	if !registered {
		fmt.Println("目标组未注册：" + *groupName)
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
			fmt.Printf("flow ok=true ticks=%d\n%s\n", tick, preview(valueText(office.Result["result"])))
			return
		case runtime.OfficeTimeout:
			fmt.Printf("flow ok=false ticks=%d（工单超时）\n%s\n", tick, preview(valueText(office.Result["result"])))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println("flow 未在限定 tick 内完成")
}

// executeDirect 直调积木层——py-* 三件（与语料消费同一实现）。
func executeDirect(pySvc *bricks.PyService, tool, argsJSON string) (string, bool) {
	switch tool {
	case "py-run":
		return bricks.PyRun(pySvc, argsJSON)
	case "py-list":
		return bricks.PyList(pySvc, argsJSON)
	case "py-service":
		return bricks.PyServiceControl(pySvc, argsJSON)
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
		Boxes []struct {
			Scope string `json:"scope"`
			Key   string `json:"key"`
		} `json:"boxes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return err
	}
	fmt.Printf("cats=%d\n", len(snapshot.Cats))
	for _, cat := range snapshot.Cats {
		fmt.Printf("  %-18s id=%-2d type=%-6s states=%-2d wires=%-2d desc=%v\n",
			cat.Name, cat.ID, cat.Type, len(cat.Status.StateLines), len(cat.Status.Wires), cat.Desc)
	}
	fmt.Printf("boxes=%d\n", len(snapshot.Boxes))
	for _, box := range snapshot.Boxes {
		fmt.Printf("  box %s/%s\n", box.Scope, box.Key)
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
