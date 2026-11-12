package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdCaptureTeeWritesFile 双写器落盘——outln 的输出同时进 stdout 与 runs/cmd_*.txt。
func TestCmdCaptureTeeWritesFile(t *testing.T) {
	dataRoot := t.TempDir()
	capture := enableCmdCapture(dataRoot)
	if capture == nil {
		t.Fatal("enableCmdCapture 返回 nil（落盘未启用）")
	}
	defer func() {
		consoleOut = os.Stdout
		capture.Close()
	}()
	outln("测试行一")
	outln("测试行二 中文内容")
	path := capture.Path()
	if path == "" {
		t.Fatal("Path 为空")
	}
	if !strings.HasPrefix(filepath.Base(path), "cmd_") || !strings.HasSuffix(path, ".txt") {
		t.Fatalf("落盘文件命名不符：%s", filepath.Base(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读落盘文件失败：%v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "测试行一") || !strings.Contains(text, "测试行二 中文内容") {
		t.Fatalf("落盘内容缺行：%q", text)
	}
}

// TestCmdCaptureCloseStopsWriting Close 后写入静默丢弃（不 panic、文件不再增长）。
func TestCmdCaptureCloseStopsWriting(t *testing.T) {
	dataRoot := t.TempDir()
	capture := enableCmdCapture(dataRoot)
	if capture == nil {
		t.Fatal("enableCmdCapture 返回 nil")
	}
	defer func() { consoleOut = os.Stdout }()
	outln("关闭前")
	path := capture.Path()
	capture.Close()
	outln("关闭后")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读落盘文件失败：%v", err)
	}
	if strings.Contains(string(raw), "关闭后") {
		t.Fatal("Close 后仍在写入")
	}
}
