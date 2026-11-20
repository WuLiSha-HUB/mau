package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cathome5/mau/fs"
)

// 本文件复现管理端段 8/9/10——打开数据目录 · 对话图片附件（上传 + 取图）· 音频资产取回。
//
// 契约对齐 `mau:CatHome4/CatHome4.Admin/AdminService.ChatImages.cs`（段 9 / 段 10 音频在
// AdminService.Audio.cs）与 `AdminService.Handlers.cs:HandleOpenDataDir`（段 8）：
// 图片内容寻址命名（sha256 前 16 位 + 扩展名，同图去重）· 取图三分支（受控根寻址 / 根内绝对路径 /
// 缓存目录裸名）· 音频只认 tts 产物目录裸名（最小可读面）· 打开数据目录由后端调 explorer.exe。

// 图片缓存目录白名单——Data 下相对名（换机不改配置）；首项为写入目录。
var imageDirs = []string{"chat-images"}

// imageWriteDir 是上传落点（imageDirs 首项）。
const imageWriteDir = "chat-images"

// maxImageBytes 是单图体积上限——10 MiB（与 QQ 文件通道口径对齐）。
const maxImageBytes = 10 * 1024 * 1024

// kokoroOutDirKey 是 TTS 产物目录配置键。
const kokoroOutDirKey = "py.service.kokoro.out_dir"

// chatImageResponse 是上传回执（ok / name / path / bytes / dedup）。
type chatImageResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Name 内容寻址文件名
	Name string `json:"name"`
	// Path 落盘绝对路径
	Path string `json:"path"`
	// Bytes 字节数
	Bytes int `json:"bytes"`
	// Dedup 是否去重命中（同内容已在盘）
	Dedup bool `json:"dedup"`
}

// openDataDirResponse 是打开数据目录回执（ok / path）。
type openDataDirResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Path 打开的数据根绝对路径
	Path string `json:"path"`
}

// handleOpenDataDir 服务 POST /api/v1/open-data-dir——explorer.exe 打开持久化数据根。
//
// 纯前端 JS 无法打开本地资源管理器（浏览器沙箱）——必须后端端点代开；
// 数据根由启动链三级锚定，前端零路径知识。
func (d *Deps) handleOpenDataDir(w http.ResponseWriter, r *http.Request) {
	dataDir := d.DataRoot
	if dataDir == "" {
		writeErr(w, "数据根未绑定")
		return
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		d.log(2, "打开数据目录失败："+err.Error())
		writeErr(w, err.Error())
		return
	}
	if err := d.openDirectory(dataDir); err != nil {
		d.log(2, "打开数据目录失败："+err.Error())
		writeErr(w, err.Error())
		return
	}
	d.log(1, "打开数据目录: "+dataDir)
	writeJSON(w, openDataDirResponse{OK: true, Path: dataDir})
}

// openDirectory 打开资源管理器（OpenDir 钩子可替换——单测不弹窗）。
func (d *Deps) openDirectory(path string) error {
	if d.OpenDir != nil {
		return d.OpenDir(path)
	}
	return exec.Command("explorer.exe", path).Start()
}

// handleChatImagesUpload 服务 POST /api/v1/chat-images——图片原始字节入库。
//
// 入口面零容忍：类型白名单 / 体积上限 / 空体拒绝——任一不成立即拒绝并给出原因（不静默）。
func (d *Deps) handleChatImagesUpload(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = contentType[:idx]
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	ext := imageExtOf(contentType)
	if ext == "" {
		d.logChat(2, "图片上传拒绝（类型不在白名单）："+contentType)
		writeErr(w, "不支持的图片类型："+contentType)
		return
	}
	if r.ContentLength > maxImageBytes {
		d.logChat(2, "图片上传拒绝（超上限）："+itoa(int(r.ContentLength))+" 字节")
		writeErr(w, "图片超过上限 "+itoa(maxImageBytes)+" 字节")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImageBytes+1))
	if err != nil {
		d.logChat(3, "图片上传失败（读取）："+err.Error())
		writeErr(w, "图片读取失败："+err.Error())
		return
	}
	if len(body) > maxImageBytes {
		d.logChat(2, "图片上传拒绝（读取越界上限）")
		writeErr(w, "图片超过上限 "+itoa(maxImageBytes)+" 字节")
		return
	}
	if len(body) == 0 {
		writeErr(w, "空图片内容")
		return
	}
	name := imageNameOf(body, ext)
	dir := filepath.Join(d.DataRoot, imageWriteDir)
	full := filepath.Join(dir, name)
	dedup := false
	if _, statErr := os.Stat(full); statErr == nil {
		dedup = true
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			d.logChat(3, "图片上传失败："+err.Error())
			writeErr(w, "图片落盘失败："+err.Error())
			return
		}
		// 先写临时名再改名——避免半截文件被当成完整图片
		temp := full + ".tmp"
		if err := os.WriteFile(temp, body, 0o644); err != nil {
			d.logChat(3, "图片上传失败："+err.Error())
			writeErr(w, "图片落盘失败："+err.Error())
			return
		}
		if err := os.Rename(temp, full); err != nil {
			_ = os.Remove(temp)
			// 并发同图——他人已落地即视为去重命中
			if _, statErr := os.Stat(full); statErr == nil {
				dedup = true
			} else {
				d.logChat(3, "图片上传失败："+err.Error())
				writeErr(w, "图片落盘失败："+err.Error())
				return
			}
		}
	}
	note := "图片上传：" + name + "（" + itoa(len(body)) + " 字节"
	if dedup {
		note += "，去重命中"
	}
	d.logChat(1, note+"）")
	writeJSON(w, chatImageResponse{OK: true, Name: name, Path: full, Bytes: len(body), Dedup: dedup})
}

