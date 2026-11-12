package config

import "bytes"

// utf8BOM 是 UTF-8 字节序标记。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// StripBOM 去掉开头的 UTF-8 BOM。
//
// C# 侧 StreamReader / JsonUtil 对 BOM 宽容（现有数据文件带 BOM 也能读）；
// Go 的 json.Unmarshal 不容忍。数据面互换要求两侧读同一份文件行为一致——
// 读入口统一剥一层（判例：数据副本 schema.json 带 BOM，首轮启动 schema 加载失败）。
func StripBOM(raw []byte) []byte {
	return bytes.TrimPrefix(raw, utf8BOM)
}
