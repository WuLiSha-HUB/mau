package fs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FindFacts 是文件名搜索结果事实——相对路径列表 + 忽略与命中计数。
type FindFacts struct {
	RelativePaths []string
	IgnoredDirs   int
	Matched       int
}

// FindFacts 文件名 glob 搜索——按文件名模式找文件（recursive 默认 true 由积木层给）。
func (s *Service) FindFacts(directory, pattern string, recursive bool, limit int) (FindFacts, error) {
	if strings.TrimSpace(pattern) == "" || limit < 1 || limit > 10000 {
		return FindFacts{}, &ToolError{Name: "ArgumentException", Msg: "Find parameters are invalid. (Parameter 'pattern')"}
	}
	root, err := s.Resolve(directory, false)
	if err != nil {
		return FindFacts{}, err
	}
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return FindFacts{}, &ToolError{Name: "DirectoryNotFoundException", Msg: "Could not find a part of the path '" + root + "'."}
	}
	facts := FindFacts{}
	var files []string
	if walkErr := appendFind(root, root, pattern, recursive, limit, &files, &facts); walkErr != nil {
		return FindFacts{}, walkErr
	}
	sort.Slice(files, func(i, j int) bool {
		return compareOrdinalIgnoreCase(files[i], files[j]) < 0
	})
	facts.RelativePaths = make([]string, 0, len(files))
	for _, file := range files {
		rel, relErr := filepath.Rel(root, file)
		if relErr != nil {
			rel = file
		}
		facts.RelativePaths = append(facts.RelativePaths, rel)
	}
	return facts, nil
}

// appendFind 递归收集匹配文件——当前目录文件按稳定顺序加入，再逐目录验证后递归。
func appendFind(root, current, pattern string, recursive bool, limit int, output *[]string, facts *FindFacts) error {
	// [段0] **/ 目录通配前缀——剥前缀 + 强制递归（连续段剥净；剥空回退 *）
	for strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
		recursive = true
	}
	if pattern == "" {
		pattern = "*"
	}
	entries, readErr := os.ReadDir(current)
	if readErr != nil {
		return wrapIO(readErr)
	}
	sortEntriesOrdinalIgnoreCase(entries)
	// [段1] 当前目录文件——匹配即计数；前 limit 条入 output（不提前退出：总量可见）
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !matchFilePattern(pattern, entry.Name()) {
			continue
		}
		facts.Matched++
		if len(*output) < limit {
			*output = append(*output, filepath.Join(current, entry.Name()))
		}
	}
	if !recursive {
		return nil
	}
	// [段2] 逐目录验证后递归，避免隐式穿过链接
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if isIgnoredDir(entry.Name()) {
			facts.IgnoredDirs++
			continue
		}
		full := filepath.Join(current, entry.Name())
		if !pathWithin(root, full) {
			continue
		}
		if walkErr := appendFind(root, full, pattern, true, limit, output, facts); walkErr != nil {
			return walkErr
		}
	}
	return nil
}

// matchFilePattern 文件名匹配——Win32 语义：最后一个点分「基名 / 扩展名」两段分别匹配；
// 无扩展名段的模式要求文件名无扩展名，基名以 * 结尾时星号吸收扩展名（* / a* / *.* 均命中带扩展名文件）。
func matchFilePattern(pattern, name string) bool {
	pattern = strings.ToLower(pattern)
	name = strings.ToLower(name)
	basePattern, extPattern, patternHasDot := splitLastDot(pattern)
	baseName, extName, _ := splitLastDot(name)
	if !globMatch(basePattern, baseName) {
		return false
	}
	if patternHasDot {
		return globMatch(extPattern, extName)
	}
	if strings.HasSuffix(basePattern, "*") {
		return true
	}
	return extName == ""
}

// splitLastDot 按最后一个点拆分——无点返回 hasDot=false，ext 为空串。
func splitLastDot(text string) (base, ext string, hasDot bool) {
	cut := strings.LastIndex(text, ".")
	if cut < 0 {
		return text, "", false
	}
	return text[:cut], text[cut+1:], true
}

// globMatch 简单通配匹配——* 任意序列 / ? 单个字符；名段耗尽时尾部 * 与 ? 可匹配零字符（Win32 语义）。
func globMatch(pattern, name string) bool {
	p := []rune(pattern)
	n := []rune(name)
	pi := 0
	ni := 0
	star := -1
	starName := 0
	for ni < len(n) {
		if pi < len(p) && (p[pi] == '?' || p[pi] == n[ni]) {
			pi++
			ni++
			continue
		}
		if pi < len(p) && p[pi] == '*' {
			star = pi
			starName = ni
			pi++
			continue
		}
		if star >= 0 {
			pi = star + 1
			starName++
			ni = starName
			continue
		}
		return false
	}
	for pi < len(p) {
		if p[pi] != '*' && p[pi] != '?' {
			return false
		}
		pi++
	}
	return true
}

// pathWithin 报告 full 是否位于 root 内（防御性归属校验）。
func pathWithin(root, full string) bool {
	trimmed := strings.TrimRight(root, string(os.PathSeparator))
	if strings.EqualFold(full, trimmed) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(full), strings.ToLower(trimmed)+string(os.PathSeparator))
}
