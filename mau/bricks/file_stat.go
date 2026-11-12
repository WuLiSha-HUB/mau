package bricks

import (
	"math"
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// FileStat 复现 file-stat——路径状态（文件 / 目录 + 递归统计）。
func FileStat(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path recursive", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	recursive := false
	recursiveRaw := Get(argsJSON, "recursive")
	if recursiveRaw != "" {
		parsed, ok := boolArg(recursiveRaw)
		if !ok {
			return "ERR|BAD_ARGS|参数 recursive 非布尔: " + recursiveRaw, false
		}
		recursive = parsed
	}
	if svc == nil {
		return "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService", false
	}
	facts, err := svc.StatFacts(path, recursive)
	if err != nil {
		return fs.ErrText(err), false
	}
	// [展示层] 文案与单位换算归积木层（A228 档 2 · B3）
	var sb strings.Builder
	sb.WriteString("路径: " + facts.ResolvedPath)
	kind := "文件"
	if facts.IsDirectory {
		kind = "目录"
	}
	sb.WriteString("\n类型: " + kind)
	sb.WriteString("\n修改时间: " + facts.LastWrite.Format("2006-01-02 15:04:05"))
	sb.WriteString("\n创建时间: " + facts.CreationTime.Format("2006-01-02 15:04:05"))
	items := -1
	if !facts.IsDirectory {
		sb.WriteString("\n大小: " + statSizeText(facts.SizeBytes))
		items = 1
	} else if facts.Recursive {
		sb.WriteString("\n[递归] 合计 " + statSizeText(facts.TotalBytes))
		sb.WriteString(" · 文件 " + strconv.Itoa(facts.FileCount) + " 个")
		sb.WriteString(" · 子目录 " + strconv.Itoa(facts.DirectoryCount) + " 个")
		if len(facts.Notes) > 0 {
			// 失败可见——不可读目录 / 跳过的重解析点不静默（否则「总字节」静默偏小）
			sb.WriteString("\n[注记] 统计未覆盖 " + strconv.Itoa(len(facts.Notes)) + " 处:")
			for _, note := range facts.Notes {
				sb.WriteString("\n  " + note)
			}
		}
		items = facts.FileCount
	} else {
		sb.WriteString("\n[提示] 目录大小须递归统计——加 recursive=true 可拿合计字节 / 文件数 / 子目录数")
	}
	return MetaHead("file-stat", true, path, items) + "\n" + path + " | " + sb.String(), true
}

// statSizeText 字节数展示——千分位字节 + 换算单位（1024 进制；≥1 KB 才带括号换算）。
func statSizeText(n int64) string {
	raw := groupDigits(n) + " 字节"
	if n < 1024 {
		return raw
	}
	kb := float64(n) / 1024.0
	if kb < 1024.0 {
		return raw + "（" + formatTwo(kb) + " KB）"
	}
	mb := kb / 1024.0
	if mb < 1024.0 {
		return raw + "（" + formatTwo(mb) + " MB）"
	}
	return raw + "（" + formatTwo(mb/1024.0) + " GB）"
}

// groupDigits 千分位整数文本——对齐 .NET "N0"（每 3 位逗号）。
func groupDigits(n int64) string {
	if n < 0 {
		return "-" + groupDigits(-n)
	}
	raw := strconv.FormatInt(n, 10)
	if len(raw) <= 3 {
		return raw
	}
	var b strings.Builder
	first := len(raw) % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(raw[:first])
	for i := first; i < len(raw); i += 3 {
		b.WriteByte(',')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

// formatTwo 两位小数文本——对齐 .NET "0.00"（四舍五入远离零）。
func formatTwo(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64)
}
