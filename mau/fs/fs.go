// Package fs 复现 Mau.Runtime 的文件系统服务面：受控根寻址、编码契约、文本读写。
//
// 对齐 C# 版 RootPathResolver / TextFileCodec / FileSystemService（P2-a 文本面）。
// 错误面用 ToolError 承载 .NET 异常类名——积木层拼成 ERR|<类名>|<消息>。
package fs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// Root 是一个受控根——id 小写归一，path 为绝对路径，Writable 决定写面是否放行。
type Root struct {
	ID       string
	Path     string
	Writable bool
}

// Service 是文件系统服务——持受控根表、相对路径基准与回收站落点。
type Service struct {
	Roots       []Root
	Base        string // 相对路径基准（空 = 首根）
	RecycleRoot string // 软删除回收目录（已解析绝对路径；空 = 未配置）
}

// ToolError 是对外错误——Name 用 .NET 异常类名，与 C# 回执逐字对齐。
type ToolError struct {
	Name string
	Msg  string
}

// Error 实现 error。
func (e *ToolError) Error() string { return e.Msg }

// ErrText 把错误渲染成 ERR|<类名>|<消息>。
func ErrText(err error) string {
	var te *ToolError
	if errors.As(err, &te) {
		return "ERR|" + te.Name + "|" + te.Msg
	}
	return "ERR|Exception|" + err.Error()
}

// 根寻址错误码——与 C# RootPathResolver 同码。
const (
	ErrUnknownID  = "ROOT_UNKNOWN_ID"
	ErrOutOfScope = "ROOT_OUT_OF_SCOPE"
	ErrBadPath    = "ROOT_BAD_PATH"
)

// Resolve 解析路径并校验归属与写权限。
func (s *Service) Resolve(path string, forWrite bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", &ToolError{Name: "ArgumentException", Msg: "Path is empty."}
	}
	full, root, code, msg, ok := s.resolveOwned(path)
	if !ok {
		return "", &ToolError{Name: "UnauthorizedAccessException", Msg: "Path resolve failed: " + code + " | " + msg}
	}
	if forWrite && !root.Writable {
		return "", &ToolError{Name: "UnauthorizedAccessException", Msg: "Path is in a read-only root: " + root.Path}
	}
	return full, nil
}

// resolveOwned 展开 + 归属判定——最长前缀匹配（带分隔符边界）。
func (s *Service) resolveOwned(path string) (full string, root Root, code, msg string, ok bool) {
	full, root, code, msg, ok = s.expand(path)
	if !ok || full == "" {
		return full, root, code, msg, ok
	}
	owner, found := s.findOwning(full)
	if !found {
		return full, Root{}, ErrOutOfScope, "路径超出受控根范围：" + full, false
	}
	return full, owner, "", "", true
}

// ResolveOwnedRoot 展开 + 归属判定（C# RootPathResolver.ResolveOwned 同规）——basePath 空则取首根。
//
// 供不持有 Service 的调用方复用同一解析单点（如 git 服务按进程工作目录作相对基准）；
// 不判 writable、不判存在性——权限与安全判定归调用方。
func ResolveOwnedRoot(path string, roots []Root, basePath string) (full string, root Root, code, msg string, ok bool) {
	return (&Service{Roots: roots, Base: basePath}).resolveOwned(path)
}

// expand 形态判定 + 根 id 映射 + 绝对化。
func (s *Service) expand(path string) (full string, root Root, code, msg string, ok bool) {
	if path == "" {
		return "", Root{}, "", "", true
	}
	if sep := strings.Index(path, ":"); sep > 0 {
		if hit, found := s.findByID(path[:sep]); found {
			abs, err := filepath.Abs(filepath.Join(hit.Path, path[sep+1:]))
			if err != nil {
				return "", Root{}, ErrBadPath, err.Error(), false
			}
			return abs, hit, "", "", true
		}
		if isRootAddress(path) {
			return "", Root{}, ErrUnknownID, "未知受控根 id: " + path[:sep], false
		}
	} else if sep <= 0 && len(s.Roots) > 0 {
		if bare, found := s.findByID(path); found {
			p := normalizeRoot(bare.Path)
			return p, bare, "", "", true
		}
	}
	// 普通路径——按基准绝对化
	basis := s.Base
	if basis == "" && len(s.Roots) > 0 {
		basis = normalizeRoot(s.Roots[0].Path)
	}
	var abs string
	var err error
	if filepath.IsAbs(path) || basis == "" {
		abs, err = filepath.Abs(path)
	} else {
		abs, err = filepath.Abs(filepath.Join(basis, path))
	}
	if err != nil {
		return "", Root{}, ErrBadPath, err.Error(), false
	}
	return abs, Root{}, "", "", true
}

// findByID 按 id 查根（大小写不敏感）。
func (s *Service) findByID(id string) (Root, bool) {
	for _, r := range s.Roots {
		if r.ID != "" && strings.EqualFold(r.ID, id) {
			return r, true
		}
	}
	return Root{}, false
}

