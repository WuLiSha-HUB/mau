package admin

import (
	"net/http"
	"strings"

	"cathome5/host/config"
)

// 本文件复现管理端段 6「外观配置池」五件——池列表 / 新建 / 编辑 / 删除 / 设为默认。
//
// 契约对齐 `mau:CatHome4/CatHome4.Admin/AdminService.Appearance.cs`：
// 池文件 Data/config/appearance.json（无密钥面——单文件落盘，不设 secrets 层）；
// 首条目自动成默认；同身份重写保留原默认标记；默认唯一（设默认即清其余）；
// 删除后引用它的猫即时回落默认外观（保守值 roundSummary=false）。

// appearanceItem 是池列表条目（GET 响应项——七字段，顺序对齐 C# 匿名对象）。
type appearanceItem struct {
	// AppearanceID 外观配置身份（GUID D 形态）
	AppearanceID string `json:"appearanceId"`
	// DisplayName 用户可见名称
	DisplayName string `json:"displayName"`
	// PetKind 桌宠种类
	PetKind string `json:"petKind"`
	// TtsStyle TTS 风格槽位
	TtsStyle string `json:"ttsStyle"`
	// RoundSummary 轮末总结开关
	RoundSummary bool `json:"roundSummary"`
	// SummaryAudio 总结成音频开关
	SummaryAudio bool `json:"summaryAudio"`
	// IsDefault 默认外观标记
	IsDefault bool `json:"isDefault"`
}

// appearanceListResponse 是 GET /api/v1/appearance-apis 的响应体。
//
// 注意：无 ok 字段——与 C# `new { version, items }` 同形（前端零改动判据）。
type appearanceListResponse struct {
	// Version 响应面版本
	Version int `json:"version"`
	// Items 池条目（Guid.CompareTo 序）
	Items []appearanceItem `json:"items"`
}

// appearanceWriteResponse 是新建 / 编辑 / 删除的回执（ok + appearanceId）。
type appearanceWriteResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// AppearanceID 本次操作的配置身份
	AppearanceID string `json:"appearanceId"`
}

// errAppearanceNameEmpty 是名称归一后为空的内部错误（C# 侧抛 InvalidDataException）。
var errAppearanceNameEmpty = errText("外观名称不能为空")

// errText 是最简错误类型。
type errText string

// Error 实现 error。
func (e errText) Error() string { return string(e) }

// handleAppearanceApisGet 服务 GET /api/v1/appearance-apis——池读取（每次请求重读文件，不缓存）。
func (d *Deps) handleAppearanceApisGet(w http.ResponseWriter, r *http.Request) {
	pool := config.LoadAppearancePool(d.appearancePoolPath())
	items := make([]appearanceItem, 0, len(pool))
	for _, cfg := range pool {
		items = append(items, appearanceItem{
			AppearanceID: cfg.AppearanceID,
			DisplayName:  cfg.DisplayName,
			PetKind:      cfg.PetKind,
			TtsStyle:     cfg.TtsStyle,
			RoundSummary: cfg.RoundSummary,
			SummaryAudio: cfg.SummaryAudio,
			IsDefault:    cfg.IsDefault,
		})
	}
	writeJSON(w, appearanceListResponse{Version: 1, Items: items})
}

// handleAppearanceApisPost 服务 POST /api/v1/appearance-apis——池新建（body: displayName + 四字段）。
func (d *Deps) handleAppearanceApisPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	displayName := fieldString(fields, "displayName")
	if displayName == "" {
		writeErr(w, "displayName 为空")
		return
	}
	if d.DataRoot == "" {
		writeErr(w, "外观池未绑定")
		return
	}
	id := newGUID()
	if id == "" {
		writeErr(w, "外观池未绑定")
		return
	}
	cfg := config.AppearanceConfig{
		SchemaVersion: 1,
		AppearanceID:  id,
		DisplayName:   displayName,
		PetKind:       fieldString(fields, "petKind"),
		TtsStyle:      fieldString(fields, "ttsStyle"),
		RoundSummary:  fieldBool(fields, "roundSummary"),
		SummaryAudio:  fieldBool(fields, "summaryAudio"),
	}
	if err := saveAppearance(d.appearancePoolPath(), cfg); err != nil {
		d.log(3, "外观池写入失败："+err.Error())
		writeErr(w, "写入失败: "+err.Error())
		return
	}
	d.log(1, "外观池新增条目「"+cfg.DisplayName+"」（"+id+"）")
	writeJSON(w, appearanceWriteResponse{OK: true, AppearanceID: id})
}