// handleCacheImageGet 服务 GET /api/v1/cache-image/{file...}——取图三分支。
//
// ① 受控根寻址 `<rootId>:<rel>`——根内只读 + 图片扩展名白名单 + 越界拒绝；
// ② 根内绝对路径——视觉工具吃绝对路径（模型天然按绝对路径引用）；不在任何根内 → 回落文件名分支；
// ③ 缓存目录白名单（纯文件名）——只认文件名，白名单目录内查找。
// 设计取舍：不认白名单与受控根之外的路径——避免成为任意文件读取面。
func (d *Deps) handleCacheImageGet(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	// [段1] 受控根寻址分支
	if isRootAddressForm(file) {
		full, ok := d.resolveInsideRoots(file)
		if !ok || serveImage(w, full, false) != nil {
			notFound(w)
		}
		return
	}
	// [段2] 本地绝对路径分支——必须落在某个受控根内
	if filepath.IsAbs(file) {
		if full, ok := d.resolveInsideRoots(file); ok && serveImage(w, full, false) == nil {
			return
		}
		file = filepath.Base(file)
	}
	// [段3] 缓存目录白名单分支——纯文件名
	if !isSafeImageName(file) {
		notFound(w)
		return
	}
	for _, rel := range imageDirs {
		full := filepath.Join(d.DataRoot, rel, file)
		if serveImage(w, full, true) == nil {
			return
		}
	}
	notFound(w)
}

// handleAudioGet 服务 GET /api/v1/audio/{file...}——只认 tts 产物目录下的裸文件名（.wav）。
//
// 不认路径分隔符 / 不认受控根寻址 / 不认绝对路径——避免成为任意文件读取面。
func (d *Deps) handleAudioGet(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if !isSafeAudioName(file) {
		notFound(w)
		return
	}
	dir := ""
	if d.Store != nil {
		dir = d.Store.Resolve(d.Schema, kokoroOutDirKey).Value
	}
	if dir == "" {
		notFound(w)
		return
	}
	full := filepath.Join(dir, file)
	data, err := os.ReadFile(full)
	if err != nil {
		notFound(w)
		return
	}
	// 文件名按轮次唯一（summary_<轮次戳>_<猫>.wav）——内容不可变，可长缓存
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", "audio/wav")
	_, _ = w.Write(data)
}

