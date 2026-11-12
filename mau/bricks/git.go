package bricks

import (
	"encoding/json"
	"strconv"
	"strings"
)

// GitStatus 复现 git-status——仓库工作区状态（分支 / 上游 / 领先落后 / 变更条目）。
func GitStatus(svc *GitService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path limit timeout_ms", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path（仓库工作目录）", false
	}
	limit := 200
	if raw := Get(argsJSON, "limit"); raw != "" && raw != ParseFail {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || parsed <= 0 {
			return "ERR|BAD_ARGS|参数 limit 非正整数: " + raw, false
		}
		limit = parsed
	}
	timeoutMs, bad := gitTimeoutArg(argsJSON, 0)
	if bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|GIT_NO_SERVICE|宿主未注入 IGitService", false
	}
	repo, repoErr := svc.ResolveRepo(path, false)
	if repoErr != "" {
		return repoErr, false
	}
	facts := svc.Run(repo, []string{"status", "--short", "--branch"}, timeoutMs)
	if facts.Exit != 0 {
		return "ERR|GIT_STATUS_" + strconv.Itoa(facts.Exit) + "|" + gitErrorText(facts), false
	}
	branch, upstream := "", ""
	ahead, behind := 0, 0
	gone, detached := false, false
	entries := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(facts.Stdout, "\r", ""), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			branch, upstream, ahead, behind, gone, detached = parseGitBranchLine(line[3:])
			continue
		}
		entries = append(entries, line)
	}
	shown := len(entries)
	truncated := shown > limit
	var sb strings.Builder
	sb.WriteString("分支 ")
	if branch != "" {
		sb.WriteString(branch)
	} else {
		sb.WriteString("（未知）")
	}
	if upstream != "" {
		sb.WriteString(" → " + upstream)
	}
	if detached {
		sb.WriteString(" · 分离头指针")
	}
	if ahead > 0 {
		sb.WriteString(" · 领先 " + strconv.Itoa(ahead))
	}
	if behind > 0 {
		sb.WriteString(" · 落后 " + strconv.Itoa(behind))
	}
	if gone {
		sb.WriteString(" · 上游分支已删除")
	}
	if !detached && ahead == 0 && behind == 0 && !gone && upstream != "" {
		sb.WriteString(" · 与上游一致")
	}
	if len(entries) == 0 {
		sb.WriteString(" · 工作区干净")
	} else {
		sb.WriteString(" · 变更 " + strconv.Itoa(shown) + " 项")
	}
	if truncated {
		sb.WriteString("\n[截断] 变更共 " + strconv.Itoa(shown) + " 项，已列 " + strconv.Itoa(limit) + " 项（limit 提额可看全）")
	}
	for i := 0; i < shown && i < limit; i++ {
		sb.WriteString("\n" + entries[i])
	}
	if facts.Truncated {
		sb.WriteString("\n[截断] 输出触达 60000 字符上限（git-status 正常不会到这里，出现请反馈）")
	}
	fields := [][2]any{
		{"branch", branch},
		{"upstream", upstream},
		{"ahead", ahead},
		{"behind", behind},
		{"changed", shown},
	}
	return MetaHeadFields("git-status", true, repo, shown, fields) + "\n" + repo + " | " + sb.String(), true
}

// parseGitBranchLine 解析分支行——`main...origin/main [ahead 1, behind 2]` / `HEAD (no branch)` / `No commits yet on main`。
func parseGitBranchLine(raw string) (branch, upstream string, ahead, behind int, gone, detached bool) {
	detached = true
	text := strings.TrimSpace(raw)
	// 括号内注记（ahead / behind / gone）先剥出
	notes := ""
	if bracket := strings.Index(text, " ["); bracket >= 0 && strings.HasSuffix(text, "]") {
		notes = text[bracket+2 : len(text)-1]
		text = text[:bracket]
	}
	if dots := strings.Index(text, "..."); dots >= 0 {
		branch = strings.TrimSpace(text[:dots])
		upstream = strings.TrimSpace(text[dots+3:])
		detached = false
	} else {
		branch = strings.TrimSpace(text)
		switch {
		case branch == "HEAD (no branch)":
			branch = "HEAD"
			detached = true
		case strings.HasPrefix(branch, "No commits yet on "):
			branch = strings.TrimSpace(branch[len("No commits yet on "):])
			detached = false
		case strings.HasPrefix(branch, "Initial commit on "):
			branch = strings.TrimSpace(branch[len("Initial commit on "):])
			detached = false
		default:
			detached = false
		}
	}
	if notes == "" {
		return
	}
	for _, part := range strings.Split(notes, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "ahead ") {
			if v, err := strconv.Atoi(part[6:]); err == nil {
				ahead = v
			}
		} else if strings.HasPrefix(part, "behind ") {
			if v, err := strconv.Atoi(part[7:]); err == nil {
				behind = v
			}
		} else if part == "gone" {
			gone = true
		}
	}
	return
}