// handleAppearanceApisEdit 服务 POST /api/v1/appearance-apis/edit——池编辑（body: appearanceId + 四字段）。
func (d *Deps) handleAppearanceApisEdit(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	id := config.ParseGUID(fieldString(fields, "appearanceId"))
	if id == "" || config.IsZeroGUID(id) {
		writeErr(w, "appearanceId 非法")
		return
	}
	displayName := fieldString(fields, "displayName")
	if displayName == "" {
		writeErr(w, "displayName 为空")
		return
	}
	if d.DataRoot == "" {
		writeErr(w, "外观池未绑定")
		return
	}
	pool, found := findAppearance(d.appearancePoolPath(), id)
	if !found {
		writeErr(w, "外观不存在")
		return
	}
	cfg := config.AppearanceConfig{
		SchemaVersion: 1,
		AppearanceID:  id,
		DisplayName:   displayName,
		PetKind:       fieldString(fields, "petKind"),
		TtsStyle:      fieldString(fields, "ttsStyle"),
		RoundSummary:  fieldBool(fields, "roundSummary"),
		SummaryAudio:  fieldBool(fields, "summaryAudio"),
		IsDefault:     pool.IsDefault,
	}
	if err := saveAppearance(d.appearancePoolPath(), cfg); err != nil {
		d.log(3, "外观池写入失败："+err.Error())
		writeErr(w, "写入失败: "+err.Error())
		return
	}
	d.log(1, "外观池「"+cfg.DisplayName+"」已更新（"+id+"）")
	writeJSON(w, appearanceWriteResponse{OK: true, AppearanceID: id})
}

// handleAppearanceApisDelete 服务 POST /api/v1/appearance-apis/delete——池删除（body: appearanceId）。
//
// 默认项亦可删（与 LLM API 池同拍板）；引用它的猫解析为 null → 回落默认外观。
func (d *Deps) handleAppearanceApisDelete(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	id := config.ParseGUID(fieldString(fields, "appearanceId"))
	if id == "" || config.IsZeroGUID(id) {
		writeErr(w, "appearanceId 非法")
		return
	}
	if d.DataRoot == "" {
		writeErr(w, "外观池未绑定")
		return
	}
	path := d.appearancePoolPath()
	pool := config.LoadAppearancePool(path)
	kept := make([]config.AppearanceConfig, 0, len(pool))
	removed := false
	for _, cfg := range pool {
		if cfg.AppearanceID == id {
			removed = true
			continue
		}
		kept = append(kept, cfg)
	}
	if !removed {
		writeErr(w, "外观不存在")
		return
	}
	if err := config.SaveAppearancePool(path, kept); err != nil {
		d.log(3, "外观池写入失败："+err.Error())
		writeErr(w, "写入失败: "+err.Error())
		return
	}
	d.log(1, "外观池条目已删除："+id)
	writeJSON(w, appearanceWriteResponse{OK: true, AppearanceID: id})
}

// handleAppearanceApisDefault 服务 POST /api/v1/appearance-apis/default——池设为默认（body: appearanceId）。
//
// 回执只有 ok（无 appearanceId）——与 C# `new { ok = true }` 同形。
func (d *Deps) handleAppearanceApisDefault(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	id := config.ParseGUID(fieldString(fields, "appearanceId"))
	if id == "" || config.IsZeroGUID(id) {
		writeErr(w, "appearanceId 非法")
		return
	}
	if d.DataRoot == "" {
		writeErr(w, "外观池未绑定")
		return
	}
	path := d.appearancePoolPath()
	pool := config.LoadAppearancePool(path)
	found := false
	for i := range pool {
		if pool[i].AppearanceID == id {
			pool[i].IsDefault = true
			found = true
			continue
		}
		pool[i].IsDefault = false
	}
	if !found {
		writeErr(w, "外观不存在")
		return
	}
	if err := config.SaveAppearancePool(path, pool); err != nil {
		d.log(3, "外观池写入失败："+err.Error())
		writeErr(w, "写入失败: "+err.Error())
		return
	}
	d.log(1, "外观池已设为默认："+id)
	writeJSON(w, okResponse{OK: true})
}

// saveAppearance 池写入一条（对齐 CH_AppearanceConfigStore.Save 的三条语义）。
//
// ① 归一 + 校验（名称 / 种类 / 风格 trim；身份非零、名称非空）；
// ② 同身份替换——未显式置默认时保留原默认标记；
// ③ 池空时新条目自动成默认（唯一外观必然被跟随默认的猫消费）。
func saveAppearance(path string, cfg config.AppearanceConfig) error {
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.PetKind = strings.TrimSpace(cfg.PetKind)
	cfg.TtsStyle = strings.TrimSpace(cfg.TtsStyle)
	if cfg.SchemaVersion != 1 || cfg.AppearanceID == "" {
		return errText("外观配置非法（身份缺失）")
	}
	if cfg.DisplayName == "" {
		return errAppearanceNameEmpty
	}
	pool := config.LoadAppearancePool(path)
	replaced := false
	for i := range pool {
		if pool[i].AppearanceID != cfg.AppearanceID {
			continue
		}
		if !cfg.IsDefault {
			cfg.IsDefault = pool[i].IsDefault
		}
		pool[i] = cfg
		replaced = true
		break
	}
	if !replaced {
		if len(pool) == 0 {
			cfg.IsDefault = true
		}
		pool = append(pool, cfg)
	}
	return config.SaveAppearancePool(path, pool)
}

// findAppearance 按身份查一条（返回独立副本——调用方改写不污染池）。
func findAppearance(path, id string) (config.AppearanceConfig, bool) {
	for _, cfg := range config.LoadAppearancePool(path) {
		if cfg.AppearanceID == id {
			return cfg, true
		}
	}
	return config.AppearanceConfig{}, false
}
