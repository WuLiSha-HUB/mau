package fs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GrepHit 是一条命中——相对路径 + 行号 + 上下文（前后各 ≤10 字符）。
type GrepHit struct {
	RelativePath string
	Line         int
	Context      string
}

// GrepSkip 是一个被跳过的文件——相对路径 + 原因（异常类名）。
type GrepSkip struct {
	RelativePath string
	Reason       string
}

// GrepFacts 是内容搜索事实——命中 / 跳过 / 忽略计数 / 总命中数。
type GrepFacts struct {
	Hits        []GrepHit
	Skipped     []GrepSkip
	IgnoredDirs int
	HitTotal    int
}

// ignoredDirNames 是递归遍历跳过名单——与 C# FileSystemService 同清单（精确匹配，忽略大小写）。
var ignoredDirNames = []string{".git", "bin", "obj", "node_modules", ".vs", "dist", "build", "out", ".idea", ".godot"}

// isIgnoredDir 报告目录名是否在忽略名单。
func isIgnoredDir(name string) bool {
	for _, it := range ignoredDirNames {
		if strings.EqualFold(name, it) {
			return true
		}
	}
	return false
}

// Grep 内容关键词搜索——大小写敏感；命中上限只影响 Hits 条数，HitTotal 按全量计数。
func (s *Service) Grep(directory, keyword, pattern string, limit int) (GrepFacts, error) {
	if strings.TrimSpace(keyword) == "" || limit < 1 || limit > 10000 {
		return GrepFacts{}, &ToolError{Name: "ArgumentException", Msg: "Grep parameters are invalid."}
	}
	root, err := s.Resolve(directory, false)
	if err != nil {
		return GrepFacts{}, err
	}
	filter := strings.TrimSpace(pattern)
	if filter == "" || filter == "*" {
		filter = "*"
	}
	files, ignored := collectFiles(root, filter)
	facts := GrepFacts{IgnoredDirs: ignored}
	for _, file := range files {
		rel, relErr := filepath.Rel(root, file)
		if relErr != nil {
			rel = file
		}
		raw, readErr := os.ReadFile(file)
		if readErr != nil {
			facts.Skipped = append(facts.Skipped, GrepSkip{RelativePath: rel, Reason: "IOException"})
			continue
		}
		text, decodeErr := decode(file, raw)
		if decodeErr != nil {
			facts.Skipped = append(facts.Skipped, GrepSkip{RelativePath: rel, Reason: "ArgumentException"})
			continue
		}
		for n, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			idx := indexRunes(line, keyword)
			if idx < 0 {
				continue
			}
			facts.HitTotal++
			if len(facts.Hits) >= limit {
				continue
			}
			runes := []rune(line)
			start := idx - 10
			if start < 0 {
				start = 0
			}
			end := start + 10 + len([]rune(keyword)) + 10
			if end > len(runes) {
				end = len(runes)
			}
			facts.Hits = append(facts.Hits, GrepHit{RelativePath: rel, Line: n + 1, Context: string(runes[start:end])})
		}
	}
	return facts, nil
}

// collectFiles 递归收集匹配文件——先当前目录文件，再子目录；忽略目录计数。
func collectFiles(root, pattern string) (files []string, ignored int) {
	recursive := true
	for strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
	}
	if pattern == "" {
		pattern = "*"
	}
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool {
			return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
		})
		// [段1] 当前目录文件——匹配即收
		for _, e := range entries {
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			if matchFilePattern(pattern, e.Name()) {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
		if !recursive {
			return
		}
		// [段2] 子目录——忽略名单跳过并计数
		for _, e := range entries {
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			if isIgnoredDir(e.Name()) {
				ignored++
				continue
			}
			walk(filepath.Join(dir, e.Name()))
		}
	}
	walk(root)
	return files, ignored
}

// indexRunes 按 rune 序列找子串——返回 rune 索引，未命中返回 -1。
func indexRunes(text, sub string) int {
	hay := []rune(text)
	needle := []rune(sub)
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
