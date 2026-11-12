package fs

import (
	"os"
	"time"
)

// VersionFacts PE 版本事实——版本三件 + 元数据（展示文案与回落注记归积木层）。
type VersionFacts struct {
	ResolvedPath   string    // 解析后的绝对路径
	Version        string    // 版本（ProductVersion 优先，空则回落 FileVersion）
	FallbackUsed   bool      // 是否发生了回落（ProductVersion 为空 → 用 FileVersion）
	FileVersion    string    // FileVersion（可能为空串——「(无)」形态归积木）
	ProductVersion string    // ProductVersion（可能为空串——「(无)」形态归积木）
	LastWrite      time.Time // 最后修改时间
	SizeBytes      int64     // 文件大小（字节）
}

// VersionFacts 读取 PE 文件版本事实——MZ 判定 + 版本三件（Product 优先，空则回落 File）+ 零值占位归一。
func (s *Service) VersionFacts(path string) (VersionFacts, error) {
	resolved, err := s.Resolve(path, false)
	if err != nil {
		return VersionFacts{}, err
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil || info.IsDir() {
		return VersionFacts{}, &ToolError{Name: "FileNotFoundException", Msg: "File was not found."}
	}
	// [段1] PE 判定——首两字节 "MZ"（非 PE 直接报错，不让空版本伪装成正常）
	if !hasMZMagic(resolved) {
		return VersionFacts{}, &ToolError{Name: "InvalidDataException", Msg: "Not a PE file (no version resource)."}
	}
	// [段2] 版本三件——StringFileInfo 字符串优先，缺失回落 VS_FIXEDFILEINFO 数值版本（.NET FileVersionInfo 语义）
	data, readErr := readVersionResource(resolved)
	if readErr != nil {
		return VersionFacts{}, wrapIO(readErr)
	}
	product := data.productVersion
	if product == "" {
		product = data.fixedProduct
	}
	file := data.fileVersion
	if file == "" {
		file = data.fixedFile
	}
	// [段3] 空值 / 全零占位归一——0.0.0.0 是 csc 直编产物的占位形态，不算版本事实
	if isZeroVersionText(product) {
		product = ""
	}
	if isZeroVersionText(file) {
		file = ""
	}
	// [段4] 读不出——按资源存在与否区分错误文案（失败可见，不伪造版本）
	if product == "" && file == "" {
		if data.hasResource {
			return VersionFacts{}, &ToolError{Name: "InvalidDataException", Msg: "PE file has an RT_VERSION resource but no readable version facts (ProductVersion/FileVersion empty or zero placeholder, VS_FIXEDFILEINFO all-zero)."}
		}
		return VersionFacts{}, &ToolError{Name: "InvalidDataException", Msg: "PE file has no version resource (no RT_VERSION entry)."}
	}
	facts := VersionFacts{
		ResolvedPath:   resolved,
		FileVersion:    file,
		ProductVersion: product,
		LastWrite:      info.ModTime(),
		SizeBytes:      info.Size(),
	}
	if product != "" {
		facts.Version = product
	} else {
		facts.Version = file
		facts.FallbackUsed = true
	}
	return facts, nil
}

// hasMZMagic 读首两字节判 PE 魔数（"MZ"）。
func hasMZMagic(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [2]byte
	if _, readErr := f.Read(head[:]); readErr != nil {
		return false
	}
	return head[0] == 'M' && head[1] == 'Z'
}

// isZeroVersionText 判定「全零占位版本」（空串 / 纯空白 / 0 / 0.0 / 0.0.0.0）。
func isZeroVersionText(text string) bool {
	if text == "" {
		return true
	}
	for _, r := range text {
		switch r {
		case '.', ' ', '\t', '0':
			continue
		default:
			return false
		}
	}
	return true
}
