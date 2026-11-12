// Package db 复现 Smriti 条目库服务（C# `Mau.Runtime.DbService`）——一库一目录、追加式 JSONL、零向量化。
//
// 库文件即真源：无索引件、无缓存件；写入只追加（更新追加新版本行、删除追加墓碑行）。
// 库根布局：
//
//	<dbRoot>/_index.json          库注册表（登记即库存在性判据）
//	<dbRoot>/_trash/<lib>-<ts>/   删库回收区（不进注册表，不参与库扫描）
//	<dbRoot>/<lib>/meta.json      库元信息（lib / fmt / createdAt）
//	<dbRoot>/<lib>/entries.jsonl  条目日志（首行头行，此后每行一条记录）
//
// 授权：每猫 dbLibs 白名单由宿主登记（Policy 段），服务层是唯一判定点（fail-closed）。
package db

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// formatSupport 是支持的存储格式版本（entries.jsonl 头行 fmt 判据；不符整库拒绝读取）。
	formatSupport = 1
	// titleMax 是标题硬上限（Unicode 字符数；超限拒写）。
	titleMax = 50
	// bodyMax 是正文硬上限（Unicode 字符数；超限拒写）。
	bodyMax = 5000
	// defaultLimit 是查询默认条数。
	defaultLimit = 10
	// limitMax 是查询条数上限。
	limitMax = 200
	// keywordMax 是查询关键词长度上限（Unicode 字符数；超限拒）。
	keywordMax = 200
	// entriesFile 是条目日志文件名（每库一份，追加式）。
	entriesFile = "entries.jsonl"
	// metaFile 是库元信息文件名。
	metaFile = "meta.json"
	// indexFile 是库注册表文件名（库根下全局一份；登记即库存在性判据）。
	indexFile = "_index.json"
	// trashDir 是删库回收目录名（库根下；不进注册表，不参与库扫描）。
	trashDir = "_trash"
	// metaKind 是条目文件头行的类别标记。
	metaKind = "db.entries"
)

// libIDPattern 是库名合法形态——小写字母 / 数字 / 短横线，2–32 位（单层目录名）。
var libIDPattern = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)

// Result 是库服务结果——统一两态：OK 带载荷 / 失败带错误码与说明。
type Result struct {
	// OK 成功标志（false = 失败，Code / Message 有效）
	OK bool
	// Code 机器码（成功为空；失败为大写下划线码，如 DB_LIB_NOT_FOUND）
	Code string
	// Message 说明文本（成功给动作摘要；失败给中文原因）
	Message string
	// Data 载荷（成功时的 JSON 文本；无载荷为空串）
	Data string
	// BadLines 本次读取跳过的坏行数（0 = 无坏行；>0 时已逐行出声）
	BadLines int
}

// Success 成功结果——动作摘要 + 载荷。
func Success(message, data string) Result {
	return Result{OK: true, Message: message, Data: data}
}

// SuccessBad 成功结果——附坏行计数。
func SuccessBad(message, data string, badLines int) Result {
	return Result{OK: true, Message: message, Data: data, BadLines: badLines}
}

// Fail 失败结果——机器码 + 中文原因。
func Fail(code, message string) Result {
	return Result{OK: false, Code: code, Message: message}
}

// ToolText 工具回执文本——成功走「结构化头 + 摘要行 + 载荷」三段；失败走 ERR|CODE|msg。
//
// 形态与 C# DbResult.ToToolText 逐字对齐（头键序 ok / tool / target / badLines）；
// 五个 db-* 积木与三个 dbAdmin-* 积木共用（组翻译积木闭包不跨积木引用，故落服务层）。
func (r Result) ToolText(tool, target string) string {
	if !r.OK {
		return "ERR|" + r.Code + "|" + r.Message
	}
	var b strings.Builder
	b.WriteString(`{"ok":true,"tool":`)
	b.WriteString(quote(tool))
	if target != "" {
		b.WriteString(`,"target":`)
		b.WriteString(quote(target))
	}
	if r.BadLines > 0 {
		b.WriteString(`,"badLines":`)
		b.WriteString(strconv.Itoa(r.BadLines))
	}
	b.WriteString("}")
	b.WriteString("\n")
	b.WriteString(r.Message)
	if r.Data != "" {
		b.WriteString("\n")
		b.WriteString(r.Data)
	}
	return b.String()
}

// Entry 是条目聚合态——同一 ID 的多行记录取 v 最大者后的内存形态。
type Entry struct {
	// ID 库内自增 ID（从 1 起；删除不复用）
	ID int
	// Title 标题（库内唯一键，不可改名）
	Title string
	// Body 正文
	Body string
	// V 版本号（从 1 起，每次改 +1）
	V int
	// Del 墓碑标记（1 = 已删除，物理行保留）
	Del int
	// CreatedAt 首次写入时刻
	CreatedAt string
	// UpdatedAt 最近写入时刻
	UpdatedAt string
}

