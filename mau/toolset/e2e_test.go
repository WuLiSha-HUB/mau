package toolset

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cathome5/mau/fs"
	"cathome5/mau/runtime"
)

// TestEndToEndToolFlow 端到端——Post → 帧驱动（传感器 + 五导线）→ claim/exec/complete → 工单回执。
func TestEndToEndToolFlow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	content := "第一行\n第二行\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("写样本失败: %v", err)
	}
	svc := &fs.Service{Roots: []fs.Root{{ID: "workspace", Path: dir, Writable: true}}, Base: dir}
	oa := runtime.NewOA(nil)
	db := runtime.NewDataBox()
	runner := runtime.NewRunner(db, oa, nil)
	set := BuildBrickSet(svc, oa, db, BrickOptions{})

	// 语料组（临时目录）
	corpusDir := filepath.Join(dir, "corpus")
	writeGroup(t, corpusDir, "GoodCat", "good_cat.mau", goodCorpus)
	groups, skipped := LoadGroups(corpusDir, "", set, func(level int, message string) {
		t.Logf("L%d %s", level, message)
	})
	if len(groups) != 1 {
		t.Fatalf("组加载失败: %v", skipped)
	}
	runner.RegisterFlow(groups[0].Graph, groups[0].Name)

	// 上架工单（载荷 = 工具参数 JSON）
	officeID := oa.Post(1, "TOOL", "text-read", 1200)
	if !oa.SetStr(officeID, 1, "args", `{"path":"a.txt"}`) {
		t.Fatal("载荷写入失败")
	}

	// 帧驱动直到闭合（1200 帧上限内应远早于此）
	// par 导线走后台 goroutine、结果次帧回投，帧间隔须给调度留窗口——零间隔帧循环下
	// goroutine 无执行窗口（-cpu=1 必红，满载偶发未回投）。
	for i := 0; i < 600; i++ {
		runner.Tick()
		if office := oa.GetOffice(officeID); office.Status == runtime.OfficeClosed || office.Status == runtime.OfficeTimeout {
			break
		}
		time.Sleep(200 * time.Microsecond)
	}
	office := oa.GetOffice(officeID)
	if office.Status != runtime.OfficeClosed {
		t.Fatalf("工单未闭合: %+v", office)
	}
	if got := office.Result["result"]; got != content && !containsAll(got, "第一行", "第二行") {
		t.Fatalf("回执不符: %q", got)
	}
	if office.Worker <= 0 || office.ClaimFrame <= 0 {
		t.Fatalf("认领痕迹缺失: worker=%d claimFrame=%d", office.Worker, office.ClaimFrame)
	}
}

// TestEndToEndUnclaimedTimeout 未注册组——工单无人认领 → 短时限后超时（B1 同构）。
func TestEndToEndUnclaimedTimeout(t *testing.T) {
	oa := runtime.NewOA(nil)
	db := runtime.NewDataBox()
	runner := runtime.NewRunner(db, oa, nil)
	officeID := oa.Post(1, "TOOL", "text-read", 2)
	_ = oa.SetStr(officeID, 1, "args", `{}`)
	for i := 0; i < 10; i++ {
		runner.Tick()
	}
	office := oa.GetOffice(officeID)
	if office.Status != runtime.OfficeTimeout {
		t.Fatalf("无人认领应超时: %+v", office)
	}
}

// containsAll 判定文本含全部片段。
func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for i := 0; i+len(part) <= len(text); i++ {
			if text[i:i+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