// findOwning 归属根匹配——最长前缀 + 分隔符边界（Windows 大小写不敏感）。
//
// 卷根（`D:\`）自身以分隔符结尾：拼前缀时不再补第二个分隔符，否则
// `D:\` 的根永远匹配不到 `D:\x`（判例：deps 探测以卷根构造临时根表，全部判越界）。
func (s *Service) findOwning(full string) (Root, bool) {
	best := Root{}
	bestLen := -1
	for _, r := range s.Roots {
		root := normalizeRoot(r.Path)
		if root == "" {
			continue
		}
		prefix := strings.ToLower(root)
		if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
			prefix += string(os.PathSeparator)
		}
		inside := strings.EqualFold(full, root) || strings.HasPrefix(strings.ToLower(full), prefix)
		if inside && len(root) > bestLen {
			best = r
			bestLen = len(root)
		}
	}
	return best, bestLen >= 0
}

// isRootAddress 根寻址形态判定——冒号在首个路径分隔符之前（盘符不算）。
func isRootAddress(path string) bool {
	colon := strings.Index(path, ":")
	if colon <= 0 {
		return false
	}
	sep := -1
	if i := strings.IndexAny(path, "/\\"); i >= 0 {
		sep = i
	}
	if sep >= 0 && sep < colon {
		return false
	}
	if colon == 1 && sep == 2 {
		return false
	}
	return true
}

// normalizeRoot 根路径规范化——绝对化 + 去尾分隔符（保卷根）。
func normalizeRoot(root string) string {
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	if filepath.Clean(abs) == filepath.VolumeName(abs)+string(os.PathSeparator) {
		return abs
	}
	return strings.TrimRight(abs, "/\\")
}

// ReadTextAuto 自动编码读取——BOM 优先，无 BOM 按类型契约。
func (s *Service) ReadTextAuto(path string) (string, error) {
	resolved, err := s.Resolve(path, false)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return "", wrapIO(err)
	}
	text, err := decode(path, raw)
	if err != nil {
		return "", err
	}
	return text, nil
}

// WriteTextAuto 自动编码覆写——既有文件保真 BOM/换行，新建按类型契约；返回风格描述。
func (s *Service) WriteTextAuto(path, content string) (string, error) {
	resolved, err := s.Resolve(path, true)
	if err != nil {
		return "", err
	}
	bom, newline, err := resolveWriteStyle(resolved, path)
	if err != nil {
		return "", err
	}
	body := normalizeNewlines(safeText(content), newline)
	if err := writeAtomic(resolved, body, path, bom); err != nil {
		return "", err
	}
	return describeStyle(path, bom, newline), nil
}

// AppendTextAuto 自动编码追加——既有文件保真，新建按契约；返回风格描述。
func (s *Service) AppendTextAuto(path, content string) (string, error) {
	resolved, err := s.Resolve(path, true)
	if err != nil {
		return "", err
	}
	bom, newline, err := resolveWriteStyle(resolved, path)
	if err != nil {
		return "", err
	}
	body := normalizeNewlines(safeText(content), newline)
	if dir := filepath.Dir(resolved); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", wrapIO(err)
		}
	}
	f, err := os.OpenFile(resolved, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", wrapIO(err)
	}
	defer f.Close()
	if err := writeBytes(f, body, path, bom); err != nil {
		return "", err
	}
	return describeStyle(path, bom, newline), nil
}

// ReadLinesAuto 行号区间读取——1 起，end=0 表示文件尾；输出 "N: 行" 拼接。
func (s *Service) ReadLinesAuto(path string, startLine, endLine int) (string, error) {
	if startLine < 1 || endLine < 0 {
		return "", &ToolError{Name: "ArgumentOutOfRangeException", Msg: "startLine"}
	}
	resolved, err := s.Resolve(path, false)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return "", wrapIO(err)
	}
	text, err := decode(path, raw)
	if err != nil {
		return "", err
	}
	lines := splitLines(text)
	end := endLine
	if end == 0 || end > len(lines) {
		end = len(lines)
	}
	if startLine > end && len(lines) > 0 {
		return "", &ToolError{Name: "ArgumentOutOfRangeException", Msg: "startLine"}
	}
	var b strings.Builder
	for line := startLine; line <= end; line++ {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d: %s", line, lines[line-1])
	}
	return b.String(), nil
}

// ReadTextFull 读全文并归一换行——供 read_between 类工具做锚点匹配。
func (s *Service) ReadTextFull(path string) (string, error) {
	text, err := s.ReadTextAuto(path)
	if err != nil {
		return "", err
	}
	return normalizeNewlines(text, "\n"), nil
}

// splitLines 按 \n 分行并去尾部 \r。
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// decode 读侧解码——BOM 优先 UTF-8，无 BOM 按类型契约（.bat=GBK）。
func decode(path string, raw []byte) (string, error) {
	if detectBom(raw) {
		return string(raw[3:]), nil
	}
	if profileIsGBK(path) {
		out, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		if err != nil {
			return "", &ToolError{Name: "ArgumentException", Msg: "GBK 解码失败: " + err.Error()}
		}
		return string(out), nil
	}
	if !utf8.Valid(raw) {
		return string(raw), nil
	}
	return string(raw), nil
}

