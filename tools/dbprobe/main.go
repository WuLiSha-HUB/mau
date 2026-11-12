// dbprobe 是条目库链路验证器——起 DbService（库根可取受控根 db 或临时目录）+ DbCat/DbAdmin 组 + OA 工单全链，
// 或直调服务层；另可拉快照打印 cats 概要（组注册面核对）。
//
// 用法（cwd = 工程根）：
//
//	bin\dbprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group DbCat -tool db-put -args {"lib":"probe","title":"标题","body":"正文"} -lib probe
//	bin\dbprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group DbAdmin -tool dbAdmin-libs -args {} -db-root <临时库根>
//	bin\dbprobe.exe -snapshot http://127.0.0.1:8079
//
// 与 tools/csprobe 同定位：链路复杂到单测覆盖不了时的一次实机取证位点（可复跑）。
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
	"cathome5/mau/db"
	"cathome5/mau/runtime"
	"cathome5/mau/toolset"
)

// previewLimit 是回执预览上限（UTF-16 码元）。
const previewLimit = 600

// main 执行一次链路验证。
func main() {
	dataRoot := flag.String("data-root", "", "数据根（读 config/workspace.json 取受控根表）")
	corpusDir := flag.String("corpus", "", "语料根（corpus/ch4）")
	groupName := flag.String("group", "DbCat", "要注册的组名（DbCat / DbAdmin）")
	toolName := flag.String("tool", "db-query", "工具名（db-put / db-query / dbAdmin-libs …）")
	args := flag.String("args", "{}", "工具参数 JSON")
	dbRootFlag := flag.String("db-root", "", "库根覆盖（空 = 取受控根 db；自测建议传临时目录）")
	libFlag := flag.String("lib", "", "若给出，先确保该库存在（已存在不报错）")
	catFlag := flag.String("cat", "", "若给出，登记该猫的库授权（majordomo = 全放）")
	viaFlow := flag.Bool("flow", true, "走 OA + Flow 全链（false = 直调服务层）")
	maxFrames := flag.Int("frames", 3000, "Flow 模式最大 tick 次数")
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
	ws := config.LoadWorkspace(filepath.Join(*dataRoot, "config", "workspace.json"), "")
	rootPath := ""
	for _, root := range ws.Roots {
		if strings.EqualFold(root.ID, "db") {
			rootPath = root.Path
			break
		}
	}
	if *dbRootFlag != "" {
		rootPath = *dbRootFlag
	}
	if rootPath == "" {
		fmt.Println("库根不可用：受控根 db 缺失且未传 -db-root")
		return
	}
	fmt.Printf("dbRoot=%s\n", rootPath)
	svc, err := db.New(rootPath)
	if err != nil {
		fmt.Println("建库服务失败：" + err.Error())
		return
	}
	svc.SetLogger(func(level int, message string) {
		fmt.Printf("[db %d] %s\n", level, message)
	})
	if *catFlag != "" {
		db.RegisterCat(*catFlag, strings.EqualFold(*catFlag, "majordomo"), nil)
		fmt.Printf("policy cat=%s allLibs=%v\n", *catFlag, strings.EqualFold(*catFlag, "majordomo"))
	}
	if *libFlag != "" {
		created := svc.Create(*libFlag, *catFlag)
		if !created.OK && created.Code != "DB_LIB_EXISTS" {
			fmt.Printf("建库失败：%s %s\n", created.Code, created.Message)
			return
		}
		fmt.Printf("lib=%s ready（%s）\n", *libFlag, created.Code+created.Message)
	}

	if !*viaFlow {
		direct := executeDirect(svc, *toolName, *args)
		fmt.Printf("direct ok=%v\n%s\n", direct.OK, preview(direct.ToolText(*toolName, "")))
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
	set := toolset.BuildBrickSet(nil, oa, box, toolset.BrickOptions{DB: svc})
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

// executeDirect 直调服务层——只覆盖 db-* / dbAdmin-* 八件（与积木同一实现）。
func executeDirect(svc *db.Service, tool, argsJSON string) db.Result {
	var raw map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &raw); err != nil {
		return db.Fail("BAD_ARGS", "参数 JSON 解析失败: "+err.Error())
	}
	lib, _ := raw["lib"].(string)
	title, _ := raw["title"].(string)
	body, _ := raw["body"].(string)
	key, _ := raw["key"].(string)
	mode, _ := raw["mode"].(string)
	confirm, _ := raw["confirm"].(string)
	cat, _ := raw["catId"].(string)
	switch tool {
	case "db-put":
		return svc.Put(lib, title, body, cat)
	case "db-query":
		return svc.Query(lib, stringField(raw, "keyword"), intField(raw, "limit"), intField(raw, "offset"), stringField(raw, "order"), cat)
	case "db-get":
		return svc.Get(lib, mode, key, cat)
	case "db-update":
		return svc.Update(lib, title, body, cat)
	case "db-delete":
		return svc.Delete(lib, title, cat)
	case "dbAdmin-libs":
		return svc.ListLibsDetail(cat)
	case "dbAdmin-create":
		return svc.Create(lib, cat)
	case "dbAdmin-drop":
		return svc.DropLib(lib, confirm, cat)
	}
	return db.Fail("BAD_ARGS", "未知工具: "+tool)
}

// stringField 取字符串字段。
func stringField(raw map[string]any, name string) string {
	value, _ := raw[name].(string)
	return value
}

// intField 取整数字段。
func intField(raw map[string]any, name string) int {
	value, ok := raw[name].(float64)
	if !ok {
		return 0
	}
	return int(value)
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return err
	}
	fmt.Printf("cats=%d\n", len(snapshot.Cats))
	for _, cat := range snapshot.Cats {
		fmt.Printf("  %-12s id=%-2d type=%-6s states=%-2d wires=%-2d desc=%v\n",
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
