package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// FileFind 复现 file-find——文件名 glob 搜索（recursive 默认 true）。
func FileFind(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "dir pattern recursive limit", "dir", "", ""); bad != "" {
		return bad, false
	}
	dir := Get(argsJSON, "dir")
	if dir == ParseFail {
		return badParse, false
	}
	if dir == "" {
		return "ERR|BAD_ARGS|缺少参数 dir", false
	}
	pattern := Get(argsJSON, "pattern")
	if pattern == ParseFail {
		return badParse, false
	}
	if pattern == "" {
		pattern = "*"
	}
	recursive := true
	recRaw := Get(argsJSON, "recursive")
	if recRaw != "" && (recRaw == "false" || recRaw == "0") {
		recursive = false
	}
	limit := 500
	limitRaw := Get(argsJSON, "limit")
	if limitRaw != "" {
		parsed, ok := parseIntArg(limitRaw)
		if !ok {
			return "ERR|BAD_ARGS|参数 limit 非整数: " + limitRaw, false
		}
		limit = parsed
	}
	if svc == nil {
		return "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService", false
	}
	facts, err := svc.FindFacts(dir, pattern, recursive, limit)
	if err != nil {
		return fs.ErrText(err), false
	}
	var lines []string
	lines = append(lines, facts.RelativePaths...)
	if facts.IgnoredDirs > 0 {
		lines = append(lines, "[skip] "+strconv.Itoa(facts.IgnoredDirs)+" 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过")
	}
	if facts.Matched > len(facts.RelativePaths) {
		lines = append(lines, "[截断] 共 "+strconv.Itoa(facts.Matched)+" 条，已列 "+strconv.Itoa(len(facts.RelativePaths))+" 条——提高 limit 至 ≥"+strconv.Itoa(facts.Matched)+"，或收窄 dir / pattern 可看全")
	}
	if len(lines) == 0 {
		return MetaHead("file-find", true, dir, 0) + "\n" + dir + " | 0 条", true
	}
	return MetaHead("file-find", true, dir, len(lines)) + "\n" + dir + " | " + strconv.Itoa(len(lines)) + " 条" + "\n" + strings.Join(lines, "\n"), true
}