// Lib 是库登记条目（`_index.json` 一行）。
type Lib struct {
	// ID 库名（库根下一层目录名）
	ID string
	// CreatedAt 建库时刻
	CreatedAt string
}

// Service 是条目库服务——一库一目录、追加式 JSONL。
type Service struct {
	// root 库根目录绝对路径（一库一子目录）
	root string
	// gate 串行保护复合写操作（追加行与注册表更新）
	gate sync.Mutex
	// logf 观测日志出口（nil = 不出声）
	logf func(level int, message string)
}

// New 建立库服务——库根不存在则创建。
func New(dbRoot string) (*Service, error) {
	if strings.TrimSpace(dbRoot) == "" {
		return nil, errors.New("库根路径为空")
	}
	normalized, err := normalizeRoot(dbRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(normalized, 0o755); err != nil {
		return nil, err
	}
	return &Service{root: normalized}, nil
}

// Root 库根目录实况（只读）。
func (s *Service) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// SetLogger 设置观测日志出口。
func (s *Service) SetLogger(logf func(level int, message string)) {
	if s == nil {
		return
	}
	s.logf = logf
}

// log 出声（nil 出口丢弃）。
func (s *Service) log(level int, message string) {
	if s == nil || s.logf == nil {
		return
	}
	s.logf(level, message)
}

// normalizeRoot 规范库根——绝对路径 + 去末尾分隔符 + 目录就位。
func normalizeRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(abs)
	// 保留卷根（如 `D:\`）的尾分隔符
	volume := filepath.VolumeName(cleaned)
	if len(cleaned) > len(volume)+1 && strings.HasSuffix(cleaned, string(filepath.Separator)) {
		cleaned = strings.TrimSuffix(cleaned, string(filepath.Separator))
	}
	return cleaned, nil
}

// [段1] 建库

// Create 新建库——建目录 + meta.json + entries.jsonl 头行 + 注册表登记。
func (s *Service) Create(libID, catID string) Result {
	lib := normalizeLibID(libID)
	if !libIDPattern.MatchString(lib) {
		return Fail("BAD_ARGS", "库名不合法（小写字母 / 数字 / 短横线，2–32 位）: "+libID)
	}
	if deny := DenyReason(catID, lib); deny != "" {
		return Fail("DB_LIB_DENIED", deny)
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	libs, indexErr := s.readIndex()
	if indexErr != "" {
		return Fail("DB_INDEX_UNREADABLE", indexErr)
	}
	if findLib(libs, lib) != nil {
		return Fail("DB_LIB_EXISTS", "库已存在: "+lib)
	}
	stamp := nowStamp()
	libDir := filepath.Join(s.root, lib)
	// 目录 + 元信息 + 头行——头行此后不改（格式判据）
	meta := objectJSON(
		field{"lib", lib},
		field{"fmt", formatSupport},
		field{"createdAt", stamp},
	)
	head := objectJSON(
		field{"_", metaKind},
		field{"fmt", formatSupport},
		field{"lib", lib},
	) + "\n"
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return Fail("DB_IO_FAIL", "建库落盘失败: "+err.Error())
	}
	if err := atomicWrite(filepath.Join(libDir, metaFile), meta); err != nil {
		return Fail("DB_IO_FAIL", "建库落盘失败: "+err.Error())
	}
	if err := atomicWrite(filepath.Join(libDir, entriesFile), head); err != nil {
		return Fail("DB_IO_FAIL", "建库落盘失败: "+err.Error())
	}
	libs = append(libs, Lib{ID: lib, CreatedAt: stamp})
	if writeErr := s.writeIndex(libs); writeErr != "" {
		return Fail("DB_IO_FAIL", writeErr)
	}
	return Success("库已建立: "+lib, objectJSON(field{"id", lib}, field{"createdAt", stamp}))
}

// [段2] 写入

// Put 新增条目——分配库内自增 ID（当前最大 ID + 1，含墓碑行），标题库内唯一。
func (s *Service) Put(libID, title, body, catID string) Result {
	titleText := trimText(title)
	bodyText := body
	s.gate.Lock()
	defer s.gate.Unlock()
	all, badLines, openFail := s.openLib(catID, libID)
	if openFail != nil {
		return *openFail
	}
	if textFail := validateTitleBody(titleText, bodyText, true); textFail != nil {
		return *textFail
	}
	if findActiveByTitle(all, titleText) != nil {
		return Fail("DB_TITLE_EXISTS", "标题已存在: "+titleText)
	}
	nextID := 1
	for _, entry := range all {
		if entry.ID >= nextID {
			nextID = entry.ID + 1
		}
	}
	stamp := nowStamp()
	entry := Entry{ID: nextID, Title: titleText, Body: bodyText, V: 1, Del: 0, CreatedAt: stamp, UpdatedAt: stamp}
	if appendErr := s.appendEntry(libID, entry); appendErr != "" {
		return Fail("DB_IO_FAIL", appendErr)
	}
	return SuccessBad("条目已写入: #"+strconv.Itoa(nextID),
		objectJSON(field{"id", nextID}, field{"v", 1}), badLines)
}

