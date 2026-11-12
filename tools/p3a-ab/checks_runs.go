package main

// checks_runs —— 运行目录文件（runs/<ts>/ 三投影 + startup.log）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// logLinePattern 是 log_all.txt 的行格式（time | Fn | LEVEL | [cat] | message...）。
var logLinePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} \| F\d+ \| (INFO|WARN|ERR)( \| \[[^\]]*\])? \| .+`)

// bootLinePattern 是 startup.log 头行（C# core= / Go version=）。
var bootLinePattern = regexp.MustCompile(`^==== (CatHome[0-9]+) 启动诊断 \| (\d{8}_\d{6}) \| sink=([a-z]+) \| (core|version)=([^ ]+) ====$`)

// checkRunDirs 对拍两侧运行目录文件的文件集与行格式。
func checkRunDirs(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "runs"
	for _, side := range []*sideState{goSide, csSide} {
		if side.primaryDir == "" {
			rep.add(cls, "运行目录["+side.name+"]", stFail, "未发现本次启动的 runs 目录")
			continue
		}
		rep.add(cls, "运行目录["+side.name+"]", stInfo, side.primaryDir)
		files := dirFiles(side.primaryDir)
		// 三投影必须存在
		missing := []string{}
		for _, want := range []string{"log_all.txt", "oa_all.txt", "err_all.txt"} {
			if !files[want] {
				missing = append(missing, want)
			}
		}
		if len(missing) == 0 {
			rep.add(cls, "三投影文件["+side.name+"]", stPass, "log_all/oa_all/err_all")
		} else {
			rep.add(cls, "三投影文件["+side.name+"]", stFail, "缺 "+strings.Join(missing, ","))
		}
		// 额外文件（frame.txt 是 C# 独有的帧流）
		extra := []string{}
		for name := range files {
			if name == "log_all.txt" || name == "oa_all.txt" || name == "err_all.txt" || name == "startup.log" {
				continue
			}
			extra = append(extra, name)
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			note := strings.Join(extra, ",")
			if side.name == "cs" && contains(extra, "frame.txt") {
				rep.add(cls, "额外文件["+side.name+"]", stKnown, note+" —— C# 帧流；P3-a 无帧存储（P3-b 接主循环）")
			} else {
				rep.add(cls, "额外文件["+side.name+"]", stDiff, note)
			}
		} else {
			rep.add(cls, "额外文件["+side.name+"]", stPass, "无")
		}
		// log_all 行格式
		lines := readLines(filepath.Join(side.primaryDir, "log_all.txt"))
		if len(lines) == 0 {
			rep.add(cls, "log_all 行["+side.name+"]", stFail, "空文件")
		} else {
			bad := 0
			for i, line := range lines {
				if i >= 200 {
					break
				}
				if !logLinePattern.MatchString(line) {
					bad++
				}
			}
			if bad == 0 {
				rep.add(cls, "log_all 行["+side.name+"]", stPass, fmt.Sprintf("%d 行全部匹配七段格式", len(lines)))
			} else {
				rep.add(cls, "log_all 行["+side.name+"]", stDiff, fmt.Sprintf("%d/%d 行不符（首行: %s）", bad, len(lines), clip(lines[0])))
			}
		}
		// oa_all / err_all 投影口径
		oaLines := readLines(filepath.Join(side.primaryDir, "oa_all.txt"))
		errLines := readLines(filepath.Join(side.primaryDir, "err_all.txt"))
		rep.add(cls, "oa_all 条数["+side.name+"]", stInfo, fmt.Sprintf("%d 行", len(oaLines)))
		rep.add(cls, "err_all 条数["+side.name+"]", stInfo, fmt.Sprintf("%d 行", len(errLines)))
		if len(oaLines) > 0 && !allContain(oaLines, "| [OA] |") {
			rep.add(cls, "oa_all 投影["+side.name+"]", stDiff, "含非 OA 行")
		} else {
			rep.add(cls, "oa_all 投影["+side.name+"]", stPass, "仅 OA 行")
		}
		if len(errLines) > 0 && !allContain(errLines, "| ERR |") {
			rep.add(cls, "err_all 投影["+side.name+"]", stDiff, "含非 ERR 行")
		} else {
			rep.add(cls, "err_all 投影["+side.name+"]", stPass, "仅 ERR 行")
		}
		// startup.log
		startupPath := filepath.Join(side.primaryDir, "startup.log")
		if side.startupDir != "" {
			startupPath = filepath.Join(side.startupDir, "startup.log")
		}
		bootLines := readLines(startupPath)
		if len(bootLines) == 0 {
			rep.add(cls, "startup.log["+side.name+"]", stFail, "缺失或为空")
			continue
		}
		m := bootLinePattern.FindStringSubmatch(bootLines[0])
		if m == nil {
			rep.add(cls, "startup.log 头行["+side.name+"]", stDiff, clip(bootLines[0]))
			continue
		}
		rep.add(cls, "startup.log 头行["+side.name+"]", stPass, fmt.Sprintf("sink=%s", m[3]))
		if side.name == "cat5" {
			csStartup := filepath.Join(csSide.primaryDir, "startup.log")
			if csSide.startupDir != "" {
				csStartup = filepath.Join(csSide.startupDir, "startup.log")
			}
			csLines := readLines(csStartup)
			if len(csLines) > 0 {
				cm := bootLinePattern.FindStringSubmatch(csLines[0])
				if cm != nil {
					if m[4] != cm[4] {
						rep.add(cls, "startup.log 版本字段名", stKnown, m[4]+"= vs "+cm[4]+"= —— 诊断文本，不供前端消费")
					} else if m[3] == cm[3] {
						rep.add(cls, "startup.log 形态", stPass, "头行同构")
					}
				}
			}
		}
	}
}

// dirFiles 返回目录下文件名集合。
func dirFiles(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// readLines 读取文本文件并按行切分（去 BOM / CR）。
func readLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := strings.TrimPrefix(string(raw), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// allContain 判定所有行都含给定片段。
func allContain(lines []string, part string) bool {
	for _, line := range lines {
		if !strings.Contains(line, part) {
			return false
		}
	}
	return true
}

// contains 判定字符串在切片内。
func contains(list []string, s string) bool {
	for _, it := range list {
		if it == s {
			return true
		}
	}
	return false
}
