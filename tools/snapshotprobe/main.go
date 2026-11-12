package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Command snapshotprobe 快照摘要探针——取 GET /api/v1/snapshot 并打印段计数。
//
// 用途（起服回归验收）：一次调用拿到 version / pid / frame / cats / boxes / tools / sessions 计数，
// 与 L3 基线对照（cats 19 · boxes 17 · tools 110），不把 200 KB 快照正文搬进前文。

// snapshotSummary 是探针输出结构。
type snapshotSummary struct {
	// Version 协议版本
	Version any `json:"version"`
	// PID 宿主进程号
	PID any `json:"pid"`
	// Frame 当前帧号
	Frame any `json:"frame"`
	// Cats cats 段
	Cats []any `json:"cats"`
	// Boxes boxes 段
	Boxes []any `json:"boxes"`
	// Tools tools 段
	Tools []any `json:"tools"`
	// Sessions sessions 段
	Sessions []any `json:"sessions"`
	// Logs logs 段
	Logs []any `json:"logs"`
}

// main 探针入口。
func main() {
	url := flag.String("url", "http://127.0.0.1:8079/api/v1/snapshot", "snapshot 地址")
	flag.Parse()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "请求失败:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "状态码异常:", resp.StatusCode)
		os.Exit(1)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读响应失败:", err)
		os.Exit(1)
	}
	var summary snapshotSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		fmt.Fprintln(os.Stderr, "解析失败:", err)
		os.Exit(1)
	}
	fmt.Printf("snapshot | version=%v | pid=%v | frame=%v | cats=%d | boxes=%d | tools=%d | sessions=%d | logs=%d\n",
		summary.Version, summary.PID, summary.Frame,
		len(summary.Cats), len(summary.Boxes), len(summary.Tools), len(summary.Sessions), len(summary.Logs))
}
