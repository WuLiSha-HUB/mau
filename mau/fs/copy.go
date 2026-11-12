package fs

import (
	"io"
	"os"
	"path/filepath"
	"time"
)

// Copy 复制文件或目录（含非空目录=整棵子树）——自动创建目标父目录；目标已存在拒绝。
// 源按只读语义解析（只读根也可作为源），目标按写语义解析。
func (s *Service) Copy(source, destination string) error {
	resolvedSource, err := s.Resolve(source, false)
	if err != nil {
		return err
	}
	resolvedDestination, err := s.Resolve(destination, true)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(resolvedDestination); statErr == nil {
		return &ToolError{Name: "IOException", Msg: "Copy destination already exists."}
	}
	info, statErr := os.Stat(resolvedSource)
	if statErr != nil {
		return &ToolError{Name: "FileNotFoundException", Msg: "Copy source was not found."}
	}
	if info.IsDir() {
		return copyDirectory(resolvedSource, resolvedDestination)
	}
	parent := filepath.Dir(resolvedDestination)
	if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
		return wrapIO(mkErr)
	}
	return copyFile(resolvedSource, resolvedDestination)
}

// copyDirectory 递归复制目录子树——重解析点（符号链接 / junction）跳过（与类边界语义一致）。
func copyDirectory(source, destination string) error {
	if mkErr := os.MkdirAll(destination, 0o755); mkErr != nil {
		return wrapIO(mkErr)
	}
	entries, readErr := os.ReadDir(source)
	if readErr != nil {
		return wrapIO(readErr)
	}
	// [段1] 文件先复制（对齐 C# EnumerateFiles → EnumerateDirectories 顺序）
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		full := filepath.Join(source, entry.Name())
		if isReparsePoint(full) {
			continue
		}
		if copyErr := copyFile(full, filepath.Join(destination, entry.Name())); copyErr != nil {
			return copyErr
		}
	}
	// [段2] 子目录递归
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		full := filepath.Join(source, entry.Name())
		if isReparsePoint(full) {
			continue
		}
		if copyErr := copyDirectory(full, filepath.Join(destination, entry.Name())); copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// copyFile 单文件复制——保留源权限位与修改时间（对齐 File.Copy）。
func copyFile(source, destination string) error {
	info, statErr := os.Stat(source)
	if statErr != nil {
		return wrapIO(statErr)
	}
	in, openErr := os.Open(source)
	if openErr != nil {
		return wrapIO(openErr)
	}
	defer in.Close()
	out, createErr := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if createErr != nil {
		return wrapIO(createErr)
	}
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		out.Close()
		return wrapIO(copyErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return wrapIO(closeErr)
	}
	// File.Copy 保留最后修改时间——回执不涉及，字节目录比对（file-diff）依赖
	if chtErr := os.Chtimes(destination, time.Now(), info.ModTime()); chtErr != nil {
		return wrapIO(chtErr)
	}
	return nil
}

// isReparsePoint 重解析点判定——符号链接 / junction 近似（C# FileAttributes.ReparsePoint）。
func isReparsePoint(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