// serveImage 图片响应单一出口——存在性检查 + 扩展名白名单 + 缓存头 + 字节响应。
//
// 缓存语义：受控根内产物同名覆盖是常态（跑测出图）→ 禁缓存；
// 缓存目录内容寻址不可变 → 长缓存。返回 nil 表示已响应；非 nil 表示未响应（调用方继续找 / 404）。
func serveImage(w http.ResponseWriter, full string, contentAddressed bool) error {
	mime := imageMimeOfExt(filepath.Ext(full))
	if mime == "application/octet-stream" {
		return errText("非图片扩展名")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	if contentAddressed {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
	return nil
}

// notFound 写 404 空体（对齐 C# Results.NotFound()）。
func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}

// resolveInsideRoots 解析并校验受控根归属（根内只读）。
func (d *Deps) resolveInsideRoots(path string) (string, bool) {
	if d.Workspace == nil {
		return "", false
	}
	svc := &fs.Service{Roots: d.Workspace.FSRoots()}
	full, err := svc.Resolve(path, false)
	if err != nil {
		return "", false
	}
	return full, true
}

// isRootAddressForm 根寻址形态判定——冒号在首个路径分隔符之前（盘符不算）。
func isRootAddressForm(path string) bool {
	colon := strings.IndexByte(path, ':')
	if colon <= 0 {
		return false
	}
	sep := strings.IndexAny(path, "/\\")
	if sep >= 0 && sep < colon {
		return false
	}
	if colon == 1 && sep == 2 {
		return false
	}
	return true
}

// isSafeImageName 文件名安全判定——纯文件名（非空 / 无分隔符 / 无上级引用）。
func isSafeImageName(file string) bool {
	if file == "" {
		return false
	}
	if strings.ContainsAny(file, `/\`) {
		return false
	}
	if strings.Contains(file, "..") {
		return false
	}
	return filepath.Base(file) == file
}

// isSafeAudioName 音频文件名安全检查——裸名 + .wav 扩展 + 长度上限。
func isSafeAudioName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(name), ".wav")
}

// imageExtOf Content-Type → 扩展名（白名单；未命中返回空串 = 拒绝上传）。
func imageExtOf(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp", "image/x-ms-bmp":
		return ".bmp"
	}
	return ""
}

// imageMimeOfExt 扩展名 → Content-Type（未命中回落 application/octet-stream）。
func imageMimeOfExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	}
	return "application/octet-stream"
}

// imageNameOf 内容寻址文件名——sha256 前 8 字节（16 位十六进制） + 扩展名。
func imageNameOf(data []byte, ext string) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:8]) + ext
}

// logChat 记一条 CHAT 类观测日志。
func (d *Deps) logChat(level int, message string) {
	if d.Logf == nil {
		return
	}
	d.Logf("CHAT", level, message)
}

// ── 对话文件附件（任意类型）────────────────────────────────────────
//
// 与图片通道（上段）平行的第二条附件通道：任意类型文件随消息发送。
// 差异：① 原名经 X-File-Name 传入（图片是纯内容寻址）；② 落盘名 = 内容寻址前缀 + 安全原名（可读）；
// ③ 取文件端点只服务文件缓存目录裸名（不做受控根分支——避免成为任意文件读取面）。

// 文件缓存目录白名单——Data 下相对名（换机不改配置）；首项为写入目录。
var fileDirs = []string{"chat-files"}

// fileWriteDir 是文件上传落点（fileDirs 首项）。
const fileWriteDir = "chat-files"

// maxFileBytes 是单文件体积上限——50 MiB（通用文件口径；图片仍走 10 MiB 图片通道）。
const maxFileBytes = 50 * 1024 * 1024

// maxFileNameBytes 是安全原名的字节上限——留出 hash 前缀与分隔符后落盘名仍可读。
const maxFileNameBytes = 120

// fileUploadResponse 是文件上传回执（ok / name / path / bytes / dedup）。
type fileUploadResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Name 落盘文件名（内容寻址前缀 + 安全原名）
	Name string `json:"name"`
	// Path 落盘绝对路径
	Path string `json:"path"`
	// Bytes 字节数
	Bytes int `json:"bytes"`
	// Dedup 是否去重命中（同内容同名字已在盘）
	Dedup bool `json:"dedup"`
}

// handleChatFilesUpload 服务 POST /api/v1/chat-files——任意类型文件原始字节入库。
//
// 原名经 `X-File-Name`（URL 编码，HTTP 头只允许 ASCII）传入；缺失时回落 `file`。
// 入口面零容忍：空体 / 超上限 / 读越界一律拒绝并给原因（失败必须可见）。
func (d *Deps) handleChatFilesUpload(w http.ResponseWriter, r *http.Request) {
	rawName := strings.TrimSpace(r.Header.Get("X-File-Name"))
	if rawName != "" {
		if unescaped, err := url.QueryUnescape(rawName); err == nil {
			rawName = unescaped
		}
	}
	if r.ContentLength > maxFileBytes {
		d.logChat(2, "文件上传拒绝（超上限）："+itoa(int(r.ContentLength))+" 字节")
		writeErr(w, "文件超过上限 "+itoa(maxFileBytes)+" 字节")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFileBytes+1))
	if err != nil {
		d.logChat(3, "文件上传失败（读取）："+err.Error())
		writeErr(w, "文件读取失败："+err.Error())
		return
	}
	if len(body) > maxFileBytes {
		d.logChat(2, "文件上传拒绝（读取越界上限）")
		writeErr(w, "文件超过上限 "+itoa(maxFileBytes)+" 字节")
		return
	}
	if len(body) == 0 {
		writeErr(w, "空文件内容")
		return
	}
	safe := sanitizeFileName(rawName)
	name := fileArtifactName(body, safe)
	dir := filepath.Join(d.DataRoot, fileWriteDir)
	full := filepath.Join(dir, name)
	dedup := false
	if _, statErr := os.Stat(full); statErr == nil {
		dedup = true
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			d.logChat(3, "文件上传失败："+err.Error())
			writeErr(w, "文件落盘失败："+err.Error())
			return
		}
		// 先写临时名再改名——避免半截文件被当成完整文件
		temp := full + ".tmp"
		if err := os.WriteFile(temp, body, 0o644); err != nil {
			d.logChat(3, "文件上传失败："+err.Error())
			writeErr(w, "文件落盘失败："+err.Error())
			return
		}
		if err := os.Rename(temp, full); err != nil {
			_ = os.Remove(temp)
			// 并发同文件——他人已落地即视为去重命中
			if _, statErr := os.Stat(full); statErr == nil {
				dedup = true
			} else {
				d.logChat(3, "文件上传失败："+err.Error())
				writeErr(w, "文件落盘失败："+err.Error())
				return
			}
		}
	}
	note := "文件上传：" + name + "（" + itoa(len(body)) + " 字节"
	if dedup {
		note += "，去重命中"
	}
	d.logChat(1, note+"）")
	writeJSON(w, fileUploadResponse{OK: true, Name: name, Path: full, Bytes: len(body), Dedup: dedup})
}

// sanitizeFileName 原名清洗——去目录部分、替换非法字符、去前导点、限长；空名回落 `file`。
//
// 判据：结果不含路径分隔符 / 冒号 / 控制字符；以 `.` 开头一律剥掉（防隐藏文件与穿越）。
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == ".." || name == "/" {
		return "file"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		case r == '\\' || r == '/' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	out = strings.TrimSpace(out)
	if out == "" {
		return "file"
	}
	// 限长（按字节退到合法 UTF-8 边界）
	for len(out) > maxFileNameBytes {
		out = out[:len(out)-1]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	if out == "" {
		return "file"
	}
	return out
}

// fileArtifactName 内容寻址文件名——sha256 前 8 字节（16 位十六进制）+ `_` + 安全原名。
//
// 同内容同名字 → 同一文件（去重）；同内容不同名 → 两个文件（保留原名可读性）。
func fileArtifactName(data []byte, safeName string) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:8]) + "_" + safeName
}

// handleCacheFileGet 服务 GET /api/v1/cache-file/{file...}——只认文件缓存目录内裸名。
//
// 设计取舍：不开放受控根分支与任意扩展名——本端点只服务「对话附件」这一个面，
// 避免成为任意文件读取入口（与 cache-image 的三分支不同，这里刻意收窄）。
func (d *Deps) handleCacheFileGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !isSafeArtifactName(name) {
		notFound(w)
		return
	}
	for _, rel := range fileDirs {
		full := filepath.Join(d.DataRoot, rel, name)
		if serveFile(w, full) == nil {
			return
		}
	}
	notFound(w)
}

// isSafeArtifactName 落盘文件名安全判定——裸名 / 无分隔符 / 无上级引用 / 有长度上限。
func isSafeArtifactName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return filepath.Base(name) == name
}

// serveFile 文件响应单一出口——存在即读；Content-Type 按扩展名；图片 / 文本 / PDF 内联，其余下载。
//
// 返回 nil 表示已响应；非 nil 表示未响应（调用方继续找 / 404）。
func serveFile(w http.ResponseWriter, full string) error {
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	ext := filepath.Ext(full)
	if fileInlineExt(ext) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(full)))
	}
	// 内容寻址前缀不可变 → 长缓存
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", fileMimeOfExt(ext))
	_, _ = w.Write(data)
	return nil
}

// fileMimeOfExt 扩展名 → Content-Type（常见类型表；未命中回落 application/octet-stream）。
func fileMimeOfExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".yaml", ".yml":
		return "text/yaml; charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".7z":
		return "application/x-7z-compressed"
	case ".rar":
		return "application/vnd.rar"
	case ".gz":
		return "application/gzip"
	case ".tar":
		return "application/x-tar"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".mp4":
		return "video/mp4"
	}
	return "application/octet-stream"
}

// fileInlineExt 内联预览判定——图片 / 文本类 / PDF 内联，其余按附件下载。
func fileInlineExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".svg",
		".txt", ".log", ".md", ".json", ".csv", ".xml", ".yaml", ".yml",
		".pdf":
		return true
	}
	return false
}
