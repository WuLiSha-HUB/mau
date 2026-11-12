package main

// report —— 逐条勾选报告（控制台 + report.txt + summary.json）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 检查状态常量。
const (
	stPass  = "PASS"
	stKnown = "KNOWN"
	stDiff  = "DIFF"
	stFail  = "FAIL"
	stInfo  = "INFO"
)

// check 是一条对拍检查。
type check struct {
	Class  string `json:"class"`
	Item   string `json:"item"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// report 汇总全部检查并负责落盘。
type report struct {
	mu     sync.Mutex
	checks []check
	outDir string
	meta   map[string]string
}

// newReport 造报告器。
func newReport(outDir string) *report {
	return &report{outDir: outDir, meta: map[string]string{}}
}

// metaSet 记一条头信息。
func (r *report) metaSet(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.meta[key] = value
}

// add 追加一条检查并即时打印。
func (r *report) add(class, item, status, note string) {
	c := check{Class: class, Item: item, Status: status, Note: note}
	r.mu.Lock()
	r.checks = append(r.checks, c)
	r.mu.Unlock()
	mark := status
	if note != "" {
		fmt.Printf("  [%s] %s | %s —— %s\n", mark, class, item, note)
	} else {
		fmt.Printf("  [%s] %s | %s\n", mark, class, item)
	}
}

// info 追加信息项（不计入判定）。
func (r *report) info(class, item, note string) { r.add(class, item, stInfo, note) }

// counts 汇总各状态数量。
func (r *report) counts() (pass, known, diff, fail, info int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.checks {
		switch c.Status {
		case stPass:
			pass++
		case stKnown:
			known++
		case stDiff:
			diff++
		case stFail:
			fail++
		default:
			info++
		}
	}
	return
}

// green 判定全绿（无 FAIL 且无非预期 DIFF）。
func (r *report) green() bool {
	_, _, diff, fail, _ := r.counts()
	return diff == 0 && fail == 0
}

// render 生成报告文本。
func (r *report) render() string {
	pass, known, diff, fail, info := r.counts()
	var b strings.Builder
	b.WriteString("== P3-a a4 实机对拍报告 ==\n")
	if len(r.meta) > 0 {
		keys := []string{"time", "data_root", "html_root", "cat5", "cs", "run_go", "run_cs"}
		for _, k := range keys {
			if v, ok := r.meta[k]; ok && v != "" {
				b.WriteString(fmt.Sprintf("%-10s %s\n", k+":", v))
			}
		}
	}
	b.WriteString("\n-- 逐条勾选 --\n")
	lastClass := ""
	r.mu.Lock()
	for _, c := range r.checks {
		if c.Class != lastClass {
			b.WriteString("\n[" + c.Class + "]\n")
			lastClass = c.Class
		}
		b.WriteString(fmt.Sprintf("  [%-5s] %s", c.Status, c.Item))
		if c.Note != "" {
			b.WriteString(" —— " + c.Note)
		}
		b.WriteString("\n")
	}
	r.mu.Unlock()
	b.WriteString("\n== 汇总 ==\n")
	b.WriteString(fmt.Sprintf("PASS %d · KNOWN %d · DIFF %d · FAIL %d · INFO %d\n", pass, known, diff, fail, info))
	if r.green() {
		b.WriteString("结论：对拍全绿（无 FAIL / 无非预期 DIFF）\n")
	} else {
		b.WriteString("结论：未全绿——DIFF / FAIL 逐条见上\n")
	}
	return b.String()
}

// flush 报告落盘（report.txt + summary.json）。
func (r *report) flush() (string, error) {
	if r.outDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(r.outDir, 0o755); err != nil {
		return "", err
	}
	text := r.render()
	textPath := filepath.Join(r.outDir, "report.txt")
	if err := os.WriteFile(textPath, []byte(text), 0o644); err != nil {
		return "", err
	}
	pass, known, diff, fail, info := r.counts()
	summary := map[string]any{
		"time":   time.Now().Format("2006-01-02 15:04:05"),
		"meta":   r.meta,
		"counts": map[string]int{"pass": pass, "known": known, "diff": diff, "fail": fail, "info": info},
		"green":  r.green(),
		"checks": r.checks,
	}
	raw, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(filepath.Join(r.outDir, "summary.json"), append(raw, '\n'), 0o644); err != nil {
		return textPath, err
	}
	return textPath, nil
}
