package http

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// noCacheHeader 是静态面的默认缓存口径（index / css / js / pet / favicon）。
const noCacheHeader = "no-cache, no-store, must-revalidate"

// fontCacheHeader 是字体面的长缓存口径（URL 固定，换代随文件名变化）。
const fontCacheHeader = "public, max-age=604800"

// handleIndex 服务主页面——`/` 返回 index.html；chat 页模式返回 chat.html。
//
// 与 C# ServeIndex 一致：`__V__` 占位符替换为前端版本（cache-busting），
// 页面缺失返回明文提示而不是 404（可观测的部署问题）。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", noCacheHeader)
	fileName := "index.html"
	if s.opts.ChatPage != "" {
		fileName = s.opts.ChatPage
	}
	path := filepath.Join(s.opts.HTMLRoot, fileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("CH4 外观层——" + fileName + " 未找到: " + path + "（源码区 CatHome4/html/ 或部署区 html/ 需放置静态页）"))
		return
	}
	// 与 C# File.ReadAllText 对齐——UTF-8 BOM 不随响应体输出
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	body := strings.ReplaceAll(string(raw), "__V__", s.opts.Version)
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(body))
}

// handleStatic 返回某静态子目录的处理器（css / js / pet / fonts）。
//
// 路径穿越校验与 C# 一致：解析后的绝对路径必须仍在 html 根内，否则 404。
func (s *Server) handleStatic(subDir, mime string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// fonts 长缓存，其余禁缓存
		if subDir == "fonts" {
			w.Header().Set("Cache-Control", fontCacheHeader)
		} else {
			w.Header().Set("Cache-Control", noCacheHeader)
		}
		rest := r.PathValue("path")
		root, err := filepath.Abs(s.opts.HTMLRoot)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		full, err := filepath.Abs(filepath.Join(root, subDir, rest))
		if err != nil || !within(root, full) {
			http.NotFound(w, r)
			return
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(raw)
	}
}

// handleRootFile 服务 html 根顶层文件（favicon.ico 等）。
func (s *Server) handleRootFile(fileName, mime string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", noCacheHeader)
		root, err := filepath.Abs(s.opts.HTMLRoot)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		full, err := filepath.Abs(filepath.Join(root, fileName))
		if err != nil || !within(root, full) {
			http.NotFound(w, r)
			return
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(raw)
	}
}

// within 判定 child 是否位于 root 目录内（Windows 大小写不敏感）。
func within(root, child string) bool {
	root = strings.TrimRight(root, "/\\")
	if strings.EqualFold(child, root) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(child), strings.ToLower(root)+string(os.PathSeparator))
}
