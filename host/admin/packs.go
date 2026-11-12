package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"cathome5/host/config"
)

// 本文件复现管理端段 7「加载包池」三件——GET / POST / delete。
//
// 契约对齐 `mau:CatHome4/CatHome4.Admin/AdminService.Packs.cs`：
// 池文件 Data/config/packs.json，形态 `{"packs":{key:{desc,paths}}}`（保持池序）；
// 同名覆盖 = 增改一体；删除按 key 精确匹配。

// packEntry 是池条目（GET 响应项——key / desc / paths 三字段）。
type packEntry struct {
	// Key 包 key（池内唯一）
	Key string `json:"key"`
	// Desc 描述
	Desc string `json:"desc"`
	// Paths 路径清单（原样输出，未命中的路径不在读面过滤）
	Paths []string `json:"paths"`
}

// packsGetResponse 是 GET /api/v1/packs 的响应体。
type packsGetResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Packs 池条目（声明序）
	Packs []packEntry `json:"packs"`
}

// packWriteResponse 是写面回执（POST / delete 同形：ok/key/count）。
type packWriteResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Key 本次操作的包 key
	Key string `json:"key"`
	// Count 操作后的池条目数
	Count int `json:"count"`
}

// handlePacksGet 服务 GET /api/v1/packs——池读取（每次请求重读文件，不缓存）。
func (d *Deps) handlePacksGet(w http.ResponseWriter, r *http.Request) {
	pool := config.LoadPackPool(config.PackPoolPath(d.DataRoot))
	items := make([]packEntry, 0, len(pool))
	for _, def := range pool {
		paths := def.Paths
		if paths == nil {
			paths = []string{}
		}
		items = append(items, packEntry{Key: def.Key, Desc: def.Desc, Paths: paths})
	}
	writeJSON(w, packsGetResponse{OK: true, Packs: items})
}

// handlePacksPost 服务 POST /api/v1/packs——池写入（body: {key, desc, paths[]}）。
//
// 同名覆盖 = 增改一体；路径项空串剔除；校验失败即拒（不静默回落）。
func (d *Deps) handlePacksPost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, "body 非 JSON")
		return
	}
	var payload struct {
		Key   string          `json:"key"`
		Desc  string          `json:"desc"`
		Paths json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, "body 非 JSON")
		return
	}
	key := strings.TrimSpace(payload.Key)
	desc := strings.TrimSpace(payload.Desc)
	var rawPaths []json.RawMessage
	if len(payload.Paths) > 0 {
		_ = json.Unmarshal(payload.Paths, &rawPaths)
	}
	paths := make([]string, 0, len(rawPaths))
	for _, item := range rawPaths {
		var text string
		if err := json.Unmarshal(item, &text); err != nil {
			continue
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if key == "" {
		writeErr(w, "key 为空")
		return
	}
	if len(paths) == 0 {
		writeErr(w, "paths 为空（包至少一个路径项）")
		return
	}
	poolPath := config.PackPoolPath(d.DataRoot)
	pool := config.LoadPackPool(poolPath)
	found := false
	for i := range pool {
		if pool[i].Key == key {
			pool[i].Desc = desc
			pool[i].Paths = paths
			found = true
			break
		}
	}
	if !found {
		pool = append(pool, config.PackDefinition{Key: key, Desc: desc, Paths: paths})
	}
	if err := config.SavePackPool(poolPath, pool); err != nil {
		d.log(3, "包池写入失败："+err.Error())
		writeErr(w, "写入失败: "+err.Error())
		return
	}
	d.log(1, "包池写入："+key+"（"+itoa(len(paths))+" 个路径项）")
	writeJSON(w, packWriteResponse{OK: true, Key: key, Count: len(pool)})
}

// handlePacksDelete 服务 POST /api/v1/packs/delete——池删除（body: {key}）。
func (d *Deps) handlePacksDelete(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, "body 非 JSON")
		return
	}
	var payload struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, "body 非 JSON")
		return
	}
	key := strings.TrimSpace(payload.Key)
	if key == "" {
		writeErr(w, "key 为空")
		return
	}
	poolPath := config.PackPoolPath(d.DataRoot)
	pool := config.LoadPackPool(poolPath)
	kept := make([]config.PackDefinition, 0, len(pool))
	removed := false
	for _, def := range pool {
		if def.Key == key {
			removed = true
			continue
		}
		kept = append(kept, def)
	}
	if !removed {
		writeErr(w, "包不存在: "+key)
		return
	}
	if err := config.SavePackPool(poolPath, kept); err != nil {
		d.log(3, "包池写入失败："+err.Error())
		writeErr(w, "写入失败: "+err.Error())
		return
	}
	writeJSON(w, packWriteResponse{OK: true, Key: key, Count: len(kept)})
}