// Update 更新条目正文——按标题定位，追加 v+1 新版本行（标题不可改）。
func (s *Service) Update(libID, title, body, catID string) Result {
	titleText := trimText(title)
	bodyText := body
	s.gate.Lock()
	defer s.gate.Unlock()
	all, badLines, openFail := s.openLib(catID, libID)
	if openFail != nil {
		return *openFail
	}
	if textFail := validateTitleBody(titleText, bodyText, false); textFail != nil {
		return *textFail
	}
	current := findActiveByTitle(all, titleText)
	if current == nil {
		return Fail("DB_TITLE_NOT_FOUND", "标题不存在: "+titleText)
	}
	entry := Entry{
		ID: current.ID, Title: current.Title, Body: bodyText,
		V: current.V + 1, Del: 0, CreatedAt: current.CreatedAt, UpdatedAt: nowStamp(),
	}
	if appendErr := s.appendEntry(libID, entry); appendErr != "" {
		return Fail("DB_IO_FAIL", appendErr)
	}
	return SuccessBad("条目已更新: #"+strconv.Itoa(entry.ID)+" v"+strconv.Itoa(entry.V),
		objectJSON(field{"id", entry.ID}, field{"v", entry.V}), badLines)
}

// Delete 删除条目——按标题定位，追加墓碑行（物理行保留可审计；标题随即释放）。
func (s *Service) Delete(libID, title, catID string) Result {
	titleText := trimText(title)
	s.gate.Lock()
	defer s.gate.Unlock()
	all, badLines, openFail := s.openLib(catID, libID)
	if openFail != nil {
		return *openFail
	}
	if titleText == "" {
		return Fail("BAD_ARGS", "标题不能为空")
	}
	current := findActiveByTitle(all, titleText)
	if current == nil {
		return Fail("DB_TITLE_NOT_FOUND", "标题不存在: "+titleText)
	}
	entry := Entry{
		ID: current.ID, Title: current.Title, Body: current.Body,
		V: current.V + 1, Del: 1, CreatedAt: current.CreatedAt, UpdatedAt: nowStamp(),
	}
	if appendErr := s.appendEntry(libID, entry); appendErr != "" {
		return Fail("DB_IO_FAIL", appendErr)
	}
	return SuccessBad("条目已删除（墓碑保留）: #"+strconv.Itoa(entry.ID),
		objectJSON(field{"id", entry.ID}), badLines)
}

// [段3] 读取

// Query 查询条目——标题子串匹配（忽略大小写），返回 ID / 标题 / 更新时间，不含正文；分页 + 排序。
func (s *Service) Query(libID, keyword string, limit, offset int, order, catID string) Result {
	take := limit
	if take <= 0 {
		take = defaultLimit
	}
	if take > limitMax {
		return Fail("BAD_ARGS", "limit 超上限（1–"+strconv.Itoa(limitMax)+"）: "+strconv.Itoa(limit))
	}
	if offset < 0 {
		return Fail("BAD_ARGS", "offset 不能为负: "+strconv.Itoa(offset))
	}
	useOrder := strings.ToLower(trimText(order))
	if useOrder == "" {
		useOrder = "updated"
	}
	if useOrder != "updated" && useOrder != "id" {
		return Fail("BAD_ARGS", "order 非枚举（updated|id）: "+order)
	}
	needle := trimText(keyword)
	if count := charCount(needle); count > keywordMax {
		return Fail("BAD_ARGS", "keyword 超长（上限 "+strconv.Itoa(keywordMax)+" 字符，实际 "+strconv.Itoa(count)+"）")
	}
	s.gate.Lock()
	all, badLines, openFail := s.openLib(catID, libID)
	s.gate.Unlock()
	if openFail != nil {
		return *openFail
	}
	active := make([]Entry, 0, len(all))
	for _, entry := range all {
		if entry.Del == 0 {
			active = append(active, entry)
		}
	}
	matched := make([]Entry, 0, len(active))
	for _, entry := range active {
		if needle == "" || containsFold(entry.Title, needle) {
			matched = append(matched, entry)
		}
	}
	if useOrder == "id" {
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	} else {
		sort.SliceStable(matched, func(i, j int) bool {
			if matched[i].UpdatedAt != matched[j].UpdatedAt {
				return matched[i].UpdatedAt > matched[j].UpdatedAt
			}
			return matched[i].ID > matched[j].ID
		})
	}
	fetchFrom := offset
	if fetchFrom > len(matched) {
		fetchFrom = len(matched)
	}
	shown := len(matched) - fetchFrom
	if shown > take {
		shown = take
	}
	items := make([]string, 0, shown)
	for i := fetchFrom; i < fetchFrom+shown; i++ {
		items = append(items, objectJSON(
			field{"id", matched[i].ID},
			field{"title", matched[i].Title},
			field{"updatedAt", matched[i].UpdatedAt},
		))
	}
	hasMore := fetchFrom+shown < len(matched)
	nextOffset := fetchFrom + shown
	payload := objectJSON(
		field{"total", len(active)},
		field{"matched", len(matched)},
		field{"shown", shown},
		field{"offset", fetchFrom},
		field{"hasMore", hasMore},
		field{"nextOffset", nextOffset},
		field{"items", rawJSON("[" + strings.Join(items, ",") + "]")},
	)
	message := "命中 " + strconv.Itoa(len(matched)) + " / 活跃 " + strconv.Itoa(len(active)) +
		"（本页 " + strconv.Itoa(shown) + " · 偏移 " + strconv.Itoa(fetchFrom) + "）"
	return SuccessBad(message, payload, badLines)
}

