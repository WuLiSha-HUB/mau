package toolpool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// bricksIndex 是 Bricks/index.json 的最小解析结构。
type bricksIndex struct {
	Bricks []struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Path     string `json:"path"`
	} `json:"bricks"`
}

// LoadFromBricks 从 Bricks 目录聚合工具组定义。
//
// 路径解析：index.json 的 path 相对 Bricks 根；只取 category=TOOLS 的条目
// （= tools.<组> 定义积木，GetToolsJson 返回该组工具定义整包 JSON）。
// 返回按组名字母序排序的分组（与 C# 侧 Flow 扫描序一致）。
func LoadFromBricks(indexPath string) ([]GroupSource, error) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("读取 Bricks/index.json 失败: %w", err)
	}
	var index bricksIndex
	if err := json.Unmarshal(trimBOM(data), &index); err != nil {
		return nil, fmt.Errorf("解析 Bricks/index.json 失败: %w", err)
	}
	root := filepath.Dir(indexPath)
	groups := make([]GroupSource, 0, 24)
	for _, brick := range index.Bricks {
		if !strings.EqualFold(brick.Category, "TOOLS") {
			continue
		}
		jsonText, err := readToolsJSON(filepath.Join(root, filepath.FromSlash(brick.Path)), brick.Name)
		if err != nil {
			// 单组失败不阻断——以缺陷形式进池（组名可归因）
			groups = append(groups, GroupSource{Name: groupFromBrickName(brick.Name), JSON: ""})
			continue
		}
		groups = append(groups, GroupSource{Name: groupNameOf(jsonText, brick.Name), JSON: jsonText})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// readToolsJSON 从工具定义积木源码提取 GetToolsJson 的字符串常量。
func readToolsJSON(path, brickName string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", brickName, err)
	}
	text, err := extractReturnString(string(trimBOM(src)), "GetToolsJson")
	if err != nil {
		return "", fmt.Errorf("%s: %w", brickName, err)
	}
	return text, nil
}

// extractReturnString 提取指定方法体内 return 的字符串常量拼接结果。
//
// 目标形态（Bricks/TOOLS 源码固定）：`return "..." + "..." + ... ;`。
// 扫描器只接受空白 / '+' / 字符串字面量 / ';'——遇到其它字符即报错（不猜）。
func extractReturnString(src, marker string) (string, error) {
	markerIdx := strings.Index(src, marker)
	if markerIdx < 0 {
		return "", fmt.Errorf("未找到方法 %s", marker)
	}
	rest := src[markerIdx:]
	// 方法名之后的第一个「真 return」——跳过 XML 注释里的 <returns> 等字样：
	// 判据 = return 之后跳过空白即字符串字面量起点（注释里的 return 后跟 's'）。
	returnIdx := -1
	searchFrom := 0
	for {
		idx := strings.Index(rest[searchFrom:], "return")
		if idx < 0 {
			break
		}
		abs := searchFrom + idx
		j := abs + len("return")
		for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == '\r' || rest[j] == '\n') {
			j++
		}
		if j < len(rest) && rest[j] == '"' {
			returnIdx = abs
			break
		}
		searchFrom = abs + len("return")
	}
	if returnIdx < 0 {
		return "", fmt.Errorf("方法 %s 内未找到 return 字符串语句", marker)
	}
	var b strings.Builder
	i := returnIdx + len("return")
	for i < len(rest) {
		c := rest[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '+':
			i++
		case c == ';':
			return b.String(), nil
		case c == '"':
			text, next, err := parseCSLiteral(rest, i)
			if err != nil {
				return "", err
			}
			b.WriteString(text)
			i = next
		default:
			return "", fmt.Errorf("方法 %s 的 return 语句含非预期字符 %q（位置 %d）", marker, string(c), i)
		}
	}
	return "", fmt.Errorf("方法 %s 的 return 语句未闭合", marker)
}

// parseCSLiteral 解析一段 C# 普通字符串字面量（起点 = 开引号）。
//
// 返回解码文本与下一个扫描位置（闭引号之后）。仅支持普通字面量（Bricks 源码形态）。
func parseCSLiteral(src string, start int) (string, int, error) {
	var b strings.Builder
	i := start + 1
	for i < len(src) {
		c := src[i]
		if c == '"' {
			return b.String(), i + 1, nil
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(src) {
			return "", 0, fmt.Errorf("字符串字面量转义截断")
		}
		esc := src[i+1]
		switch esc {
		case '"', '\\', '\'':
			b.WriteByte(esc)
			i += 2
		case 'n':
			b.WriteByte('\n')
			i += 2
		case 'r':
			b.WriteByte('\r')
			i += 2
		case 't':
			b.WriteByte('\t')
			i += 2
		case '0':
			b.WriteByte(0)
			i += 2
		case 'u':
			if i+6 > len(src) {
				return "", 0, fmt.Errorf("\\u 转义截断")
			}
			code, err := strconv.ParseUint(src[i+2:i+6], 16, 32)
			if err != nil {
				return "", 0, fmt.Errorf("\\u 转义非法: %w", err)
			}
			b.WriteRune(rune(code))
			i += 6
		case 'x':
			j := i + 2
			for j < len(src) && j < i+2+4 && isHex(src[j]) {
				j++
			}
			if j == i+2 {
				return "", 0, fmt.Errorf("\\x 转义非法")
			}
			code, err := strconv.ParseUint(src[i+2:j], 16, 32)
			if err != nil {
				return "", 0, fmt.Errorf("\\x 转义非法: %w", err)
			}
			b.WriteRune(rune(code))
			i = j
		default:
			return "", 0, fmt.Errorf("未知转义 \\%c", esc)
		}
	}
	return "", 0, fmt.Errorf("字符串字面量未闭合")
}

// isHex 判定十六进制字符。
func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// groupNameOf 从定义 JSON 取 group 字段（空则按积木名推断）。
func groupNameOf(jsonText, brickName string) string {
	var head struct {
		Group string `json:"group"`
	}
	if err := json.Unmarshal([]byte(jsonText), &head); err == nil && head.Group != "" {
		return head.Group
	}
	return groupFromBrickName(brickName)
}

// groupFromBrickName 从积木名（tools.textcat）推断组名（TextCat 的兜底路径）。
func groupFromBrickName(brickName string) string {
	if i := strings.Index(brickName, "."); i >= 0 {
		return brickName[i+1:]
	}
	return brickName
}
