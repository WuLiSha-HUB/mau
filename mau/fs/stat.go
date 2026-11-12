package fs

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// StatFacts 路径状态事实——类型 + 大小 + 时间戳 + 目录递归统计（展示文案归积木层）。
type StatFacts struct {
	ResolvedPath   string    // 解析后的绝对路径
	IsDirectory    bool      // 是否目录（false = 文件）
	SizeBytes      int64     // 文件大小（字节；目录 = 0——目录大小须递归统计）
	LastWrite      time.Time // 最后修改时间
	CreationTime   time.Time // 创建时间
	Recursive      bool      // 是否已做递归统计
	TotalBytes     int64     // 递归合计字节
	FileCount      int       // 递归文件数
	DirectoryCount int       // 递归子目录数
	Notes          []string  // 统计注记——不可读子目录 / 跳过的重解析点（失败可见）
}

// StatFacts 路径状态——文件 / 目录（目录 recursive=true 时递归统计合计字节与计数）。
func (s *Service) StatFacts(path string, recursive bool) (StatFacts, error) {
	resolved, err := s.Resolve(path, false)
	if err != nil {
		return StatFacts{}, err
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		return StatFacts{}, &ToolError{Name: "FileNotFoundException", Msg: "Path was not found."}
	}
	facts := StatFacts{ResolvedPath: resolved}
	if info.IsDir() {
		facts.IsDirectory = true
		facts.LastWrite = info.ModTime()
		facts.CreationTime = creationTime(info)
		facts.Recursive = recursive
		if recursive {
			var notes []string
			collectStat(resolved, &facts, &notes)
			facts.Notes = notes
		}
		return facts, nil
	}
	facts.SizeBytes = info.Size()
	facts.LastWrite = info.ModTime()
	facts.CreationTime = creationTime(info)
	return facts, nil
}

// collectStat 递归统计目录内容——文件数 / 子目录数 / 合计字节（重解析点跳过 + 不可读目录记注记）。
func collectStat(directory string, facts *StatFacts, notes *[]string) {
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		// 不可读目录出声——否则「总字节」静默偏小（失败必须可见）
		*notes = append(*notes, "不可读 "+directory+"（"+errorName(readErr)+"）")
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, statErr := os.Stat(filepath.Join(directory, entry.Name()))
		if statErr != nil {
			*notes = append(*notes, "不可读 "+directory+"（"+errorName(statErr)+"）")
			continue
		}
		facts.FileCount++
		facts.TotalBytes += info.Size()
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		full := filepath.Join(directory, entry.Name())
		facts.DirectoryCount++
		if isReparsePoint(full) {
			// 重解析点不穿——与类边界语义一致（穿过去既可能越界也可能成环）
			*notes = append(*notes, "跳过重解析点 "+full)
			continue
		}
		collectStat(full, facts, notes)
	}
}

// errorName 取错误的 .NET 异常类名（与 C# 回执文案对齐）。
func errorName(err error) string {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Name
	}
	return "IOException"
}