// Get 取单条全文——按标题完全匹配或按 ID 取（墓碑行视为不存在）。
func (s *Service) Get(libID, mode, key, catID string) Result {
	useMode := strings.ToLower(trimText(mode))
	if useMode == "" {
		useMode = "title"
	}
	if useMode != "title" && useMode != "id" {
		return Fail("BAD_ARGS", "mode 非枚举（title|id）: "+mode)
	}
	s.gate.Lock()
	all, badLines, openFail := s.openLib(catID, libID)
	s.gate.Unlock()
	if openFail != nil {
		return *openFail
	}
	keyText := trimText(key)
	var found *Entry
	if useMode == "title" {
		found = findActiveByTitle(all, keyText)
	} else {
		id, ok := tryParseInt(keyText)
		if !ok || id <= 0 {
			return Fail("BAD_ARGS", "id 须为正整数: "+keyText)
		}
		found = findActiveByID(all, id)
	}
	if found == nil {
		return Fail("DB_ENTRY_NOT_FOUND", "条目未命中（"+useMode+"="+keyText+"）")
	}
	payload := objectJSON(
		field{"id", found.ID},
		field{"title", found.Title},
		field{"body", found.Body},
		field{"v", found.V},
		field{"createdAt", found.CreatedAt},
		field{"updatedAt", found.UpdatedAt},
	)
	return SuccessBad("条目 #"+strconv.Itoa(found.ID), payload, badLines)
}

// ListLibs 库清单——注册表登记的库 + 活跃条目计数（控制台库下拉与端点用）。
//
// 按猫授权过滤：未授权库不出现在清单（防泄露未登记库存在性；空 catID = 全放）。
func (s *Service) ListLibs(catID string) Result {
	libs, indexErr := s.readIndex()
	if indexErr != "" {
		return Fail("DB_INDEX_UNREADABLE", indexErr)
	}
	items := make([]string, 0, len(libs))
	for _, lib := range libs {
		if !IsAllowed(catID, lib.ID) {
			continue
		}
		all, _, openFail := s.openLib(catID, lib.ID)
		active := 0
		if openFail == nil {
			for _, entry := range all {
				if entry.Del == 0 {
					active++
				}
			}
		}
		items = append(items, objectJSON(
			field{"id", lib.ID},
			field{"createdAt", lib.CreatedAt},
			field{"entries", active},
			field{"broken", openFail != nil},
		))
	}
	// 摘要自证全集——过滤掉未授权库时显式出声（不静默缩水）
	summary := "库 " + strconv.Itoa(len(items))
	if hidden := len(libs) - len(items); hidden > 0 {
		summary = summary + "（授权内 " + strconv.Itoa(len(items)) + " / 共 " + strconv.Itoa(len(libs)) +
			"——" + strconv.Itoa(hidden) + " 个未授权已隐藏）"
	}
	return Success(summary, objectJSON(field{"libs", rawJSON("[" + strings.Join(items, ",") + "]")}))
}

