package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// TextGrep 复现 text-grep——目录内递归扫文本文件，返回 相对路径:行号:上下文。
//
// 展示层口径与 C# BRIK-TEXT-009 逐字对齐：
// 命中行 → [skip] 忽略目录提示 → [截断] 自证 → [skip-file] 逐条。
func TextGrep(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "dir keyword pattern limit", "dir keyword", "", ""); bad != "" {
		return bad, false
	}
	dir := Get(argsJSON, "dir")
	keyword := Get(argsJSON, "keyword")
	if dir == ParseFail || keyword == ParseFail {
		return badParse, false
	}
	if dir == "" || keyword == "" {
		return "ERR|BAD_ARGS|缺少参数 dir 或 keyword", false
	}
	pattern := Get(argsJSON, "pattern")
	if pattern == "" {
		pattern = "*"
	}
	limit := 200
	limitRaw := Get(argsJSON, "limit")
	if limitRaw != "" {
		n, err := strconv.Atoi(limitRaw)
		if err != nil {
			return "ERR|BAD_ARGS|参数 limit 非整数: " + limitRaw, false
		}
		limit = n
	}
	facts, err := svc.Grep(dir, keyword, pattern, limit)
	if err != nil {
		return fs.ErrText(err), false
	}
	var lines []string
	for _, h := range facts.Hits {
		lines = append(lines, h.RelativePath+":"+strconv.Itoa(h.Line)+":"+h.Context)
	}
	if facts.IgnoredDirs > 0 {
		lines = append(lines, "[skip] "+strconv.Itoa(facts.IgnoredDirs)+" 个忽略目录（.git/bin/obj/node_modules 等）未扫描——匹配条目在其内已跳过")
	}
	if facts.HitTotal > len(facts.Hits) {
		lines = append(lines, "[截断] 共 "+strconv.Itoa(facts.HitTotal)+" 条，已列 "+strconv.Itoa(len(facts.Hits))+" 条——提高 limit 至 ≥"+strconv.Itoa(facts.HitTotal)+"，或收窄 dir / keyword 可看全")
	}
	for _, sk := range facts.Skipped {
		lines = append(lines, "[skip-file] "+sk.RelativePath+" | "+sk.Reason)
	}
	hits := len(lines)
	head := MetaHead("text-grep", true, dir, hits) + "\n" + dir + " | 命中 " + strconv.Itoa(hits)
	if hits == 0 {
		return head, true
	}
	return head + "\n" + strings.Join(lines, "\n"), true
}
