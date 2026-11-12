package fs

import (
	"encoding/binary"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// versionResourceData 版本资源解析结果——StringFileInfo 字符串 + VS_FIXEDFILEINFO 数值版本。
type versionResourceData struct {
	fileVersion    string // StringFileInfo\FileVersion（原始字符串）
	productVersion string // StringFileInfo\ProductVersion（原始字符串）
	fixedFile      string // VS_FIXEDFILEINFO 数值版本 a.b.c.d（全零为空串）
	fixedProduct   string // VS_FIXEDFILEINFO 产品版本 a.b.c.d（全零为空串）
	hasResource    bool   // 是否存在 RT_VERSION（类型 16）资源条目
}

// peSection PE 节表条目（只保留 RVA 转换所需字段）。
type peSection struct {
	virtualAddr uint32 // VirtualAddress
	virtualSize uint32 // VirtualSize
	rawSize     uint32 // SizeOfRawData
	rawPtr      uint32 // PointerToRawData
}

// readVersionResource 解析 PE 资源目录中的 RT_VERSION 数据并提取版本事实。
// 结构不可读时按「无版本资源」返回（MZ 判定归业务层）；hasResource 只表达「存在类型 16 条目」。
func readVersionResource(path string) (versionResourceData, error) {
	var out versionResourceData
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	// [段1] DOS 头 → PE 头 → 节表
	var dos [64]byte
	if _, err := f.ReadAt(dos[:], 0); err != nil {
		return out, nil
	}
	peOff := int64(binary.LittleEndian.Uint32(dos[0x3C:0x40]))
	var pe [24]byte
	if _, err := f.ReadAt(pe[:], peOff); err != nil || pe[0] != 'P' || pe[1] != 'E' || pe[2] != 0 || pe[3] != 0 {
		return out, nil
	}
	numSections := int(binary.LittleEndian.Uint16(pe[6:8]))
	optSize := int64(binary.LittleEndian.Uint16(pe[20:22]))
	optOff := peOff + 24
	var magicBuf [2]byte
	if _, err := f.ReadAt(magicBuf[:], optOff); err != nil {
		return out, nil
	}
	ddBase := int64(96)
	if binary.LittleEndian.Uint16(magicBuf[:]) == 0x20B {
		ddBase = 112 // PE32+
	}
	var resDir [8]byte
	if _, err := f.ReadAt(resDir[:], optOff+ddBase+2*8); err != nil {
		return out, nil
	}
	resRVA := binary.LittleEndian.Uint32(resDir[0:4])
	if resRVA == 0 {
		return out, nil
	}
	sections := make([]peSection, 0, numSections)
	secBase := optOff + optSize
	for i := 0; i < numSections; i++ {
		var sh [40]byte
		if _, err := f.ReadAt(sh[:], secBase+int64(i*40)); err != nil {
			return out, nil
		}
		sections = append(sections, peSection{
			virtualAddr: binary.LittleEndian.Uint32(sh[12:16]),
			virtualSize: binary.LittleEndian.Uint32(sh[8:12]),
			rawSize:     binary.LittleEndian.Uint32(sh[16:20]),
			rawPtr:      binary.LittleEndian.Uint32(sh[20:24]),
		})
	}
	rvaToOff := func(rva uint32) (int64, bool) {
		sec, ok := findSection(sections, rva)
		if !ok {
			return 0, false
		}
		return int64(rva-sec.virtualAddr) + int64(sec.rawPtr), true
	}
	// [段2] 资源节数据——只读承载资源目录的节（VS_VERSIONINFO 通常远小于节大小）
	resSec, ok := findSection(sections, resRVA)
	if !ok {
		return out, nil
	}
	resOff := int64(resRVA-resSec.virtualAddr) + int64(resSec.rawPtr)
	dataLen := int64(resSec.rawSize)
	if dataLen <= 0 {
		dataLen = int64(resSec.virtualSize)
	}
	if stat, statErr := f.Stat(); statErr == nil {
		if remain := stat.Size() - resOff; remain < dataLen {
			dataLen = remain
		}
	}
	if dataLen <= 0 {
		return out, nil
	}
	data := make([]byte, dataLen)
	if _, err := f.ReadAt(data, resOff); err != nil && err != io.EOF {
		return out, err
	}
	// [段3] 资源目录三层遍历——Type → Name → Language；取第一个 RT_VERSION 数据块
	var versionChunk []byte
	types, ok := parseResourceDir(data, 0)
	if !ok {
		return out, nil
	}
	for _, typeEntry := range types {
		if !typeEntry.isDir || typeEntry.id != 16 {
			continue
		}
		out.hasResource = true
		names, nameOK := parseResourceDir(data, typeEntry.offset)
		if !nameOK {
			continue
		}
		for _, nameEntry := range names {
			if !nameEntry.isDir {
				continue
			}
			langs, langOK := parseResourceDir(data, nameEntry.offset)
			if !langOK {
				continue
			}
			for _, langEntry := range langs {
				if langEntry.isDir {
					continue
				}
				p := int(langEntry.offset)
				if p+16 > len(data) {
					continue
				}
				dataRVA := binary.LittleEndian.Uint32(data[p : p+4])
				dataSize := binary.LittleEndian.Uint32(data[p+4 : p+8])
				if dataSize == 0 {
					continue
				}
				fileOff, convOK := rvaToOff(dataRVA)
				if !convOK {
					continue
				}
				chunk := make([]byte, dataSize)
				if _, err := f.ReadAt(chunk, fileOff); err != nil && err != io.EOF {
					continue
				}
				versionChunk = chunk
				break
			}
			if versionChunk != nil {
				break
			}
		}
		if versionChunk != nil {
			break
		}
	}
	if versionChunk == nil {
		return out, nil
	}
	// [段4] VS_VERSIONINFO——固定信息 + StringFileInfo 键值
	root, _, rootOK := parseVersionBlock(versionChunk, 0)
	if !rootOK || !strings.EqualFold(root.key, "VS_VERSION_INFO") {
		return out, nil
	}
	if len(root.value) >= 52 && binary.LittleEndian.Uint32(root.value[0:4]) == 0xFEEF04BD {
		fileMS := binary.LittleEndian.Uint32(root.value[8:12])
		fileLS := binary.LittleEndian.Uint32(root.value[12:16])
		prodMS := binary.LittleEndian.Uint32(root.value[16:20])
		prodLS := binary.LittleEndian.Uint32(root.value[20:24])
		if fileMS != 0 || fileLS != 0 {
			out.fixedFile = fixedVersionText(fileMS, fileLS)
		}
		if prodMS != 0 || prodLS != 0 {
			out.fixedProduct = fixedVersionText(prodMS, prodLS)
		}
	}
	for _, child := range root.children {
		if !strings.EqualFold(child.key, "StringFileInfo") {
			continue
		}
		for _, table := range child.children {
			for _, item := range table.children {
				switch {
				case strings.EqualFold(item.key, "FileVersion") && out.fileVersion == "":
					out.fileVersion = blockStringValue(item)
				case strings.EqualFold(item.key, "ProductVersion") && out.productVersion == "":
					out.productVersion = blockStringValue(item)
				}
			}
		}
	}
	return out, nil
}

// findSection 找承载 RVA 的节（跨度取 VirtualSize 与 SizeOfRawData 的较大者）。
func findSection(sections []peSection, rva uint32) (peSection, bool) {
	for _, sec := range sections {
		span := sec.virtualSize
		if sec.rawSize > span {
			span = sec.rawSize
		}
		if rva >= sec.virtualAddr && rva < sec.virtualAddr+span {
			return sec, true
		}
	}
	return peSection{}, false
}

// resourceDirEntry 资源目录条目——ID 或命名项 + 偏移（相对资源根；高位置位表示子目录）。
type resourceDirEntry struct {
	id     uint32
	isDir  bool
	offset uint32
}

// parseResourceDir 解析 IMAGE_RESOURCE_DIRECTORY（offset 相对资源根）。
func parseResourceDir(data []byte, offset uint32) ([]resourceDirEntry, bool) {
	base := int(offset)
	if base < 0 || base+16 > len(data) {
		return nil, false
	}
	named := int(binary.LittleEndian.Uint16(data[base+12 : base+14]))
	ids := int(binary.LittleEndian.Uint16(data[base+14 : base+16]))
	total := named + ids
	entries := make([]resourceDirEntry, 0, total)
	for i := 0; i < total; i++ {
		p := base + 16 + i*8
		if p+8 > len(data) {
			return nil, false
		}
		nameField := binary.LittleEndian.Uint32(data[p : p+4])
		offField := binary.LittleEndian.Uint32(data[p+4 : p+8])
		entry := resourceDirEntry{isDir: offField&0x80000000 != 0, offset: offField & 0x7FFFFFFF}
		if nameField&0x80000000 == 0 {
			entry.id = nameField
		}
		entries = append(entries, entry)
	}
	return entries, true
}

// versionBlock VS_VERSIONINFO 结构块——键 + 值字节 + 子块。
type versionBlock struct {
	key      string
	value    []byte
	children []versionBlock
}

// parseVersionBlock 解析单个 VS_VERSIONINFO 块——返回块与其结束偏移。
func parseVersionBlock(raw []byte, offset int) (versionBlock, int, bool) {
	if offset < 0 || offset+6 > len(raw) {
		return versionBlock{}, 0, false
	}
	length := int(binary.LittleEndian.Uint16(raw[offset : offset+2]))
	valueLength := int(binary.LittleEndian.Uint16(raw[offset+2 : offset+4]))
	blockType := binary.LittleEndian.Uint16(raw[offset+4 : offset+6])
	if length < 6 || offset+length > len(raw) {
		return versionBlock{}, 0, false
	}
	blockEnd := offset + length
	// [段1] szKey——UTF-16LE 至首个双零
	keyStart := offset + 6
	keyEnd := keyStart
	for keyEnd+1 < blockEnd {
		if raw[keyEnd] == 0 && raw[keyEnd+1] == 0 {
			break
		}
		keyEnd += 2
	}
	block := versionBlock{key: decodeUTF16(raw[keyStart:keyEnd])}
	// [段2] Value——text 型 wValueLength 为 WCHAR 数；binary 型为字节数
	valueStart := align4(keyEnd + 2)
	valueBytes := valueLength
	if blockType == 1 {
		valueBytes = valueLength * 2
	}
	valueEnd := valueStart + valueBytes
	if valueEnd > blockEnd {
		valueEnd = blockEnd
	}
	if valueStart < blockEnd && valueEnd > valueStart {
		block.value = raw[valueStart:valueEnd]
	}
	// [段3] 子块——按各自 wLength 前进（4 字节对齐）
	childStart := align4(valueEnd)
	for childStart+6 <= blockEnd {
		if binary.LittleEndian.Uint16(raw[childStart:childStart+2]) == 0 {
			break
		}
		child, next, ok := parseVersionBlock(raw, childStart)
		if !ok || next <= childStart {
			break
		}
		block.children = append(block.children, child)
		childStart = align4(next)
	}
	return block, blockEnd, true
}

// align4 向上对齐到 4 字节边界。
func align4(offset int) int {
	return (offset + 3) &^ 3
}

// decodeUTF16 解码 UTF-16LE（至首个零单元）。
func decodeUTF16(raw []byte) string {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		unit := binary.LittleEndian.Uint16(raw[i : i+2])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units))
}

// blockStringValue 取 String 块的值文本（UTF-16LE，去尾零）。
func blockStringValue(block versionBlock) string {
	return strings.TrimRight(decodeUTF16(block.value), "\x00")
}

// fixedVersionText VS_FIXEDFILEINFO 数值版本 → a.b.c.d。
func fixedVersionText(ms, ls uint32) string {
	return itoa16(ms>>16) + "." + itoa16(ms&0xFFFF) + "." + itoa16(ls>>16) + "." + itoa16(ls&0xFFFF)
}

// itoa16 低 16 位转十进制文本。
func itoa16(value uint32) string {
	if value == 0 {
		return "0"
	}
	var digits [5]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
