package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveCsBridgeSource 桥源目录解析——显式覆盖优先，空取 <repo>/host/csbridge/bin/Debug/net10.0。
func TestResolveCsBridgeSource(t *testing.T) {
	r := &deployRun{o: deployOptions{Repo: `D:\Mau\WorkSpace\CatHome5`}}
	want := filepath.Join(`D:\Mau\WorkSpace\CatHome5`, "host", "csbridge", "bin", "Debug", "net10.0")
	if got := r.resolveCsBridgeSource(); got != want {
		t.Fatalf("默认源目录应为 %q，got %q", want, got)
	}
	override := `D:\somewhere\csbridge`
	r = &deployRun{o: deployOptions{Repo: `D:\Mau\WorkSpace\CatHome5`, CsBridge: override}}
	if got := r.resolveCsBridgeSource(); got != override {
		t.Fatalf("显式覆盖应优先，got %q", got)
	}
}

// TestCollectCsBridge 桥产物收集——缺失出声跳过（不阻断），存在整棵复制到暂存区 csbridge/。
func TestCollectCsBridge(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "bridge-bin")
	// 缺失：step 出声、返回 nil、不建目标目录、artifacts 不登记
	r := &deployRun{o: deployOptions{CsBridge: src}, artifacts: map[string]string{}}
	if err := r.collectCsBridge(staging); err != nil {
		t.Fatalf("缺失不应阻断：%v", err)
	}
	if len(r.steps) != 1 || r.steps[0].Name != "csbridge" || r.steps[0].OK {
		t.Fatalf("缺失应记一条 ok=false 的 csbridge 步骤：%+v", r.steps)
	}
	if _, err := os.Stat(filepath.Join(staging, "csbridge")); !os.IsNotExist(err) {
		t.Fatalf("缺失时不应创建目标目录")
	}
	if _, exists := r.artifacts["csbridge"]; exists {
		t.Fatalf("缺失时不应登记产物")
	}
	// 存在：整棵复制（含子目录）、step 通过、产物登记
	if err := os.MkdirAll(filepath.Join(src, "BuildHost-netcore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, csBridgeExeName), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "MauCsBridge.dll"), []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "BuildHost-netcore", "Microsoft.Build.dll"), []byte("mb"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &deployRun{o: deployOptions{CsBridge: src}, artifacts: map[string]string{}}
	if err := r.collectCsBridge(staging); err != nil {
		t.Fatalf("收集失败：%v", err)
	}
	if len(r.steps) != 1 || !r.steps[0].OK {
		t.Fatalf("存在应记一条 ok=true 的 csbridge 步骤：%+v", r.steps)
	}
	if got := r.artifacts["csbridge"]; got != filepath.Join(staging, "csbridge") {
		t.Fatalf("产物登记应为暂存区 csbridge/，got %q", got)
	}
	for _, name := range []string{
		csBridgeExeName,
		"MauCsBridge.dll",
		filepath.Join("BuildHost-netcore", "Microsoft.Build.dll"),
	} {
		if _, err := os.Stat(filepath.Join(staging, "csbridge", name)); err != nil {
			t.Fatalf("复制应含 %q：%v", name, err)
		}
	}
	// 重跑幂等（先清理再复制，不残留旧文件）
	if err := os.WriteFile(filepath.Join(staging, "csbridge", "stale.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &deployRun{o: deployOptions{CsBridge: src}, artifacts: map[string]string{}}
	if err := r.collectCsBridge(staging); err != nil {
		t.Fatalf("重跑失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "csbridge", "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("重跑应清理旧产物")
	}
}

// TestRelaunchSkipsAutostartWhenHostRunning 自启前检（CH4 1.8.266 同因）——运行区已有宿主实例：
// 跳过自启、不新增第二个实例（退出码 0，报告出「自启跳过」步骤）；无实例时才走起实例路径。
func TestRelaunchSkipsAutostartWhenHostRunning(t *testing.T) {
	prev := hostScanFindInDir
	t.Cleanup(func() { hostScanFindInDir = prev })
	hostScanFindInDir = func(dir string) ([]int, error) { return []int{4321, 4322}, nil }
	target := filepath.Join(t.TempDir(), "MauOut5")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version.txt"), []byte("0.35.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &deployRun{o: deployOptions{Target: target, Mode: "host"}, mode: "relaunch"}
	if code := r.relaunch(); code != deployExitOK {
		t.Fatalf("已有实例应视为服务已承载（退出码 0），got %d（steps=%+v）", code, r.steps)
	}
	skipSeen := false
	for _, step := range r.steps {
		if step.Name == "start" || step.Name == "probe" {
			t.Fatalf("跳过自启后不应再起实例 / 探活：%+v", r.steps)
		}
		if step.Name == "自启跳过（运行区已有实例 pid 4321,4322）" && step.OK {
			skipSeen = true
		}
	}
	if !skipSeen {
		t.Fatalf("报告缺「自启跳过」步骤：%+v", r.steps)
	}
	// 对照：无实例 → 运行区缺 exe → 起实例失败 → 需人工（证明上面是「跳过」而不是「碰巧没起」）
	hostScanFindInDir = func(dir string) ([]int, error) { return nil, nil }
	r = &deployRun{o: deployOptions{Target: target, Mode: "host"}, mode: "relaunch"}
	if code := r.relaunch(); code != deployExitManual {
		t.Fatalf("无实例且运行区缺 exe 应需人工，got %d", code)
	}
}
