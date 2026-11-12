package fs

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiffEntry 差异条目事实——相对路径 + 差异形态（only-left / only-right / changed）。
type DiffEntry struct {
	RelativePath string // 相对对比根的路径（'/' 分隔）
	Status       string // 差异形态
}

// DiffFacts 对比事实——同异判定 + 差异条目 + 统计口径（展示文案归积木层）。
type DiffFacts struct {
	LeftPath        string      // 左路径（解析后的绝对路径）
	RightPath       string      // 右路径（解析后的绝对路径）
	BothFiles       bool        // 是否文件模式（false = 目录模式）
	Same            bool        // 是否完全相同
	LeftSizeBytes   int64       // 左文件大小（目录模式 = 0）
	RightSizeBytes  int64       // 右文件大小（目录模式 = 0）
	FirstDiffOffset int64       // 文件模式首个不同字节偏移（0 起；-1 = 全同 / 不适用）
	Entries         []DiffEntry // 差异条目（≤ limit）
	SameCount       int         // 相同文件数（目录模式）
	DiffTotal       int         // 差异总数（未截断口径）
	Total           int         // 参与比较的文件总数（目录模式）
	IgnoredDirs     int         // 被忽略的目录数
	ContentCompared bool        // 是否做了逐字节比对
	Notes           []string    // 对比注记——不可读目录 / 跳过的重解析点
}

// DiffFacts 对比两个文件或两个目录——文件模式逐字节；目录模式按路径集合 + 大小（content=true 时同大小对再逐字节）。
func (s *Service) DiffFacts(left, right string, content bool, limit int) (DiffFacts, error) {
	resolvedLeft, err := s.Resolve(left, false)
	if err != nil {
		return DiffFacts{}, err
	}
	resolvedRight, err := s.Resolve(right, false)
	if err != nil {
		return DiffFacts{}, err
	}
	leftInfo, leftErr := os.Stat(resolvedLeft)
	if leftErr != nil {
		return DiffFacts{}, &ToolError{Name: "FileNotFoundException", Msg: "Left path was not found."}
	}
	rightInfo, rightErr := os.Stat(resolvedRight)
	if rightErr != nil {
		return DiffFacts{}, &ToolError{Name: "FileNotFoundException", Msg: "Right path was not found."}
	}
	if leftInfo.IsDir() != rightInfo.IsDir() {
		return DiffFacts{}, &ToolError{Name: "InvalidDataException", Msg: "Diff requires the same kind on both sides (left=" + kindName(leftInfo.IsDir()) + ", right=" + kindName(rightInfo.IsDir()) + ")."}
	}
	// [文件模式] 单条读通道同时给出同异与首个不同偏移
	if !leftInfo.IsDir() {
		facts := DiffFacts{
			LeftPath:        resolvedLeft,
			RightPath:       resolvedRight,
			BothFiles:       true,
			Total:           1,
			ContentCompared: true,
			LeftSizeBytes:   leftInfo.Size(),
			RightSizeBytes:  rightInfo.Size(),
		}
		same, firstDiff, cmpErr := compareBytes(resolvedLeft, resolvedRight)
		if cmpErr != nil {
			return DiffFacts{}, cmpErr
		}
		facts.Same = same
		facts.FirstDiffOffset = firstDiff
		if same {
			facts.SameCount = 1
		} else {
			facts.DiffTotal = 1
		}
		return facts, nil
	}
	// [目录模式] 相对路径集合 + 大小；content=true 时同大小对逐字节
	if limit < 0 {
		return DiffFacts{}, &ToolError{Name: "OverflowException", Msg: "Arithmetic operation resulted in an overflow."}
	}
	leftMap := make(map[string]relFile)
	rightMap := make(map[string]relFile)
	ignored := 0
	var notes []string
	collectRelFiles(resolvedLeft, resolvedLeft, leftMap, &ignored, &notes)
	collectRelFiles(resolvedRight, resolvedRight, rightMap, &ignored, &notes)
	facts := DiffFacts{
		LeftPath:        resolvedLeft,
		RightPath:       resolvedRight,
		ContentCompared: content,
		IgnoredDirs:     ignored,
		Notes:           notes,
	}
	var diffs []DiffEntry
	same := 0
	for key, leftFile := range leftMap {
		rightFile, exists := rightMap[key]
		if !exists {
			diffs = append(diffs, DiffEntry{RelativePath: leftFile.relative, Status: "only-left"})
			continue
		}
		if rightFile.size != leftFile.size {
			diffs = append(diffs, DiffEntry{RelativePath: leftFile.relative, Status: "changed"})
			continue
		}
		if content {
			leftPath := filepath.Join(resolvedLeft, filepath.FromSlash(leftFile.relative))
			rightPath := filepath.Join(resolvedRight, filepath.FromSlash(rightFile.relative))
			sameFile, _, cmpErr := compareBytes(leftPath, rightPath)
			if cmpErr != nil {
				return DiffFacts{}, cmpErr
			}
			if !sameFile {
				diffs = append(diffs, DiffEntry{RelativePath: leftFile.relative, Status: "changed"})
				continue
			}
		}
		same++
	}
	onlyRight := 0
	for key, rightFile := range rightMap {
		if _, exists := leftMap[key]; !exists {
			onlyRight++
			diffs = append(diffs, DiffEntry{RelativePath: rightFile.relative, Status: "only-right"})
		}
	}
	sort.Slice(diffs, func(i, j int) bool {
		return strings.Compare(diffs[i].RelativePath, diffs[j].RelativePath) < 0
	})
	facts.SameCount = same
	facts.Total = len(leftMap) + onlyRight
	facts.DiffTotal = len(diffs)
	facts.Same = len(diffs) == 0
	if len(diffs) <= limit {
		facts.Entries = diffs
	} else {
		facts.Entries = diffs[:limit]
	}
	return facts, nil
}

