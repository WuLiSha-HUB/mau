package fs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Entry 是目录树条目事实——相对路径 + 目录标志（尾标属展示约定，归积木层）。
type Entry struct {
	RelativePath string
	IsDir        bool
}

// GitFact 是 git 目录特征事实——相对路径 + HEAD 摘要（展示文案归积木层）。
type GitFact struct {
	RelativePath string
	Head         string
}

// TreeFacts 是目录树事实——条目 + git 特征 + 忽略与总量计数（limit 只影响 Entries 条数）。
type TreeFacts struct {
	Entries     []Entry
	GitFacts    []GitFact
	IgnoredDirs int
	Total       int
}

// TreeFacts 目录树事实——广度优先逐层推进；忽略目录跳过并计数，.git 记特征事实。
func (s *Service) TreeFacts(path string, depth, limit int) (TreeFacts, error) {
	if depth < 1 || depth > 10 || limit < 1 || limit > 10000 {
		return TreeFacts{}, &ToolError{Name: "ArgumentOutOfRangeException", Msg: "Specified argument was out of the range of valid values. (Parameter 'depth')"}
	}
	root, err := s.Resolve(path, false)
	if err != nil {
		return TreeFacts{}, err
	}
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return TreeFacts{}, &ToolError{Name: "DirectoryNotFoundException", Msg: "Tree root was not found."}
	}
	facts := TreeFacts{}
	currentLayer := []string{root}
	remainLayer := depth
	for len(currentLayer) > 0 {
		var nextLayer []string
		for _, dir := range currentLayer {
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				return TreeFacts{}, wrapIO(readErr)
			}
			sortEntriesOrdinalIgnoreCase(entries)
			for _, entry := range entries {
				full := filepath.Join(dir, entry.Name())
				rel, relErr := filepath.Rel(root, full)
				if relErr != nil {
					rel = full
				}
				isDir := entry.IsDir()
				if !isDir && entry.Type()&os.ModeSymlink != 0 {
					if target, targetErr := os.Stat(full); targetErr == nil && target.IsDir() {
						isDir = true
					}
				}
				// [忽略目录] .git 记特征事实（不列出），其余忽略名单目录跳过（子项不展开）；计数不静默
				if isDir && isIgnoredDir(entry.Name()) {
					if strings.EqualFold(entry.Name(), ".git") {
						facts.GitFacts = append(facts.GitFacts, collectGitFact(rel, full))
					}
					facts.IgnoredDirs++
					continue
				}
				facts.Total++
				if facts.Total <= limit {
					facts.Entries = append(facts.Entries, Entry{RelativePath: rel, IsDir: isDir})
				}
				if remainLayer > 1 && isDir && entry.Type()&os.ModeSymlink == 0 {
					nextLayer = append(nextLayer, full)
				}
			}
		}
		if remainLayer <= 1 {
			break
		}
		remainLayer--
		currentLayer = nextLayer
	}
	return facts, nil
}

// collectGitFact 收集 .git 特征——直读 HEAD + refs 文件；不可读按空处理。
func collectGitFact(rel, gitDir string) GitFact {
	fact := GitFact{RelativePath: rel}
	head, err := readTextShared(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return fact
	}
	head = strings.TrimSpace(head)
	if strings.HasPrefix(head, "ref:") {
		branch := strings.TrimSpace(head[4:])
		commit := ""
		if raw, refErr := readTextShared(filepath.Join(gitDir, filepath.FromSlash(branch))); refErr == nil {
			commit = strings.TrimSpace(raw)
		}
		if len(commit) > 7 {
			commit = commit[:7]
		}
		fact.Head = branch
		if commit != "" {
			fact.Head = fact.Head + " → " + commit
		}
		return fact
	}
	// detached HEAD——HEAD 即 commit
	if len(head) > 7 {
		head = head[:7]
	}
	fact.Head = head
	return fact
}

// readTextShared 读取 UTF-8 文本（剥 BOM）——git 元数据文件用。
func readTextShared(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		raw = raw[3:]
	}
	return string(raw), nil
}

// sortEntriesOrdinalIgnoreCase 目录项按名称 OrdinalIgnoreCase 排序（对齐 C# Array.Sort）。
func sortEntriesOrdinalIgnoreCase(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return compareOrdinalIgnoreCase(entries[i].Name(), entries[j].Name()) < 0
	})
}

// compareOrdinalIgnoreCase 逐字符大写比较——对应 StringComparer.OrdinalIgnoreCase。
func compareOrdinalIgnoreCase(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	limit := len(ra)
	if len(rb) < limit {
		limit = len(rb)
	}
	for i := 0; i < limit; i++ {
		ca := unicode.ToUpper(ra[i])
		cb := unicode.ToUpper(rb[i])
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
	}
	return len(ra) - len(rb)
}
