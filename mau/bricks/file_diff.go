package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// FileDiff 复现 file-diff——对比两个文件或两个目录（差异是结论不是失败）。
func FileDiff(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "left right content limit", "left right", "", ""); bad != "" {
		return bad, false
	}
	left := Get(argsJSON, "left")
	right := Get(argsJSON, "right")
	if left == ParseFail || right == ParseFail {
		return badParse, false
	}
	if left == "" || right == "" {
		return "ERR|BAD_ARGS|缺少参数 left 或 right", false
	}
	content := false
	contentRaw := Get(argsJSON, "content")
	if contentRaw != "" {
		parsed, ok := boolArg(contentRaw)
		if !ok {
			return "ERR|BAD_ARGS|参数 content 非布尔: " + contentRaw, false
		}
		content = parsed
	}
	limit := 200
	limitRaw := Get(argsJSON, "limit")
	if limitRaw != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(limitRaw))
		if err != nil {
			return "ERR|BAD_ARGS|参数 limit 非整数: " + limitRaw, false
		}
		limit = parsed
	}
	if svc == nil {
		return "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService", false
	}
	facts, err := svc.DiffFacts(left, right, content, limit)
	if err != nil {
		return fs.ErrText(err), false
	}
	if facts.BothFiles {
		return fileDiffText(facts, left), true
	}
	return dirDiffText(facts, left), true
}

// fileDiffText 文件模式结论——同异 + 大小 + 首个差异偏移。
func fileDiffText(facts fs.DiffFacts, leftArg string) string {
	var sb strings.Builder
	sb.WriteString("左: " + facts.LeftPath + "（" + groupDigits(facts.LeftSizeBytes) + " 字节）")
	sb.WriteString("\n右: " + facts.RightPath + "（" + groupDigits(facts.RightSizeBytes) + " 字节）")
	if facts.Same {
		sb.WriteString("\n判定: 逐字节相同")
	} else {
		sb.WriteString("\n判定: 内容不同（首个差异偏移 " + groupDigits(facts.FirstDiffOffset))
		if facts.LeftSizeBytes != facts.RightSizeBytes {
			sb.WriteString(" · 大小差 " + groupDigits(facts.RightSizeBytes-facts.LeftSizeBytes) + " 字节")
		}
		sb.WriteString("）")
	}
	summary := "不同"
	items := 1
	if facts.Same {
		summary = "相同"
		items = 0
	}
	return MetaHead("file-diff", true, leftArg, items) + "\n" + leftArg + " | " + summary + " · " + facts.LeftPath + " ↔ " + facts.RightPath + "\n" + sb.String()
}

// dirDiffText 目录模式结论——汇总计数 + 判定口径 + 差异清单 + 注记与截断提示。
func dirDiffText(facts fs.DiffFacts, leftArg string) string {
	var sb strings.Builder
	sb.WriteString("左: " + facts.LeftPath)
	sb.WriteString("\n右: " + facts.RightPath)
	if facts.Same {
		sb.WriteString("\n判定: 两侧内容一致")
	} else {
		sb.WriteString("\n判定: 存在 " + strconv.Itoa(facts.DiffTotal) + " 处差异")
	}
	sb.WriteString("\n汇总: 共 " + strconv.Itoa(facts.Total) + " 个文件 · 相同 " + strconv.Itoa(facts.SameCount))
	sb.WriteString(" · 差异 " + strconv.Itoa(facts.DiffTotal))
	// [段1] 判定口径明示——不把「按大小判同」伪装成「逐字节相同」
	if facts.ContentCompared {
		sb.WriteString("\n[口径] 按「路径集合 + 大小 + 逐字节」判定（content=true）")
	} else {
		sb.WriteString("\n[口径] 按「路径集合 + 大小」判定；同大小的 " + strconv.Itoa(facts.SameCount) + " 对未逐字节比对（加 content=true 可逐字节）")
	}
	if facts.IgnoredDirs > 0 {
		sb.WriteString("\n[skip] " + strconv.Itoa(facts.IgnoredDirs) + " 个忽略目录（.git/bin/obj/node_modules 等）未参与比对")
	}
	if len(facts.Notes) > 0 {
		sb.WriteString("\n[注记] 比对未覆盖 " + strconv.Itoa(len(facts.Notes)) + " 处:")
		for _, note := range facts.Notes {
			sb.WriteString("\n  " + note)
		}
	}
	// [段2] 差异清单——只在有条目时出段
	if len(facts.Entries) > 0 {
		sb.WriteString("\n差异清单:")
		for _, entry := range facts.Entries {
			sb.WriteString("\n  " + diffStatusText(entry.Status) + "  " + entry.RelativePath)
		}
	}
	// [段3] 截断自证——集合缩水必须出声
	if facts.DiffTotal > len(facts.Entries) {
		sb.WriteString("\n[截断] 共 " + strconv.Itoa(facts.DiffTotal) + " 条差异，已列 " + strconv.Itoa(len(facts.Entries)))
		sb.WriteString(" 条——提高 limit 至 ≥" + strconv.Itoa(facts.DiffTotal) + " 可看全")
	}
	summary := strconv.Itoa(facts.DiffTotal) + " 处差异"
	if facts.Same {
		summary = "一致"
	}
	return MetaHead("file-diff", true, leftArg, facts.DiffTotal) + "\n" + leftArg + " | " + summary + " · " + facts.LeftPath + " ↔ " + facts.RightPath + "\n" + sb.String()
}

// diffStatusText 差异形态展示文案——服务给枚举值，中文归积木层。
func diffStatusText(status string) string {
	switch status {
	case "only-left":
		return "仅左"
	case "only-right":
		return "仅右"
	}
	return "不同"
}