// relFile 相对路径 + 大小（键为小写归一——对齐 C# OrdinalIgnoreCase 字典）。
type relFile struct {
	relative string
	size     int64
}

// collectRelFiles 收集目录内全部文件的相对路径 → 大小（忽略目录跳过并计数；重解析点不穿）。
func collectRelFiles(root, current string, files map[string]relFile, ignored *int, notes *[]string) {
	entries, readErr := os.ReadDir(current)
	if readErr != nil {
		*notes = append(*notes, "不可读 "+current+"（"+errorName(readErr)+"）")
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		full := filepath.Join(current, entry.Name())
		info, statErr := os.Stat(full)
		if statErr != nil {
			*notes = append(*notes, "不可读 "+current+"（"+errorName(statErr)+"）")
			continue
		}
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			rel = full
		}
		slashed := filepath.ToSlash(rel)
		files[strings.ToLower(slashed)] = relFile{relative: slashed, size: info.Size()}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if isIgnoredDir(entry.Name()) {
			*ignored = *ignored + 1
			continue
		}
		full := filepath.Join(current, entry.Name())
		if isReparsePoint(full) {
			*notes = append(*notes, "跳过重解析点 "+full)
			continue
		}
		collectRelFiles(root, full, files, ignored, notes)
	}
}

// compareBytes 逐字节比对两个文件——同异 + 首个不同偏移（分块读，共享读打开）。
func compareBytes(left, right string) (bool, int64, error) {
	a, openAErr := os.Open(left)
	if openAErr != nil {
		return false, 0, wrapIO(openAErr)
	}
	defer a.Close()
	b, openBErr := os.Open(right)
	if openBErr != nil {
		return false, 0, wrapIO(openBErr)
	}
	defer b.Close()
	bufA := make([]byte, 81920)
	bufB := make([]byte, 81920)
	var pos int64
	for {
		na, readAErr := readChunk(a, bufA)
		if readAErr != nil {
			return false, 0, wrapIO(readAErr)
		}
		nb, readBErr := readChunk(b, bufB)
		if readBErr != nil {
			return false, 0, wrapIO(readBErr)
		}
		if na == 0 && nb == 0 {
			return true, -1, nil
		}
		n := na
		if nb < n {
			n = nb
		}
		for i := 0; i < n; i++ {
			if bufA[i] != bufB[i] {
				return false, pos + int64(i), nil
			}
		}
		if na != nb {
			return false, pos + int64(n), nil
		}
		pos += int64(n)
	}
}

// readChunk 读满缓冲区——Read 可能短读，循环补满。
func readChunk(f *os.File, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		n, err := f.Read(buffer[total:])
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// kindName 类型名——错误文案用（directory / file）。
func kindName(isDir bool) string {
	if isDir {
		return "directory"
	}
	return "file"
}