// GitLog 复现 git-log——最近 N 条提交（短哈希 / 日期 / 作者 / 标题），可按路径过滤。
func GitLog(svc *GitService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path count path_filter timeout_ms", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path（仓库工作目录）", false
	}
	count := 20
	if raw := Get(argsJSON, "count"); raw != "" && raw != ParseFail {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || parsed <= 0 {
			return "ERR|BAD_ARGS|参数 count 非正整数: " + raw, false
		}
		count = parsed
		if count > 200 {
			count = 200
		}
	}
	pathFilter := Get(argsJSON, "path_filter")
	if pathFilter == ParseFail {
		pathFilter = ""
	}
	timeoutMs, bad := gitTimeoutArg(argsJSON, 0)
	if bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|GIT_NO_SERVICE|宿主未注入 IGitService", false
	}
	repo, repoErr := svc.ResolveRepo(path, false)
	if repoErr != "" {
		return repoErr, false
	}
	gitArgs := []string{"log", "-n", strconv.Itoa(count), "--date=short", "--pretty=format:%h %ad %an %s"}
	if pathFilter != "" {
		gitArgs = append(gitArgs, "--", pathFilter)
	}
	facts := svc.Run(repo, gitArgs, timeoutMs)
	if facts.Exit != 0 {
		return "ERR|GIT_LOG_" + strconv.Itoa(facts.Exit) + "|" + gitErrorText(facts), false
	}
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(facts.Stdout, "\r", ""), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	var sb strings.Builder
	sb.WriteString("最近 " + strconv.Itoa(len(lines)) + " 条提交")
	if pathFilter != "" {
		sb.WriteString(" · 路径过滤 " + pathFilter)
	}
	for _, line := range lines {
		sb.WriteString("\n" + line)
	}
	if len(lines) == 0 {
		sb.WriteString("\n（无匹配提交——空仓库 / 过滤路径无历史 / count 之外的提交）")
	}
	if facts.Truncated {
		sb.WriteString("\n[截断] 输出触达 60000 字符上限（count 调小可避）")
	}
	fields := [][2]any{}
	if pathFilter != "" {
		fields = append(fields, [2]any{"path_filter", pathFilter})
	}
	return MetaHeadFields("git-log", true, repo, len(lines), fields) + "\n" + repo + " | " + sb.String(), true
}