// ListLibsDetail 库级明细——注册表登记的库 + 活跃 / 墓碑 / 坏行 / 字节数（管理面用）。
//
// 按猫授权过滤，摘要自证全集（与 ListLibs 同口径）。
func (s *Service) ListLibsDetail(catID string) Result {
	libs, indexErr := s.readIndex()
	if indexErr != "" {
		return Fail("DB_INDEX_UNREADABLE", indexErr)
	}
	items := make([]string, 0, len(libs))
	for _, lib := range libs {
		if !IsAllowed(catID, lib.ID) {
			continue
		}
		all, badLines, openFail := s.openLib(catID, lib.ID)
		active := 0
		tombstones := 0
		if openFail == nil {
			for _, entry := range all {
				if entry.Del == 0 {
					active++
				} else {
					tombstones++
				}
			}
		}
		bytes := int64(0)
		if info, err := os.Stat(filepath.Join(s.root, lib.ID, entriesFile)); err == nil {
			bytes = info.Size()
		}
		items = append(items, objectJSON(
			field{"id", lib.ID},
			field{"createdAt", lib.CreatedAt},
			field{"entries", active},
			field{"tombstones", tombstones},
			field{"badLines", badLines},
			field{"bytes", bytes},
			field{"broken", openFail != nil},
		))
	}
	summary := "库 " + strconv.Itoa(len(items))
	if hidden := len(libs) - len(items); hidden > 0 {
		summary = summary + "（授权内 " + strconv.Itoa(len(items)) + " / 共 " + strconv.Itoa(len(libs)) +
			"——" + strconv.Itoa(hidden) + " 个未授权已隐藏）"
	}
	return Success(summary, objectJSON(field{"libs", rawJSON("[" + strings.Join(items, ",") + "]")}))
}

// DropLib 删库——目录移入库根 _trash 下的「库名-时间戳」目录（可恢复），注册表摘除。
//
// confirm 须与库名完全一致（精确比对，不 Trim、不转大小写——不可逆动作的保护不得被归一化削掉）。
func (s *Service) DropLib(libID, confirm, catID string) Result {
	lib := normalizeLibID(libID)
	if !libIDPattern.MatchString(lib) {
		return Fail("BAD_ARGS", "库名不合法（小写字母 / 数字 / 短横线，2–32 位）: "+libID)
	}
	// [段1] 确认串精确比对——不 Trim、不转大小写
	if confirm != lib {
		return Fail("BAD_ARGS", "confirm 须与库名完全一致（确认删库）: "+confirm)
	}
	if deny := DenyReason(catID, lib); deny != "" {
		return Fail("DB_LIB_DENIED", deny)
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	libs, indexErr := s.readIndex()
	if indexErr != "" {
		return Fail("DB_INDEX_UNREADABLE", indexErr)
	}
	target := findLib(libs, lib)
	if target == nil {
		return Fail("DB_LIB_NOT_FOUND", "库未登记: "+lib)
	}
	trashRoot := filepath.Join(s.root, trashDir)
	stamp := strings.ReplaceAll(strings.ReplaceAll(nowStamp(), " ", "_"), ":", "")
	trashPath := filepath.Join(trashRoot, lib+"-"+stamp)
	libDir := filepath.Join(s.root, lib)
	if err := os.MkdirAll(trashRoot, 0o755); err != nil {
		return Fail("DB_IO_FAIL", "移入回收区失败: "+err.Error())
	}
	if _, err := os.Stat(libDir); err == nil {
		if err := os.Rename(libDir, trashPath); err != nil {
			return Fail("DB_IO_FAIL", "移入回收区失败: "+err.Error())
		}
	}
	kept := make([]Lib, 0, len(libs))
	for _, item := range libs {
		if item.ID == target.ID {
			continue
		}
		kept = append(kept, item)
	}
	if writeErr := s.writeIndex(kept); writeErr != "" {
		// 注册表写失败——目录已移走，尽量搬回原处（避免「库还在盘上但已不在注册表」的半删态）
		if _, statErr := os.Stat(libDir); os.IsNotExist(statErr) {
			if _, trashErr := os.Stat(trashPath); trashErr == nil {
				if moveErr := os.Rename(trashPath, libDir); moveErr != nil {
					return Fail("DB_IO_FAIL", writeErr+"（目录已移入回收区且注册表未更新，需人工核对: "+trashPath+"）")
				}
			}
		}
		return Fail("DB_IO_FAIL", writeErr+"（已回滚目录，库未删除）")
	}
	return Success("库已移入回收区: "+lib, objectJSON(field{"id", lib}, field{"trash", trashPath}))
}

// [段5] 内部——打开与追加

// openLib 打开库并载入条目聚合——库名形态 → 注册表登记 → 条目文件读取（三关全过才返回 nil）。
func (s *Service) openLib(catID, libID string) ([]Entry, int, *Result) {
	lib := normalizeLibID(libID)
	if !libIDPattern.MatchString(lib) {
		fail := Fail("BAD_ARGS", "库名不合法（小写字母 / 数字 / 短横线，2–32 位）: "+libID)
		return nil, 0, &fail
	}
	// [段0] 库级授权（唯一判定点）——先于登记检查：未授权不泄露库是否存在
	if deny := DenyReason(catID, lib); deny != "" {
		fail := Fail("DB_LIB_DENIED", deny)
		return nil, 0, &fail
	}
	libs, indexErr := s.readIndex()
	if indexErr != "" {
		fail := Fail("DB_INDEX_UNREADABLE", indexErr)
		return nil, 0, &fail
	}
	if findLib(libs, lib) == nil {
		fail := Fail("DB_LIB_NOT_FOUND", "库未登记: "+lib)
		return nil, 0, &fail
	}
	all, badLines, readErr := s.readEntries(lib)
	if readErr != "" {
		fail := Fail("DB_FORMAT_UNSUPPORTED", readErr)
		return nil, badLines, &fail
	}
	return all, badLines, nil
}

// readEntries 读取条目文件并按 ID 聚合——同 ID 多行取 v 最大者；坏行具名出声后跳过（不中断全库）。
func (s *Service) readEntries(lib string) ([]Entry, int, string) {
	path := filepath.Join(s.root, lib, entriesFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, "库条目文件缺失: " + path
		}
		return nil, 0, "库条目文件读取失败: " + err.Error()
	}
	lines := splitLines(data)
	if len(lines) == 0 {
		return nil, 0, "库条目文件为空（缺头行）: " + path
	}
	// 头行校验——格式判据，不符整库拒绝读取
	var head map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		return nil, 0, "库头行解析失败（格式不符）: " + path
	}
	headFmt, _ := asInt(head["fmt"])
	headKind, _ := head["_"].(string)
	if headFmt != formatSupport || headKind != metaKind {
		return nil, 0, "库头行格式不符（期望 " + metaKind + " fmt=" + strconv.Itoa(formatSupport) + "）: " + path
	}
	latest := make(map[int]Entry, len(lines))
	badLines := 0
	for i := 1; i < len(lines); i++ {
		text := lines[i]
		if strings.TrimSpace(text) == "" {
			continue
		}
		entry, lineErr := parseEntry(text)
		if lineErr != "" {
			badLines++
			s.log(2, "条目行解析失败已跳过 | lib="+lib+" | 行="+strconv.Itoa(i+1)+" | "+lineErr)
			continue
		}
		if known, exists := latest[entry.ID]; !exists || entry.V > known.V {
			latest[entry.ID] = entry
		}
	}
	all := make([]Entry, 0, len(latest))
	for _, entry := range latest {
		all = append(all, entry)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, badLines, ""
}

