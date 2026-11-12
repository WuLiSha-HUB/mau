package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// FileTree 复现 file-tree——目录树（depth 层级 / limit 条数上限；稳定排序）。
func FileTree(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path depth limit", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	depth := 2
	limit := 500
	depthRaw := Get(argsJSON, "depth")
	limitRaw := Get(argsJSON, "limit")
	if depthRaw != "" {
		parsed, ok := parseIntArg(depthRaw)
		if !ok {
			return "ERR|BAD_ARGS|参数 depth 非整数: " + depthRaw, false
		}
		depth = parsed
	}
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
	facts, err := svc.TreeFacts(path, depth, limit)
	if err != nil {
		return fs.ErrText(err), false
	}
	var lines []string
	// [段1] 条目行——目录加尾标（显示约定归积木层）
	for _, entry := range facts.Entries {
		if entry.IsDir {
			lines = append(lines, entry.RelativePath+"/")
			continue
		}
		lines = append(lines, entry.RelativePath)
	}
	// [段2] git 特征行——limit 有余量时补位
	for _, git := range facts.GitFacts {
		if len(lines) >= limit {
			break
		}
		lines = append(lines, "[git] 存在 .git（"+git.RelativePath+"——HEAD: "+git.Head+"）")
	}
	// [段3] 提示行——不占 limit 配额
	if facts.IgnoredDirs > 0 {
		lines = append(lines, "[skip] "+strconv.Itoa(facts.IgnoredDirs)+" 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过")
	}
	if facts.Total > len(facts.Entries) {
		lines = append(lines, "[截断] 共 "+strconv.Itoa(facts.Total)+" 条，已列 "+strconv.Itoa(len(facts.Entries))+" 条——提高 limit 至 ≥"+strconv.Itoa(facts.Total)+"，或收窄 path / 降 depth 可看全")
	}
	shown := len(lines)
	if shown == 0 {
		return MetaHead("file-tree", true, path, 0) + "\n" + path + " | 0 条", true
	}
	return MetaHead("file-tree", true, path, shown) + "\n" + path + " | " + strconv.Itoa(shown) + " 条" + "\n" + strings.Join(lines, "\n"), true
}

// parseIntArg 整数参数解析——对齐 int.TryParse（允许首尾空白）。
func parseIntArg(raw string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return parsed, true
}