// GitDiff 复现 git-diff——工作区差异（未暂存 / 已暂存两态），按字符上限截断并自证。
func GitDiff(svc *GitService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path staged path_filter limit timeout_ms", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path（仓库工作目录）", false
	}
	staged := false
	if raw := Get(argsJSON, "staged"); raw != "" && raw != ParseFail {
		parsed, ok := boolArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 staged 非布尔: " + raw, false
		}
		staged = parsed
	}
	pathFilter := Get(argsJSON, "path_filter")
	if pathFilter == ParseFail {
		pathFilter = ""
	}
	limit, bad := gitLimitArg(argsJSON, 8000, 60000)
	if bad != "" {
		return bad, false
	}
	timeoutMs, bad := gitTimeoutArg(argsJSON, 0)
	if bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|GIT_NO_SERVICE|宿主未注入 IGitService", false
	}
	repo, repoErr := svc.ResolveRepo(path, false)
	if repoErr != "" {
		return repoErr, false
	}
	gitArgs := []string{"diff"}
	if staged {
		gitArgs = append(gitArgs, "--staged")
	}
	if pathFilter != "" {
		gitArgs = append(gitArgs, "--", pathFilter)
	}
	facts := svc.Run(repo, gitArgs, timeoutMs)
	if facts.Exit != 0 {
		return "ERR|GIT_DIFF_" + strconv.Itoa(facts.Exit) + "|" + gitErrorText(facts), false
	}
	diff := strings.ReplaceAll(facts.Stdout, "\r", "")
	fileCount, added, removed := 0, 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "diff --cc "):
			fileCount++
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removed++
		}
	}
	truncated := utf16Len(diff) > limit
	shownText := diff
	if truncated {
		shownText = utf16Truncate(diff, limit)
	}
	var sb strings.Builder
	if staged {
		sb.WriteString("已暂存")
	} else {
		sb.WriteString("未暂存")
	}
	if pathFilter != "" {
		sb.WriteString(" · 路径过滤 " + pathFilter)
	}
	if diff == "" {
		sb.WriteString(" · 无差异")
	} else {
		sb.WriteString(" · " + strconv.Itoa(fileCount) + " 个文件")
		sb.WriteString(" · +" + strconv.Itoa(added) + " −" + strconv.Itoa(removed))
	}
	if truncated {
		sb.WriteString("\n[截断] diff 共 " + groupDigits(int64(utf16Len(diff))) + " 字符，已列 " + groupDigits(int64(limit)) + " 字符（limit 提额可看全）")
	}
	sb.WriteString("\n--- diff ---")
	if shownText != "" {
		sb.WriteString("\n" + strings.TrimRight(shownText, "\n"))
	} else {
		sb.WriteString("\n（无）")
	}
	if facts.Truncated {
		sb.WriteString("\n[截断] 输出触达 60000 字符上限（limit 调小可避）")
	}
	fields := [][2]any{
		{"staged", staged},
		{"files", fileCount},
		{"added", added},
		{"removed", removed},
	}
	if pathFilter != "" {
		fields = append(fields, [2]any{"path_filter", pathFilter})
	}
	return MetaHeadFields("git-diff", true, repo, fileCount, fields) + "\n" + repo + " | " + sb.String(), true
}

// GitShow 复现 git-show——单个提交详情（元信息 + 变更统计 + 补丁，按字符上限截断并自证）。
func GitShow(svc *GitService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path rev limit timeout_ms", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path（仓库工作目录）", false
	}
	rev := Get(argsJSON, "rev")
	if rev == ParseFail {
		rev = ""
	}
	if rev == "" {
		rev = "HEAD"
	}
	if strings.HasPrefix(rev, "-") {
		// 选项注入防护——rev 一律按提交名解析，不接受以 `-` 开头的形态
		return "ERR|BAD_ARGS|参数 rev 非法（不接受以 - 开头的形态）: " + rev, false
	}
	limit, bad := gitLimitArg(argsJSON, 8000, 60000)
	if bad != "" {
		return bad, false
	}
	timeoutMs, bad := gitTimeoutArg(argsJSON, 0)
	if bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|GIT_NO_SERVICE|宿主未注入 IGitService", false
	}
	repo, repoErr := svc.ResolveRepo(path, false)
	if repoErr != "" {
		return repoErr, false
	}
	facts := svc.Run(repo, []string{"show", "--stat", "--date=short", "--pretty=format:%h %ad %an%n%s%n%b", rev}, timeoutMs)
	if facts.Exit != 0 {
		return "ERR|GIT_SHOW_" + strconv.Itoa(facts.Exit) + "|" + gitErrorText(facts), false
	}
	body := strings.TrimRight(strings.ReplaceAll(facts.Stdout, "\r", ""), "\n")
	truncated := utf16Len(body) > limit
	shownText := body
	if truncated {
		shownText = utf16Truncate(body, limit)
	}
	headline := gitFirstLine(body)
	var sb strings.Builder
	sb.WriteString(rev)
	if headline != "" {
		sb.WriteString(" · " + headline)
	}
	if truncated {
		sb.WriteString("\n[截断] 输出共 " + groupDigits(int64(utf16Len(body))) + " 字符，已列 " + groupDigits(int64(limit)) + " 字符（limit 提额可看全）")
	}
	sb.WriteString("\n--- show ---")
	sb.WriteString("\n" + shownText)
	if facts.Truncated {
		sb.WriteString("\n[截断] 输出触达 60000 字符上限（limit 调小可避）")
	}
	fields := [][2]any{{"rev", rev}}
	return MetaHeadFields("git-show", true, repo, -1, fields) + "\n" + repo + " | " + sb.String(), true
}

