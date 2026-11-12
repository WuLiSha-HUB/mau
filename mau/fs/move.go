package fs

import (
	"os"
	"path/filepath"
)

// Move 移动 / 重命名——文件与目录均支持（目录=整棵子树）；自动创建目标父目录；目标已存在拒绝。
func (s *Service) Move(source, destination string) error {
	resolvedSource, err := s.Resolve(source, true)
	if err != nil {
		return err
	}
	resolvedDestination, err := s.Resolve(destination, true)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(resolvedDestination); statErr == nil {
		return &ToolError{Name: "IOException", Msg: "Move destination already exists."}
	}
	parent := filepath.Dir(resolvedDestination)
	if parent == "" {
		return &ToolError{Name: "InvalidOperationException", Msg: "Parent directory is unavailable."}
	}
	if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
		return wrapIO(mkErr)
	}
	info, statErr := os.Stat(resolvedSource)
	if statErr != nil {
		return &ToolError{Name: "FileNotFoundException", Msg: "Move source was not found."}
	}
	if info.IsDir() || info.Mode().IsRegular() {
		if renameErr := os.Rename(resolvedSource, resolvedDestination); renameErr != nil {
			return wrapIO(renameErr)
		}
		return nil
	}
	return &ToolError{Name: "FileNotFoundException", Msg: "Move source was not found."}
}
