package front

import (
	"bytes"
	"encoding/json"
	"os"
)

// BrickContract 是积木契约的轻量镜像——Bricks/index.json 的 inputs / outputs 类型序列。
type BrickContract struct {
	Name        string
	InputTypes  []string
	OutputTypes []string
}

// BrickTable 是积木索引——名字 → 契约。零值即空表（全部按未命中处理）。
type BrickTable struct {
	entries map[string]BrickContract
}

// NewBrickTable 用条目列表构造索引。
func NewBrickTable(entries []BrickContract) *BrickTable {
	t := &BrickTable{entries: make(map[string]BrickContract, len(entries))}
	for _, e := range entries {
		t.entries[e.Name] = e
	}
	return t
}

// LoadBrickIndex 读取 Bricks/index.json 构造索引。
func LoadBrickIndex(path string) (*BrickTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// 剥 UTF-8 BOM——index.json 由生成链产出时带 BOM，json.Unmarshal 不接受
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var file brickIndexFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	entries := make([]BrickContract, 0, len(file.Bricks))
	for _, b := range file.Bricks {
		c := BrickContract{Name: b.Name}
		for _, in := range b.Contract.Inputs {
			c.InputTypes = append(c.InputTypes, in.Type)
		}
		for _, out := range b.Contract.Outputs {
			c.OutputTypes = append(c.OutputTypes, out.Type)
		}
		entries = append(entries, c)
	}
	return NewBrickTable(entries), nil
}

// Find 查积木契约。
func (t *BrickTable) Find(name string) (BrickContract, bool) {
	if t == nil {
		return BrickContract{}, false
	}
	c, ok := t.entries[name]
	return c, ok
}

// brickIndexFile 是 index.json 的解析结构（只取契约面）。
type brickIndexFile struct {
	Bricks []struct {
		Name     string `json:"name"`
		Contract struct {
			Inputs []struct {
				Type string `json:"type"`
			} `json:"inputs"`
			Outputs []struct {
				Type string `json:"type"`
			} `json:"outputs"`
		} `json:"contract"`
	} `json:"bricks"`
}