// resolveWriteStyle 写侧风格——既有文件保真，新建按契约。
func resolveWriteStyle(resolved, path string) (bom bool, newline string, err error) {
	if _, statErr := os.Stat(resolved); statErr == nil {
		raw, readErr := os.ReadFile(resolved)
		if readErr != nil {
			return false, "", wrapIO(readErr)
		}
		bom = detectBom(raw)
		for _, b := range raw {
			if b == '\n' {
				return bom, detectNewline(raw), nil
			}
		}
		return bom, defaultNewline(path), nil
	}
	return defaultBom(path), defaultNewline(path), nil
}

// writeAtomic 原子写——tmp + rename（保父目录）。
func writeAtomic(resolved, body, path string, bom bool) error {
	dir := filepath.Dir(resolved)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return wrapIO(err)
		}
	}
	tmp := resolved + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return wrapIO(err)
	}
	if err := writeBytes(f, body, path, bom); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return wrapIO(err)
	}
	if err := os.Rename(tmp, resolved); err != nil {
		os.Remove(tmp)
		return wrapIO(err)
	}
	return nil
}

// writeBytes 按编码族写入——UTF-8 可带 BOM；GBK 直写。
func writeBytes(f *os.File, body, path string, bom bool) error {
	if profileIsGBK(path) {
		out, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(body))
		if err != nil {
			return &ToolError{Name: "ArgumentException", Msg: "GBK 编码失败: " + err.Error()}
		}
		_, err = f.Write(out)
		if err != nil {
			return wrapIO(err)
		}
		return nil
	}
	data := []byte(body)
	if bom {
		data = append([]byte{0xEF, 0xBB, 0xBF}, data...)
	}
	if _, err := f.Write(data); err != nil {
		return wrapIO(err)
	}
	return nil
}

// safeText 去掉字符串里不适合落盘的孤立代理项（C# SafeText 的 Go 等价：剔 U+FFFD 前的非法 UTF-8）。
func safeText(s string) string {
	return strings.ToValidUTF8(s, "")
}

// wrapIO 把 IO 错误映射为 .NET 异常名。
func wrapIO(err error) error {
	if os.IsNotExist(err) {
		return &ToolError{Name: "FileNotFoundException", Msg: err.Error()}
	}
	if os.IsPermission(err) {
		return &ToolError{Name: "UnauthorizedAccessException", Msg: err.Error()}
	}
	return &ToolError{Name: "IOException", Msg: err.Error()}
}

// profileIsGBK 基础编码族判定——.bat 走 GBK(936)，其余 UTF-8。
func profileIsGBK(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".bat")
}

// defaultNewline 新建文件默认换行——代码族 / Web 资产族 / .bat 走 CRLF。
func defaultNewline(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".cs", ".csproj", ".mau", ".mauproj", ".html", ".js", ".css", ".bat":
		return "\r\n"
	}
	return "\n"
}

// defaultBom 新建文件默认 BOM——代码族与 .md/.ps1 写 BOM。
func defaultBom(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".cs", ".csproj", ".mau", ".mauproj", ".md", ".ps1":
		return true
	}
	return false
}

// detectBom 首三字节 EF BB BF。
func detectBom(raw []byte) bool {
	return len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF
}

// detectNewline 首个 CRLF 早于独立 LF 判 CRLF。
func detectNewline(raw []byte) string {
	firstLF := -1
	for i, b := range raw {
		if b == '\n' {
			if i > 0 && raw[i-1] == '\r' {
				continue
			}
			firstLF = i
			break
		}
	}
	firstCRLF := bytes.Index(raw, []byte("\r\n"))
	if firstCRLF >= 0 && (firstLF < 0 || firstCRLF <= firstLF) {
		return "\r\n"
	}
	return "\n"
}

// normalizeNewlines 换行归一。
func normalizeNewlines(content, newline string) string {
	unified := strings.ReplaceAll(content, "\r\n", "\n")
	if newline == "\r\n" {
		return strings.ReplaceAll(unified, "\n", "\r\n")
	}
	return unified
}

// describeStyle 写入风格描述——「编码 / 换行」。
func describeStyle(path string, bom bool, newline string) string {
	enc := "UTF-8"
	if profileIsGBK(path) {
		enc = "GBK(936)"
	} else if bom {
		enc = "UTF-8 BOM"
	}
	nl := "LF"
	if newline == "\r\n" {
		nl = "CRLF"
	}
	return enc + " / " + nl
}

// encodingOf 供测试断言编码族（避免测试直接依赖内部函数）。
func encodingOf(path string) encoding.Encoding {
	if profileIsGBK(path) {
		return simplifiedchinese.GBK
	}
	return encoding.Nop
}