// GitCommit 复现 git-commit——显式 paths 或 add_all 全量；两步（add → commit）任一失败即 ERR。
func GitCommit(svc *GitService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path message add_all paths timeout_ms", "path message", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path（仓库工作目录）", false
	}
	message := Get(argsJSON, "message")
	if message == ParseFail {
		message = ""
	}
	if strings.TrimSpace(message) == "" {
		return "ERR|BAD_ARGS|缺少参数 message（提交信息）", false
	}
	addAll := false
	if raw := Get(argsJSON, "add_all"); raw != "" && raw != ParseFail {
		parsed, ok := boolArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 add_all 非布尔: " + raw, false
		}
		addAll = parsed
	}
	pathsRaw := Get(argsJSON, "paths")
	if pathsRaw == ParseFail {
		pathsRaw = ""
	}
	paths := parseGitPaths(pathsRaw)
	if !addAll && len(paths) == 0 {
		return "ERR|BAD_ARGS|未指定提交范围——paths 给显式路径列表，或 add_all=true 覆盖全仓", false
	}
	for _, item := range paths {
		if strings.HasPrefix(item, "-") {
			return "ERR|BAD_ARGS|paths 含非法项（不接受以 - 开头的形态）: " + item, false
		}
	}
	timeoutMs, bad := gitTimeoutArg(argsJSON, 0)
	if bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|GIT_NO_SERVICE|宿主未注入 IGitService", false
	}
	repo, repoErr := svc.ResolveRepo(path, true)
	if repoErr != "" {
		return repoErr, false
	}
	// 第一步：暂存（add_all 全量 / 显式路径）
	addArgs := []string{"add"}
	if addAll {
		addArgs = append(addArgs, "-A")
	} else {
		addArgs = append(addArgs, "--")
		addArgs = append(addArgs, paths...)
	}
	addFacts := svc.Run(repo, addArgs, timeoutMs)
	if addFacts.Exit != 0 {
		return "ERR|GIT_ADD_" + strconv.Itoa(addFacts.Exit) + "|" + gitErrorText(addFacts), false
	}
	// 第二步：提交
	commitFacts := svc.Run(repo, []string{"commit", "-m", message}, timeoutMs)
	if commitFacts.Exit != 0 {
		return "ERR|GIT_COMMIT_" + strconv.Itoa(commitFacts.Exit) + "|" + gitErrorText(commitFacts), false
	}
	output := strings.TrimSpace(strings.ReplaceAll(commitFacts.Stdout+commitFacts.Stderr, "\r", ""))
	headline := gitFirstLine(output)
	var sb strings.Builder
	sb.WriteString("已提交")
	if addAll {
		sb.WriteString(" · 全量暂存（add_all）")
	} else {
		sb.WriteString(" · 暂存 " + strconv.Itoa(len(paths)) + " 条路径")
	}
	if headline != "" {
		sb.WriteString(" · " + headline)
	}
	if output != "" {
		sb.WriteString("\n--- commit ---\n" + output)
	}
	fields := [][2]any{
		{"add_all", addAll},
		{"paths", len(paths)},
	}
	return MetaHeadFields("git-commit", true, repo, len(paths), fields) + "\n" + repo + " | " + sb.String(), true
}

// parseGitPaths 解析路径列表——JSON 数组原文优先（如 ["a.cs","b.cs"]），否则逗号分隔；空项剔除。
func parseGitPaths(raw string) []string {
	paths := []string{}
	text := strings.TrimSpace(raw)
	if text == "" {
		return paths
	}
	if strings.HasPrefix(text, "[") {
		var items []any
		if err := json.Unmarshal([]byte(text), &items); err == nil {
			for _, item := range items {
				value := ""
				if s, isStr := item.(string); isStr {
					value = s
				} else if encoded, err := json.Marshal(item); err == nil {
					value = string(encoded)
				}
				if strings.TrimSpace(value) != "" {
					paths = append(paths, strings.TrimSpace(value))
				}
			}
			return paths
		}
		// 解析失败回落逗号分隔——paths 是内容面参数，形态宽容、逐项仍校验
	}
	for _, part := range strings.Split(text, ",") {
		if strings.TrimSpace(part) != "" {
			paths = append(paths, strings.TrimSpace(part))
		}
	}
	return paths
}

