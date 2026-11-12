package bricks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// writeMauFixture 建临时仓库根（Mau.sln 占位 + Bricks/index.json + 语料）。
func writeMauFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Mau.sln"), []byte(""), 0o644); err != nil {
		t.Fatalf("写 Mau.sln 占位失败: %v", err)
	}
	indexDir := filepath.Join(root, "Bricks")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatalf("建 Bricks 目录失败: %v", err)
	}
	index := `{"bricks":[{"name":"self.desc","contract":{"inputs":[{"type":"string"},{"type":"string"},{"type":"string"}],"outputs":[{"type":"string"}]}}]}`
	if err := os.WriteFile(filepath.Join(indexDir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("写 index.json 失败: %v", err)
	}
	corpusDir := filepath.Join(root, "corpus")
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		t.Fatalf("建语料目录失败: %v", err)
	}
	good := "§ 'S_A' = { 'Idle', 'Done' }\n§ 'T_Init' : 'S_A' = 'Idle' → 'self.desc'[\"空闲\", \"\", \"\"] | 'S_A' = 'Done'\n"
	if err := os.WriteFile(filepath.Join(corpusDir, "a.mau"), []byte(good), 0o644); err != nil {
		t.Fatalf("写正例语料失败: %v", err)
	}
	bad := "§ 'S_A' = { 'Idle', 'Done' }\n§ 'T_Bad' : 'S_A' = 'Idle' → 'no.such'[\"x\"] | 'S_A' = 'Done'\n"
	if err := os.WriteFile(filepath.Join(corpusDir, "bad.mau"), []byte(bad), 0o644); err != nil {
		t.Fatalf("写负例语料失败: %v", err)
	}
	return root
}

// TestMauVerifyArgsGuard 参数面零容忍——未知键 / 缺必填。
func TestMauVerifyArgsGuard(t *testing.T) {
	svc := &MauService{root: t.TempDir()}
	if reply, ok := svc.Verify(`{"bad":1}`); ok || !strings.HasPrefix(reply, "ERR|BAD_ARGS|未知参数: bad") {
		t.Fatalf("未知键面不符: %q ok=%v", reply, ok)
	}
	if reply, ok := svc.Verify(`{}`); ok || !strings.Contains(reply, "缺参数 file") {
		t.Fatalf("必填面不符: %q ok=%v", reply, ok)
	}
}