// parseEntry 解析一行条目记录——坏行返回空原因（调用方出声跳过）。
func parseEntry(text string) (Entry, string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return Entry{}, "JSON 解析失败"
	}
	var id int
	if rawID, ok := raw["id"]; !ok || json.Unmarshal(rawID, &id) != nil {
		id = 0
	}
	var title string
	if rawTitle, ok := raw["title"]; ok {
		_ = json.Unmarshal(rawTitle, &title)
	}
	if id <= 0 || title == "" {
		return Entry{}, "缺 id 或 title"
	}
	entry := Entry{ID: id, Title: title, V: 1}
	if rawBody, ok := raw["body"]; ok {
		_ = json.Unmarshal(rawBody, &entry.Body)
	}
	if rawV, ok := raw["v"]; ok {
		var v int
		if json.Unmarshal(rawV, &v) == nil {
			entry.V = v
		}
	}
	if rawDel, ok := raw["del"]; ok {
		var del int
		if json.Unmarshal(rawDel, &del) == nil {
			entry.Del = del
		}
	}
	if rawCreated, ok := raw["createdAt"]; ok {
		_ = json.Unmarshal(rawCreated, &entry.CreatedAt)
	}
	if rawUpdated, ok := raw["updatedAt"]; ok {
		_ = json.Unmarshal(rawUpdated, &entry.UpdatedAt)
	}
	return entry, ""
}

// appendEntry 追加一行条目记录——UTF-8 无 BOM + LF，一次写整行。
func (s *Service) appendEntry(lib string, entry Entry) string {
	path := filepath.Join(s.root, lib, entriesFile)
	line := objectJSON(
		field{"id", entry.ID},
		field{"title", entry.Title},
		field{"body", entry.Body},
		field{"v", entry.V},
		field{"del", entry.Del},
		field{"createdAt", entry.CreatedAt},
		field{"updatedAt", entry.UpdatedAt},
	)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return "条目追加失败: " + err.Error()
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(line + "\n"); err != nil {
		return "条目追加失败: " + err.Error()
	}
	return ""
}

// [段6] 内部——注册表与工具

// readIndex 读库注册表——文件缺失视为空库表（首次建库形态）；解析失败或格式不符整表拒绝。
func (s *Service) readIndex() ([]Lib, string) {
	path := filepath.Join(s.root, indexFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ""
		}
		return nil, "库注册表读取失败: " + err.Error()
	}
	var raw struct {
		Fmt  int `json:"fmt"`
		Libs []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"createdAt"`
		} `json:"libs"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "库注册表解析失败: " + path
	}
	if raw.Fmt != formatSupport {
		return nil, "库注册表格式不符（fmt=" + strconv.Itoa(raw.Fmt) + "，期望 " + strconv.Itoa(formatSupport) + "）"
	}
	libs := make([]Lib, 0, len(raw.Libs))
	for _, item := range raw.Libs {
		if item.ID == "" {
			continue
		}
		libs = append(libs, Lib{ID: item.ID, CreatedAt: item.CreatedAt})
	}
	return libs, ""
}