// GitPush 复现 git-push——remote 默认 origin · branch 缺省 HEAD（当前分支）；走系统凭据。
func GitPush(svc *GitService, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path remote branch timeout_ms", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path（仓库工作目录）", false
	}
	remote := Get(argsJSON, "remote")
	if remote == ParseFail || remote == "" {
		remote = "origin"
	}
	branch := Get(argsJSON, "branch")
	if branch == ParseFail {
		branch = ""
	}
	if branch == "" {
		branch = "HEAD"
	}
	if strings.HasPrefix(remote, "-") || strings.HasPrefix(branch, "-") {
		return "ERR|BAD_ARGS|remote / branch 不接受以 - 开头的形态", false
	}
	timeoutMs, bad := gitTimeoutArg(argsJSON, 60000)
	if bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|GIT_NO_SERVICE|宿主未注入 IGitService", false
	}
	repo, repoErr := svc.ResolveRepo(path, true)
	if repoErr != "" {
		return repoErr, false
	}
	facts := svc.Run(repo, []string{"push", remote, branch}, timeoutMs)
	if facts.Exit != 0 {
		return "ERR|GIT_PUSH_" + strconv.Itoa(facts.Exit) + "|" + gitErrorText(facts), false
	}
	// 推送进度与结果主要在 stderr（git 的约定）——成功时一并展示
	output := strings.TrimSpace(strings.ReplaceAll(facts.Stderr+facts.Stdout, "\r", ""))
	var sb strings.Builder
	sb.WriteString("推送 " + remote + "/")
	if strings.EqualFold(branch, "HEAD") {
		sb.WriteString("当前分支")
	} else {
		sb.WriteString(branch)
	}
	sb.WriteString(" · " + strconv.FormatInt(facts.ElapsedMs, 10) + " ms")
	if output != "" {
		sb.WriteString("\n--- push ---\n" + output)
	} else {
		sb.WriteString("\n（无输出——通常表示已是最新）")
	}
	fields := [][2]any{
		{"remote", remote},
		{"branch", branch},
		{"elapsed_ms", facts.ElapsedMs},
	}
	return MetaHeadFields("git-push", true, repo, -1, fields) + "\n" + repo + " | " + sb.String(), true
}

// gitTimeoutArg 解析 timeout_ms——缺省用 def；非整数报错（对齐 C# int.TryParse）。
func gitTimeoutArg(argsJSON string, def int) (int, string) {
	raw := Get(argsJSON, "timeout_ms")
	if raw == "" || raw == ParseFail {
		return def, ""
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, "ERR|BAD_ARGS|参数 timeout_ms 非整数: " + raw
	}
	return parsed, ""
}

// gitLimitArg 解析 limit——缺省用 def；非正整数报错；超过 max 钳制。
func gitLimitArg(argsJSON string, def, max int) (int, string) {
	raw := Get(argsJSON, "limit")
	if raw == "" || raw == ParseFail {
		return def, ""
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return 0, "ERR|BAD_ARGS|参数 limit 非正整数: " + raw
	}
	if parsed > max {
		parsed = max
	}
	return parsed, ""
}

// gitErrorText 失败正文——优先 stderr，空则 stdout，再空给退出码说明（去 \r）。
func gitErrorText(facts GitFacts) string {
	text := strings.TrimSpace(facts.Stderr)
	if text == "" {
		text = strings.TrimSpace(facts.Stdout)
	}
	if text == "" {
		text = "git 退出 " + strconv.Itoa(facts.Exit) + "（无输出）"
	}
	return strings.ReplaceAll(text, "\r", "")
}

// gitFirstLine 首个非空行（摘要用）。
func gitFirstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
