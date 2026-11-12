package fs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RecycleOutcome 软删除结果——回收站落点 + 被移动内容统计（目录=整棵子树合计；文件=单件）。
type RecycleOutcome struct {
	Target         string // 回收站内新路径（可恢复的唯一落点）
	IsDirectory    bool   // 是否为目录（整棵子树整体迁移）
	FileCount      int    // 文件数（目录=子树合计；文件=1）
	DirectoryCount int    // 子目录数（目录=子树合计；文件=0）
	TotalBytes     int64  // 合计字节数（目录=子树合计；文件=其长度）
}

// NewService 建服务——对齐 C# 构造契约：回收站落点须落在可写受控根内并预先建目录。
func NewService(roots []Root, recycleRoot, base string) (*Service, error) {
	svc := &Service{Roots: roots, Base: base}
	if strings.TrimSpace(recycleRoot) == "" {
		return nil, &ToolError{Name: "ArgumentException", Msg: "Recycle root is empty."}
	}
	resolved, err := svc.Resolve(recycleRoot, true)
	if err != nil {
		return nil, err
	}
	if mkErr := os.MkdirAll(resolved, 0o755); mkErr != nil {
		return nil, wrapIO(mkErr)
	}
	svc.RecycleRoot = resolved
	return svc, nil
}

// Recycle 软删除——文件或目录（含非空目录=整棵子树）移入受控回收站并统计被移动内容。
func (s *Service) Recycle(path string) (RecycleOutcome, error) {
	resolved, err := s.Resolve(path, true)
	if err != nil {
		return RecycleOutcome{}, err
	}
	trimmed := strings.TrimRight(resolved, `/\`)
	// [段1] 受控根本身不可回收——软删可恢复，但根被移走等于运行面整体失踪
	for _, r := range s.Roots {
		root := strings.TrimRight(normalizeRoot(r.Path), `/\`)
		if root != "" && strings.EqualFold(trimmed, root) {
			return RecycleOutcome{}, &ToolError{Name: "InvalidOperationException", Msg: "Refusing to recycle a controlled root: " + resolved}
		}
	}
	if s.RecycleRoot == "" {
		return RecycleOutcome{}, &ToolError{Name: "InvalidOperationException", Msg: "Recycle root is not configured."}
	}
	// [段2] 落点命名——UTC 时间戳 + GUID + 叶子名（同秒多次回收不冲突）
	leaf := filepath.Base(trimmed)
	if leaf == "" || leaf == "." || leaf == `\` || leaf == "/" {
		leaf = "root"
	}
	outcome := RecycleOutcome{
		Target: filepath.Join(s.RecycleRoot, recycleStamp(time.Now())+"-"+newGUIDHex()+"-"+leaf),
	}
	// [段3] 文件 / 目录双支持——统计先于迁移（迁移后原路径不可读）
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		return RecycleOutcome{}, &ToolError{Name: "FileNotFoundException", Msg: "Recycle target was not found."}
	}
	if info.Mode().IsRegular() {
		outcome.FileCount = 1
		outcome.TotalBytes = info.Size()
		if moveErr := os.Rename(resolved, outcome.Target); moveErr != nil {
			return RecycleOutcome{}, wrapIO(moveErr)
		}
		return outcome, nil
	}
	if !info.IsDir() {
		return RecycleOutcome{}, &ToolError{Name: "FileNotFoundException", Msg: "Recycle target was not found."}
	}
	outcome.IsDirectory = true
	if collectErr := collectRecycleStats(resolved, &outcome); collectErr != nil {
		return RecycleOutcome{}, collectErr
	}
	if moveErr := os.Rename(resolved, outcome.Target); moveErr != nil {
		return RecycleOutcome{}, wrapIO(moveErr)
	}
	return outcome, nil
}

// collectRecycleStats 回收统计采集——迁移前递归统计文件数 / 子目录数 / 字节数。
func collectRecycleStats(directory string, outcome *RecycleOutcome) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return wrapIO(err)
	}
	for _, entry := range entries {
		full := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			outcome.DirectoryCount++
			if subErr := collectRecycleStats(full, outcome); subErr != nil {
				return subErr
			}
			continue
		}
		info, statErr := os.Stat(full)
		if statErr != nil {
			return wrapIO(statErr)
		}
		outcome.FileCount++
		outcome.TotalBytes += info.Size()
	}
	return nil
}

// recycleStamp 回收落点时间戳——对齐 C# "yyyyMMddTHHmmssfffffffZ"（7 位小数、无小数点）。
func recycleStamp(now time.Time) string {
	utc := now.UTC()
	return utc.Format("20060102T150405") + fmt.Sprintf("%07d", utc.Nanosecond()/100) + "Z"
}

// newGUIDHex 生成 32 位小写十六进制随机串——对齐 Guid.NewGuid().ToString("N")。
func newGUIDHex() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// 随机源不可用——按时间填充，保住落点格式（内部机制，不改变可观测契约）
		now := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(now >> ((i % 8) * 8))
		}
	}
	return hex.EncodeToString(buf[:])
}
