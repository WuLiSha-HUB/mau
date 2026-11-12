// maucatprobe 是 MauCat 链路验证器——起 MauService（仓库根 / 积木索引 / cs 桥）+ MauCat 组 + OA 工单全链，
// 或直调四件积木；另有差分门（Go 原生 front vs 桥 verify_raw 逐字对拍）与快照 cats 概要（组注册面核对）。
//
// 用法（cwd = 工程根）：
//
//	bin\maucatprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group MauCat -tool mau-verify -args {"file":"mau:corpus/ch4/MauCat/mau_cat.mau"}
//	bin\maucatprobe.exe -data-root <数据副本> -corpus <语料根>/ch4 -group MauCat -tool mau-gen -args {"proj":"mau:..."}
//	bin\maucatprobe.exe -data-root <数据副本> -diff <语料根>/ch4
//	bin\maucatprobe.exe -mau-root <假根> -diff <假根>/corpus
//	bin\maucatprobe.exe -snapshot http://127.0.0.1:8079
//
// 与 tools/pyprobe 同定位：链路复杂到单测覆盖不了时的一次实机取证位点（可复跑）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	dataRoot := flag.String("data-root", "", "数据根（读 config/workspace.json 取受控根表）")
	corpusDir := flag.String("corpus", "", "语料根（corpus/ch4）")
	groupName := flag.String("group", "MauCat", "要注册的组名（MauCat）")
	toolName := flag.String("tool", "mau-verify", "工具名（mau-verify / mau-gen / mau-proj / mau-setup）")
	args := flag.String("args", "{}", "工具参数 JSON")
	viaFlow := flag.Bool("flow", true, "走 OA + Flow 全链（false = 直调积木层）")
	maxFrames := flag.Int("frames", 3000, "Flow 模式最大 tick 次数")
	snapshotURL := flag.String("snapshot", "", "若给出，先拉该地址的 /api/v1/snapshot 打印 cats 概要")
	mauRootOverride := flag.String("mau-root", "", "覆盖受控根 id=mau 的路径（假根夹具——verify/gen/proj/setup 的仓库根）")
	diffDir := flag.String("diff", "", "差分门：对目录内全部 .mau 逐字对拍 Go 原生 verify 与桥 verify_raw")
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
	roots := make([]fs.Root, 0, len(ws.Roots))
	for _, root := range ws.Roots {
		path := root.Path
		if *mauRootOverride != "" && strings.EqualFold(root.ID, "mau") {
			path = *mauRootOverride
		}
		roots = append(roots, fs.Root{ID: root.ID, Path: path, Writable: root.Writable})
	}
	if *mauRootOverride != "" {
		found := false
		for _, root := range roots {
			if strings.EqualFold(root.ID, "mau") {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, fs.Root{ID: "mau", Path: *mauRootOverride, Writable: true})
		}
	}
	logf := func(level int, message string) {
		fmt.Printf("[mau %d] %s\n", level, message)
	}
	csSvc := bricks.NewCsService(roots, logf)
	defer csSvc.Shutdown()
	mauSvc := bricks.NewMauService(roots, csSvc, logf)
	if *diffDir != "" {
		runVerifyDiff(mauSvc, *diffDir)
		return
	}

	if !*viaFlow {
		reply, ok := executeDirect(mauSvc, *toolName, *args)
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
	fsSvc := &fs.Service{Roots: roots}
	set := toolset.BuildBrickSet(fsSvc, oa, box, toolset.BrickOptions{Mau: mauSvc})
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

// executeDirect 直调积木层——mau-* 四件（与语料消费同一实现）。
func executeDirect(mauSvc *bricks.MauService, tool, argsJSON string) (string, bool) {
	switch tool {
	case "mau-verify":
		return bricks.MauVerify(mauSvc, argsJSON)
	case "mau-gen":
		return bricks.MauGen(mauSvc, argsJSON)
	case "mau-proj":
		return bricks.MauProj(mauSvc, argsJSON)
	case "mau-setup":
		return bricks.MauSetup(mauSvc, argsJSON)
	}
	return "ERR|BAD_ARGS|未知工具: " + tool, false
}

// runVerifyDiff 差分门——目录内全部 .mau 逐字对拍 Go 原生 verify 与桥 verify_raw。
//
// 判据：尾行 `DIFF 0 · FAIL 0`；DIFF 表示两侧回执不一致的文件数（逐字，含 A214 头与正文）。
func runVerifyDiff(mauSvc *bricks.MauService, dir string) {
	files := make([]string, 0, 64)
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".mau") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		fmt.Println("目录遍历失败：" + walkErr.Error())
	}
	sort.Strings(files)
	pass := 0
	fail := 0
	for _, path := range files {
		rel := filepath.ToSlash(path)
		argBytes, _ := json.Marshal(map[string]string{"file": "mau:" + relPathFromRoot(path, mauSvc.Root())})
		arg := string(argBytes)
		native, nativeOK := mauSvc.Verify(arg)
		oracle, oracleOK := mauSvc.VerifyOracle(arg)
		if native == oracle && nativeOK == oracleOK {
			pass++
			fmt.Printf("PASS %s\n", rel)
			continue
		}
		fail++
		fmt.Printf("DIFF %s（native ok=%v oracle ok=%v）\n", rel, nativeOK, oracleOK)
		printFirstDiff(native, oracle)
	}
	fmt.Printf("文件 %d · PASS %d · DIFF %d · FAIL 0\n", len(files), pass, fail)
	if fail > 0 {
		fmt.Printf("DIFF %d · FAIL 0（不一致 %d 件）\n", fail, fail)
	} else {
		fmt.Printf("DIFF 0 · FAIL 0\n")
	}
}

// relPathFromRoot 把绝对路径转成相对仓库根的斜杠路径。
func relPathFromRoot(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// printFirstDiff 打印首个差异位置与两侧上下文（定位用）。
func printFirstDiff(left, right string) {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	index := 0
	for index < limit && left[index] == right[index] {
		index++
	}
	start := index - 60
	if start < 0 {
		start = 0
	}
	leftEnd := index + 120
	if leftEnd > len(left) {
		leftEnd = len(left)
	}
	rightEnd := index + 120
	if rightEnd > len(right) {
		rightEnd = len(right)
	}
	fmt.Printf("  首个差异 @%d\n  native: %q\n  oracle: %q\n", index, left[start:leftEnd], right[start:rightEnd])
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