// writeIndex 写库注册表——原子写（先落临时文件再替换）。
func (s *Service) writeIndex(libs []Lib) string {
	items := make([]string, 0, len(libs))
	for _, lib := range libs {
		items = append(items, objectJSON(field{"id", lib.ID}, field{"createdAt", lib.CreatedAt}))
	}
	json := objectJSON(field{"fmt", formatSupport}, field{"libs", rawJSON("[" + strings.Join(items, ",") + "]")})
	if err := atomicWrite(filepath.Join(s.root, indexFile), json); err != nil {
		return "库注册表写入失败: " + err.Error()
	}
	return ""
}

// findLib 按 ID 查库登记——大小写不敏感；未登记返回 nil。
func findLib(libs []Lib, lib string) *Lib {
	for i := range libs {
		if strings.EqualFold(libs[i].ID, lib) {
			return &libs[i]
		}
	}
	return nil
}

// findActiveByTitle 按标题查活跃条目——完全匹配（忽略大小写：唯一键口径，写入 / 定位共用）；未命中返回 nil。
func findActiveByTitle(all []Entry, title string) *Entry {
	for i := range all {
		if all[i].Del == 0 && strings.EqualFold(all[i].Title, title) {
			return &all[i]
		}
	}
	return nil
}

// findActiveByID 按 ID 查活跃条目——墓碑行视为不存在。
func findActiveByID(all []Entry, id int) *Entry {
	for i := range all {
		if all[i].Del == 0 && all[i].ID == id {
			return &all[i]
		}
	}
	return nil
}

// validateTitleBody 标题与正文硬限校验——按 Unicode 字符数计（非字节）。
func validateTitleBody(title, body string, checkTitle bool) *Result {
	if checkTitle {
		count := charCount(title)
		if count == 0 {
			fail := Fail("BAD_ARGS", "标题不能为空")
			return &fail
		}
		if count > titleMax {
			fail := Fail("DB_TITLE_TOO_LONG", "标题超长（上限 "+strconv.Itoa(titleMax)+" 字符，实际 "+strconv.Itoa(count)+"）")
			return &fail
		}
		if hasControlChar(title) {
			fail := Fail("BAD_ARGS", "标题不得含控制字符（换行 / 制表 / 其他 <0x20）")
			return &fail
		}
	}
	if trimText(body) == "" {
		fail := Fail("BAD_ARGS", "正文不能为空")
		return &fail
	}
	if count := charCount(body); count > bodyMax {
		fail := Fail("DB_BODY_TOO_LONG", "正文超长（上限 "+strconv.Itoa(bodyMax)+" 字符，实际 "+strconv.Itoa(count)+"）")
		return &fail
	}
	return nil
}

// charCount Unicode 字符数——按码点计数（组合字符按码点计；对齐口径见 L3 H 域已知边界）。
func charCount(text string) int {
	if text == "" {
		return 0
	}
	return len([]rune(text))
}

// hasControlChar 是否含控制字符——C0（小于 0x20）与 DEL（0x7F）；标题面禁（单行键），正文面允许。
func hasControlChar(text string) bool {
	for _, r := range text {
		if r < 0x20 || r == 0x7F {
			return true
		}
	}
	return false
}

// normalizeLibID 库名归一——去空白 + 转小写。
func normalizeLibID(libID string) string {
	return strings.ToLower(strings.TrimSpace(libID))
}

// trimText 文本归一——去两端空白。
func trimText(text string) string {
	return strings.TrimSpace(text)
}

// nowStamp 当前时刻——`yyyy-MM-dd HH:mm:ss`（字典序即时间序）。
func nowStamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// tryParseInt 整数解析——对齐 .NET int.TryParse（首尾空白容忍 + 正负号 + 溢出拒绝）。
func tryParseInt(text string) (int, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false
	}
	return value, true
}

