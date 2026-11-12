package fs

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"
)

// HashFacts 文件哈希事实——算法 + 摘要 + 元数据（展示文案归积木层）。
type HashFacts struct {
	ResolvedPath string // 解析后的绝对路径
	Algorithm    string // 算法名（现行固定 SHA256）
	Hash         string // 摘要（大写十六进制）
	SizeBytes    int64  // 文件大小（字节）
	ElapsedMs    int64  // 读取耗时（毫秒）
}

// HashFacts 计算文件 SHA256——目录 / 缺失明确报错；共享读（正在被写入的活跃文件同样可算）。
func (s *Service) HashFacts(path string) (HashFacts, error) {
	resolved, err := s.Resolve(path, false)
	if err != nil {
		return HashFacts{}, err
	}
	info, statErr := os.Stat(resolved)
	if statErr == nil && info.IsDir() {
		return HashFacts{}, &ToolError{Name: "InvalidDataException", Msg: "Path is a directory (hash requires a file)."}
	}
	if statErr != nil {
		return HashFacts{}, &ToolError{Name: "FileNotFoundException", Msg: "File was not found."}
	}
	facts := HashFacts{ResolvedPath: resolved, Algorithm: "SHA256", SizeBytes: info.Size()}
	start := time.Now()
	f, openErr := os.Open(resolved)
	if openErr != nil {
		return HashFacts{}, wrapIO(openErr)
	}
	defer f.Close()
	hasher := sha256.New()
	if _, copyErr := io.Copy(hasher, f); copyErr != nil {
		return HashFacts{}, wrapIO(copyErr)
	}
	facts.Hash = strings.ToUpper(hex.EncodeToString(hasher.Sum(nil)))
	facts.ElapsedMs = time.Since(start).Milliseconds()
	return facts, nil
}
