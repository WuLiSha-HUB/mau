// fakesetup 是 SetUp.exe 的假件——写报告 JSON 并退出（mau.setup 实机夹具，不跑真实部署链）。
//
// 用法（作为假根下的 SetUp.exe 被 MauService 拉起）：
//
//	fakesetup.exe prepare --report <报告路径>
//	fakesetup.exe sync-html --target <运行区> --report <报告路径>
//
// 行为：解析 --report → 写报告 JSON（Ok / Ts / Steps / Artifacts）+ 同名 .log → 退出码 0。
package main

import (
	"encoding/json"
	"os"
)

// main 写报告并退出。
func main() {
	report := ""
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--report" && i+1 < len(os.Args) {
			report = os.Args[i+1]
			i++
			continue
		}
		if os.Args[i] == "--target" && i+1 < len(os.Args) {
			i++
		}
	}
	if report == "" {
		os.Exit(2)
	}
	payload := map[string]any{
		"Ok": true,
		"Ts": "2026-11-12T12:00:00",
		"Steps": []any{
			map[string]any{"Name": "构建", "Ok": true, "Detail": "假件"},
			map[string]any{"Name": "组翻译", "Ok": true},
		},
		"Artifacts": []any{"a.dll", "b.dll"},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(report, data, 0o644); err != nil {
		os.Exit(4)
	}
	_ = os.WriteFile(report+".log", []byte("fakesetup\n"), 0o644)
}