// containsFold 子串匹配（忽略大小写）——对齐 .NET IndexOf(OrdinalIgnoreCase) 的逐字符口径。
func containsFold(haystack, needle string) bool {
	hay := []rune(haystack)
	pattern := []rune(needle)
	if len(pattern) == 0 {
		return true
	}
	if len(pattern) > len(hay) {
		return false
	}
	for start := 0; start+len(pattern) <= len(hay); start++ {
		matched := true
		for i := 0; i < len(pattern); i++ {
			if !strings.EqualFold(string(hay[start+i]), string(pattern[i])) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// splitLines 行切分——对齐 File.ReadAllLines（\r\n / \n / \r 三态；末尾换行不产生空行）。
func splitLines(data []byte) []string {
	text := string(data)
	if text == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(text, "\n")+1)
	var current strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch c {
		case '\r':
			lines = append(lines, current.String())
			current.Reset()
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
		case '\n':
			lines = append(lines, current.String())
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

// [段7] 内部——JSON 与落盘

// field 是 JSON 对象的一个键值对（值可为原始值或 rawJSON 片段）。
type field struct {
	// Key 键名
	Key string
	// Value 值（rawJSON 作片段嵌入，其余走标准编码）
	Value any
}

// rawJSON 是已编码的 JSON 片段（不再二次转义）。
type rawJSON string

// objectJSON 按字段序构造紧凑 JSON 对象——键走 quote，值走 scalarJSON（中文直出、HTML 敏感字符不转义）。
func objectJSON(fields ...field) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(quote(f.Key))
		b.WriteByte(':')
		b.WriteString(scalarJSON(f.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// scalarJSON 单值编码——rawJSON 直出；字符串走 quote；布尔与数值直出；nil → null。
func scalarJSON(value any) string {
	switch v := value.(type) {
	case rawJSON:
		return string(v)
	case string:
		return quote(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case nil:
		return "null"
	}
	return marshalRelaxed(value)
}

// quote 字符串字面量——中文直出、HTML 敏感字符不转义（对齐 JsonUtil.Str / TextUtil.JsonEscape）。
func quote(s string) string {
	return marshalRelaxed(s)
}

// marshalRelaxed 标准 JSON 编码（关闭 HTML 转义、中文直出、去尾换行）。
func marshalRelaxed(value any) string {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "null"
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// atomicWrite 原子写——临时文件 + 改名（UTF-8 无 BOM；对齐 ConfigStore.AtomicWrite）。
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// asInt 把 JSON 解码出的数值转 int（float64 形态）。
func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	}
	return 0, false
}

// [段8] 库级授权策略

// policyGate 保护策略表（读 = 积木执行，写 = 宿主装配）。
var policyGate sync.Mutex

// policyByCat 每猫白名单——catID → 允许库集合（空集合 = 无库权限）。
var policyByCat = make(map[string]map[string]bool)

// policyAllLibs 全放行猫（默认猫 majordomo）。
var policyAllLibs = make(map[string]bool)

// RegisterCat 登记每猫库白名单——cat.cfg 装配 / catcfg.apply 时调用（覆盖已存在同 ID）。
func RegisterCat(catID string, allLibs bool, libs []string) {
	if catID == "" {
		return
	}
	set := make(map[string]bool, len(libs))
	for _, lib := range libs {
		normalized := normalizeLibID(lib)
		if normalized != "" {
			set[normalized] = true
		}
	}
	policyGate.Lock()
	policyByCat[catID] = set
	if allLibs {
		policyAllLibs[catID] = true
	} else {
		delete(policyAllLibs, catID)
	}
	policyGate.Unlock()
}

// UnregisterCat 注销每猫库白名单——cat.delete 时调用。
func UnregisterCat(catID string) {
	if catID == "" {
		return
	}
	policyGate.Lock()
	delete(policyByCat, catID)
	delete(policyAllLibs, catID)
	policyGate.Unlock()
}

// ClearPolicy 清空策略表（测试隔离用）。
func ClearPolicy() {
	policyGate.Lock()
	policyByCat = make(map[string]map[string]bool)
	policyAllLibs = make(map[string]bool)
	policyGate.Unlock()
}

// IsAllowed 库是否对猫放行——判定唯一出口（空 catID = 无猫上下文放行）。
func IsAllowed(catID, libID string) bool {
	return DenyReason(catID, libID) == ""
}

// DenyReason 库访问拒绝原因——空串 = 放行；非空 = 拒绝文案（具名出声，供工具回执）。
//
// 语义（对齐 C# DbAccessPolicy · fail-closed）：空 catID = 无猫上下文（管理端点 / 单测 / CLI）放行；
// 已登记猫按白名单逐库判定（空集合 = 无库权限）；未登记猫 = 无权限（拒，失败可见）。
func DenyReason(catID, libID string) string {
	cat := catID
	if cat == "" {
		return ""
	}
	lib := normalizeLibID(libID)
	policyGate.Lock()
	defer policyGate.Unlock()
	if policyAllLibs[cat] {
		return ""
	}
	set, exists := policyByCat[cat]
	if !exists {
		return "猫未登记库权限（" + cat + "）——cat.cfg dbLibs 缺失或策略未加载"
	}
	if len(set) == 0 {
		return "猫无库权限（" + cat + "）——cat.cfg dbLibs 为空"
	}
	if !set[lib] {
		return "库未授权（" + lib + " 不在猫 " + cat + " 的 dbLibs 白名单）"
	}
	return ""
}