// TestMauVerifyTempCorpus 原生 verify——正例逐字格式（头 + 摘要行 + 报告行）。
func TestMauVerifyTempCorpus(t *testing.T) {
	root := writeMauFixture(t)
	svc := NewMauService([]fs.Root{{ID: "mau", Path: root, Writable: true}}, nil, nil)
	reply, ok := svc.Verify(`{"file":"corpus/a.mau"}`)
	if !ok {
		t.Fatalf("正例未通过: %q", reply)
	}
	lines := strings.SplitN(reply, "\n", 2)
	if lines[0] != `{"ok":true,"tool":"mau-verify","target":"corpus/a.mau","items":1,"flow":"FL_A"}` {
		t.Fatalf("A214 头不符: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "corpus/a.mau | 验证通过 · 1 报告\r\n报告: ") {
		t.Fatalf("正文不符: %q", lines[1])
	}
}

// TestMauVerifyNegative 原生 verify——负例（E400 未知积木）失败格式。
func TestMauVerifyNegative(t *testing.T) {
	root := writeMauFixture(t)
	svc := NewMauService([]fs.Root{{ID: "mau", Path: root, Writable: true}}, nil, nil)
	reply, ok := svc.Verify(`{"file":"corpus/bad.mau"}`)
	if ok {
		t.Fatalf("负例不应通过: %q", reply)
	}
	lines := strings.SplitN(reply, "\n", 2)
	if lines[0] != `{"ok":false,"tool":"mau-verify","target":"corpus/bad.mau","flow":"FL_Bad","errors":1}` {
		t.Fatalf("失败头不符: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "corpus/bad.mau | 验证失败 · 1 错误\r\nFAIL|VALIDATE|corpus/bad.mau\r\ncorpus/bad.mau:2: E400: ") {
		t.Fatalf("失败正文不符: %q", lines[1])
	}
}

// TestMauVerifyPathEscape 越界路径面。
func TestMauVerifyPathEscape(t *testing.T) {
	root := writeMauFixture(t)
	svc := NewMauService([]fs.Root{{ID: "mau", Path: root, Writable: true}}, nil, nil)
	reply, ok := svc.Verify(`{"file":"../outside.mau"}`)
	if ok || !strings.HasPrefix(reply, "ERR|PATH_ESCAPE|路径越界（仅允许仓库根内）: ../outside.mau") {
		t.Fatalf("越界面不符: %q ok=%v", reply, ok)
	}
}

// TestRenderVerifyFormats 渲染面逐字断言（成功 / 失败两形——原生与 oracle 共用）。
func TestRenderVerifyFormats(t *testing.T) {
	success := renderVerify("x.mau", "X", []string{"r1"}, nil)
	wantSuccess := `{"ok":true,"tool":"mau-verify","target":"x.mau","items":1,"flow":"FL_X"}` + "\n" +
		"x.mau | 验证通过 · 1 报告\r\n报告: r1"
	if success != wantSuccess {
		t.Fatalf("成功形不符:\n got=%q\nwant=%q", success, wantSuccess)
	}
	failure := renderVerify("x.mau", "X", nil, []mauVerifyDiag{{Line: 3, Code: "E400", Msg: "未知积木"}})
	wantFailure := `{"ok":false,"tool":"mau-verify","target":"x.mau","flow":"FL_X","errors":1}` + "\n" +
		"x.mau | 验证失败 · 1 错误\r\nFAIL|VALIDATE|x.mau\r\nx.mau:3: E400: 未知积木"
	if failure != wantFailure {
		t.Fatalf("失败形不符:\n got=%q\nwant=%q", failure, wantFailure)
	}
}

// TestReadSetupReport 报告解析——Ok / Ts / Steps / Artifacts 与摘要顺序。
func TestReadSetupReport(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "setup_report.json")
	payload := map[string]any{
		"Ok": true,
		"Ts": "2026-11-12T12:00:00",
		"Steps": []any{
			map[string]any{"Name": "构建", "Ok": true, "Detail": "假件"},
			map[string]any{"Name": "组翻译", "Ok": true},
		},
		"Artifacts": []any{"a.dll", "b.dll"},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("报告序列化失败: %v", err)
	}
	if err := os.WriteFile(report, data, 0o644); err != nil {
		t.Fatalf("写报告失败: %v", err)
	}
	logPath := report + ".log"
	summary, stepCount, stepOk, artifactCount := readSetupReport(report, 0, logPath)
	if stepCount != 2 || stepOk != 2 || artifactCount != 2 {
		t.Fatalf("计数不符: steps=%d ok=%d artifacts=%d", stepCount, stepOk, artifactCount)
	}
	for _, want := range []string{
		"exit=0\r\n",
		"Ok=true | 报告时间 2026-11-12T12:00:00\r\n",
		"  构建 [OK] 假件\r\n",
		"  组翻译 [OK]\r\n",
		"步数: 2\r\n",
		"产物: 2 件（时间戳见报告 JSON）\r\n",
		"日志: " + logPath,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("摘要缺片段 %q:\n%s", want, summary)
		}
	}
}

// TestMauSetupNoRepo 仓库根缺失面。
func TestMauSetupNoRepo(t *testing.T) {
	svc := &MauService{}
	reply, ok := svc.Setup(`{"mode":"prepare"}`)
	if ok || !strings.HasPrefix(reply, "ERR|NO_REPO|") {
		t.Fatalf("NO_REPO 面不符: %q ok=%v", reply, ok)
	}
	reply, ok = svc.Setup(`{"mode":"bogus"}`)
	if ok || !strings.HasPrefix(reply, "ERR|BAD_ARGS|mode 非法值: bogus（prepare|sync-html）") {
		t.Fatalf("枚举面不符: %q ok=%v", reply, ok)
	}
}

// TestMauGenNoBridge 桥缺失面（gen / proj 的唯一运行期依赖）。
func TestMauGenNoBridge(t *testing.T) {
	svc := &MauService{root: t.TempDir()}
	reply, ok := svc.Gen(`{"proj":"x.mauproj"}`)
	if ok || !strings.HasPrefix(reply, "ERR|MAU_NO_BRIDGE|") {
		t.Fatalf("gen 桥缺失面不符: %q ok=%v", reply, ok)
	}
	reply, ok = svc.Proj(`{"proj":"x.mauproj","build":true}`)
	if ok || !strings.HasPrefix(reply, "ERR|MAU_NO_BRIDGE|") {
		t.Fatalf("proj 桥缺失面不符: %q ok=%v", reply, ok)
	}
}
