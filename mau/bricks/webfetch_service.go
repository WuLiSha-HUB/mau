package bricks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WebFetchTask 是下载任务实体——任务表行（持久化单元）。
//
// 运行态（取消句柄）不入表——崩溃 / 停机后按 State 与 .part 字节续传。
type WebFetchTask struct {
	// ID 任务号（自增，落盘续号）
	ID int64
	// URL 源地址
	URL string
	// FileName 目标文件名（派生后）
	FileName string
	// TargetPath 产物绝对路径
	TargetPath string
	// CatKey 来源猫（通知注入收件人）
	CatKey string
	// State Pending / Downloading / Completed / Failed / Canceled
	State string
	// TotalBytes 总长（-1 = 未知）
	TotalBytes int64
	// ReceivedBytes 已收字节
	ReceivedBytes int64
	// HTTPStatus HTTP 状态码（0 = 未响应）
	HTTPStatus int
	// Error 失败原因（终态；原文）
	Error string
	// CreatedAt 登记时刻（Unix 毫秒）
	CreatedAt int64
	// FinishedAt 终结时刻（Unix 毫秒；0 = 未终结）
	FinishedAt int64
	// Notified 通知已产生（终态后置位——防重复投递）
	Notified bool
	// Delivered 通知已投递 / 无需投递
	Delivered bool
}

// WebFetchService 是网页资产下载服务——web-fetch / web-fetch-jobs 工具的执行面。
//
// 形态对齐 C# WebFetchService：全局任务表（全猫共用）+ 每任务一个后台 goroutine +
// 静态客户端（不设超时）；任务态原子落盘、跨重启续传（Range 续 / 服务端不支持则重下）；
// 完成 / 失败回调注入（拒收者留存重试）。
type WebFetchService struct {
	// mu 表锁——全部读写锁内完成
	mu sync.Mutex
	// tasks 任务表
	tasks []*WebFetchTask
	// tokens 运行态取消句柄——任务号 → 取消函数（不入表）
	tokens map[int64]context.CancelFunc
	// nextID 任务号自增位
	nextID int64
	// storePath 任务表落盘路径（空 = 仅内存）
	storePath string
	// downloadsDir 产物目录
	downloadsDir string
	// inject 完成注入回调（catKey, content）→ 是否受理
	inject func(catKey, content string) bool
	// pumpStop 通知泵停止信号（nil = 未启动）
	pumpStop chan struct{}
	// pumpStopDone 通知泵停止同步
	pumpStopDone sync.Once
	// logf 日志出口（nil = 不出声）
	logf func(level int, message string)
	// client HTTP 客户端（不设超时——下载任务无上限，取消走上下文）
	client *http.Client
}

// webFetchPumpMs 是通知泵扫描周期（毫秒）。
const webFetchPumpMs = 300

// webFetchReadBuffer 是读取缓冲字节数。
const webFetchReadBuffer = 81920

// NewWebFetchService 构造下载服务（未接线前仅内存表）。
func NewWebFetchService() *WebFetchService {
	return &WebFetchService{
		tokens: make(map[int64]context.CancelFunc),
		nextID: 1,
		client: &http.Client{Timeout: 0},
	}
}

// Configure 配置接线——任务表落盘路径 + 产物目录 + 注入回调（宿主启动期注入）。
func (s *WebFetchService) Configure(storePath, downloadsDir string, inject func(catKey, content string) bool) {
	s.mu.Lock()
	s.storePath = storePath
	s.downloadsDir = downloadsDir
	s.mu.Unlock()
	s.inject = inject
}

// SetLogger 设置日志出口（装配期注入；nil = 静默）。
func (s *WebFetchService) SetLogger(logf func(level int, message string)) {
	s.logf = logf
}

// Start 启动恢复——载入任务表 → 未完成任务续传 → 起通知泵。
func (s *WebFetchService) Start() {
	s.load()
	s.mu.Lock()
	pending := make([]int64, 0, len(s.tasks))
	for _, task := range s.tasks {
		if !isWebFetchTerminal(task.State) {
			pending = append(pending, task.ID)
		}
	}
	s.mu.Unlock()
	for _, id := range pending {
		s.log(1, fmt.Sprintf("下载续传: id=#%d", id))
		s.startTask(id)
	}
	stop := make(chan struct{})
	s.pumpStop = stop
	go s.pumpLoop(stop)
}

// Shutdown 收尾——停通知泵 + 取消全部在途任务（不置终态：半截 .part 留待续传）。
func (s *WebFetchService) Shutdown() {
	if s.pumpStop != nil {
		s.pumpStopDone.Do(func() { close(s.pumpStop) })
		s.pumpStop = nil
	}
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.tokens))
	for _, cancel := range s.tokens {
		cancels = append(cancels, cancel)
	}
	s.tokens = make(map[int64]context.CancelFunc)
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Fetch 推入下载任务——登记（含冲突检查）→ 起后台下载 → 立即返回任务号。
func (s *WebFetchService) Fetch(catKey, rawURL, fileName string) string {
	if catKey == "" {
		return "ERR|DOWNLOAD_ARGS|缺少来源猫（catKey）"
	}
	if rawURL == "" {
		return "ERR|DOWNLOAD_BAD_URL|缺少参数 url"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return "ERR|DOWNLOAD_BAD_URL|URL 非法: " + rawURL
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "ERR|DOWNLOAD_BAD_URL|仅支持 http/https: " + rawURL
	}
	name := fileName
	if name == "" {
		name = deriveFileName(rawURL)
	}
	dir := s.downloadsDir
	if dir == "" {
		return "ERR|DOWNLOAD_TARGET|下载落点未配置（宿主未接线 downloadsDir）"
	}
	target := filepath.Join(dir, name)
	s.mu.Lock()
	if fileExists(target) || fileExists(target+".part") {
		s.mu.Unlock()
		return "ERR|DOWNLOAD_NAME_CONFLICT|目标同名文件已存在: " + name + "（改名或清理后重试）"
	}
	for _, row := range s.tasks {
		if !isWebFetchTerminal(row.State) && row.URL == rawURL && strings.EqualFold(row.TargetPath, target) {
			id := row.ID
			s.mu.Unlock()
			return fmt.Sprintf("ERR|DOWNLOAD_EXISTS|同源同目标任务已在进行: #%d", id)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.mu.Unlock()
		return "ERR|DOWNLOAD_TARGET|下载目录创建失败: " + err.Error()
	}
	task := &WebFetchTask{
		ID:         s.nextID,
		URL:        rawURL,
		FileName:   name,
		TargetPath: target,
		CatKey:     catKey,
		State:      "Pending",
		TotalBytes: -1,
		CreatedAt:  nowMillis(),
	}
	s.nextID++
	s.tasks = append(s.tasks, task)
	s.saveLocked()
	s.mu.Unlock()
	s.log(1, fmt.Sprintf("下载登记: id=#%d | cat=%s | %s", task.ID, catKey, rawURL))
	s.startTask(task.ID)
	return fmt.Sprintf("任务 #%d 已登记 | %s | 下载中（完成或失败时系统提示）→ %s", task.ID, task.FileName, task.TargetPath)
}

// Jobs 任务管理——list（全部任务）/ status（单任务详情）/ cancel（取消进行中任务）。
func (s *WebFetchService) Jobs(action, id string) string {
	act := action
	if act == "" {
		act = "list"
	}
	if act == "list" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.tasks) == 0 {
			return "（无下载任务）"
		}
		lines := make([]string, 0, len(s.tasks))
		for _, row := range s.tasks {
			lines = append(lines, listLine(row))
		}
		return strings.Join(lines, "\n")
	}
	taskID, parseErr := atoiInt64(id)
	if parseErr != nil {
		return "ERR|DOWNLOAD_ARGS|缺少或非法参数 id（status / cancel 必填任务号）"
	}
	switch act {
	case "status":
		s.mu.Lock()
		defer s.mu.Unlock()
		row := s.findLocked(taskID)
		if row == nil {
			return fmt.Sprintf("ERR|DOWNLOAD_NO_TASK|任务不存在: #%d", taskID)
		}
		return detailText(row)
	case "cancel":
		return s.cancelTask(taskID)
	default:
		return "ERR|DOWNLOAD_ARGS|未知动作: " + act + "（list|status|cancel）"
	}
}

// startTask 起后台下载——登记取消句柄并投递（同任务不重复起）。
func (s *WebFetchService) startTask(taskID int64) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if _, running := s.tokens[taskID]; running {
		s.mu.Unlock()
		cancel()
		return
	}
	s.tokens[taskID] = cancel
	s.mu.Unlock()
	go s.runTask(ctx, taskID)
}

// runTask 后台下载主体——请求（带 Range 续传）→ 流式落 .part → 成功后改名 → 置终态。
func (s *WebFetchService) runTask(ctx context.Context, taskID int64) {
	s.mu.Lock()
	task := s.findLocked(taskID)
	if task == nil {
		s.mu.Unlock()
		return
	}
	task.State = "Downloading"
	s.saveLocked()
	taskURL := task.URL
	targetPath := task.TargetPath
	s.mu.Unlock()

	part := targetPath + ".part"
	succeeded := false
	failReason := ""
	func() {
		existing := int64(0)
		if info, statErr := os.Stat(part); statErr == nil {
			existing = info.Size()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, taskURL, nil)
		if err != nil {
			failReason = "InvalidDataException: " + err.Error()
			return
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
		if existing > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
		}
		resp, err := s.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				failReason = "已取消"
				return
			}
			failReason = "HttpRequestException: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		s.mu.Lock()
		if row := s.findLocked(taskID); row != nil {
			row.HTTPStatus = resp.StatusCode
		}
		s.mu.Unlock()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			failReason = fmt.Sprintf("HTTP %d %s", resp.StatusCode, httpReasonPhrase(resp.Status, resp.StatusCode))
			return
		}
		appendMode := existing > 0 && resp.StatusCode == http.StatusPartialContent
		start := int64(0)
		if appendMode {
			start = existing
		}
		total := int64(-1)
		if resp.ContentLength >= 0 {
			total = start + resp.ContentLength
		}
		s.mu.Lock()
		if row := s.findLocked(taskID); row != nil {
			row.TotalBytes = total
			row.ReceivedBytes = start
		}
		s.mu.Unlock()

		flag := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flag |= os.O_APPEND
		} else {
			flag |= os.O_TRUNC
		}
		sink, openErr := os.OpenFile(part, flag, 0o644)
		if openErr != nil {
			failReason = "IOException: " + openErr.Error()
			return
		}
		buffer := make([]byte, webFetchReadBuffer)
		for {
			read, readErr := resp.Body.Read(buffer)
			if read > 0 {
				if _, writeErr := sink.Write(buffer[:read]); writeErr != nil {
					_ = sink.Close()
					failReason = "IOException: " + writeErr.Error()
					return
				}
				s.mu.Lock()
				if row := s.findLocked(taskID); row != nil {
					row.ReceivedBytes += int64(read)
				}
				s.mu.Unlock()
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = sink.Close()
				if ctx.Err() != nil {
					failReason = "已取消"
					return
				}
				failReason = "IOException: " + readErr.Error()
				return
			}
		}
		if closeErr := sink.Close(); closeErr != nil {
			failReason = "IOException: " + closeErr.Error()
			return
		}
		if ctx.Err() != nil {
			failReason = "已取消"
			return
		}
		_ = os.Remove(targetPath)
		if moveErr := os.Rename(part, targetPath); moveErr != nil {
			failReason = "IOException: " + moveErr.Error()
			return
		}
		succeeded = true
	}()
	s.finish(taskID, succeeded, failReason)
}

// finish 置终态——成功 / 失败 / 取消三态收口 + 终结时刻 + 落盘；取消句柄出表。
func (s *WebFetchService) finish(taskID int64, ok bool, failReason string) {
	s.mu.Lock()
	task := s.findLocked(taskID)
	if task == nil {
		s.mu.Unlock()
		return
	}
	delete(s.tokens, taskID)
	if task.State == "Canceled" {
		task.FinishedAt = nowMillis()
		s.saveLocked()
		s.mu.Unlock()
		return
	}
	if ok {
		task.State = "Completed"
	} else {
		task.State = "Failed"
		task.Error = failReason
	}
	task.FinishedAt = nowMillis()
	s.saveLocked()
	state, fileName, errorText, received := task.State, task.FileName, task.Error, task.ReceivedBytes
	s.mu.Unlock()
	if state == "Completed" {
		s.log(1, fmt.Sprintf("下载完成: id=#%d | %s | %s", taskID, fileName, formatBytes(received)))
		return
	}
	s.log(3, fmt.Sprintf("下载失败: id=#%d | %s | %s", taskID, fileName, errorText))
}

// pumpLoop 通知泵——周期调度单次扫描。
func (s *WebFetchService) pumpLoop(stop chan struct{}) {
	ticker := time.NewTicker(webFetchPumpMs * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.pumpOnce()
		}
	}
}

// pumpOnce 单次扫描——终态即产生通知；未投递通知就地投递，拒收者留存下轮重试。
func (s *WebFetchService) pumpOnce() {
	due := make([]*WebFetchTask, 0, 4)
	s.mu.Lock()
	changed := false
	for _, row := range s.tasks {
		if isWebFetchTerminal(row.State) && !row.Notified && row.FinishedAt > 0 {
			row.Notified = true
			if row.State == "Canceled" {
				row.Delivered = true
			}
			changed = true
		}
		if row.Notified && !row.Delivered {
			due = append(due, row)
		}
	}
	if changed {
		s.saveLocked()
	}
	s.mu.Unlock()
	if len(due) == 0 {
		return
	}
	inject := s.inject
	if inject == nil {
		return
	}
	for _, row := range due {
		accepted := false
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.log(3, fmt.Sprintf("下载通知投递异常: id=#%d | %v", row.ID, recovered))
				}
			}()
			accepted = inject(row.CatKey, buildNotifyText(row))
		}()
		if accepted {
			s.mu.Lock()
			row.Delivered = true
			s.saveLocked()
			s.mu.Unlock()
		}
	}
}

// cancelTask 取消任务——在途置 Canceled 并触发取消；终态任务不可取消。
func (s *WebFetchService) cancelTask(taskID int64) string {
	s.mu.Lock()
	row := s.findLocked(taskID)
	if row == nil {
		s.mu.Unlock()
		return fmt.Sprintf("ERR|DOWNLOAD_NO_TASK|任务不存在: #%d", taskID)
	}
	if isWebFetchTerminal(row.State) {
		state := row.State
		s.mu.Unlock()
		return fmt.Sprintf("ERR|DOWNLOAD_NO_TASK|任务已终结，不可取消: #%d（%s）", taskID, state)
	}
	row.State = "Canceled"
	row.FinishedAt = nowMillis()
	row.Notified = true
	row.Delivered = true
	fileName := row.FileName
	s.saveLocked()
	cancel := s.tokens[taskID]
	delete(s.tokens, taskID)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.log(1, fmt.Sprintf("下载取消: id=#%d | %s", taskID, fileName))
	return fmt.Sprintf("已取消 #%d | %s", taskID, fileName)
}

// load 载入任务表——文件缺失 / 解析失败按空表继续（出声，不阻断）。
func (s *WebFetchService) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storePath == "" || !fileExists(s.storePath) {
		return
	}
	raw, err := os.ReadFile(s.storePath)
	if err != nil {
		s.log(3, "下载任务表加载失败（按空表继续）: "+err.Error())
		return
	}
	var doc struct {
		NextID int64            `json:"nextId"`
		Tasks  []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(trimBOMBytes(raw), &doc); err != nil {
		s.log(3, "下载任务表加载失败（按空表继续）: "+err.Error())
		return
	}
	if doc.NextID > s.nextID {
		s.nextID = doc.NextID
	}
	for _, item := range doc.Tasks {
		if _, ok := item["id"]; !ok {
			continue
		}
		row := parseWebFetchTask(item)
		s.tasks = append(s.tasks, row)
		if row.ID >= s.nextID {
			s.nextID = row.ID + 1
		}
	}
	s.log(1, fmt.Sprintf("下载任务表已加载：%d 条（%s）", len(s.tasks), s.storePath))
}

// saveLocked 落盘——锁内调用（原子写：临时文件 + 替换）。
func (s *WebFetchService) saveLocked() {
	if s.storePath == "" {
		return
	}
	items := make([]string, 0, len(s.tasks))
	for _, row := range s.tasks {
		items = append(items, "{"+strings.Join([]string{
			`"id":` + int64Text(row.ID),
			`"url":` + jsonString(row.URL),
			`"fileName":` + jsonString(row.FileName),
			`"targetPath":` + jsonString(row.TargetPath),
			`"catKey":` + jsonString(row.CatKey),
			`"state":` + jsonString(row.State),
			`"totalBytes":` + int64Text(row.TotalBytes),
			`"receivedBytes":` + int64Text(row.ReceivedBytes),
			`"httpStatus":` + int64Text(int64(row.HTTPStatus)),
			`"error":` + jsonString(row.Error),
			`"createdAt":` + int64Text(row.CreatedAt),
			`"finishedAt":` + int64Text(row.FinishedAt),
			`"notified":` + boolText(row.Notified),
			`"delivered":` + boolText(row.Delivered),
		}, ",")+"}")
	}
	payload := `{"version":1,"nextId":` + int64Text(s.nextID) + `,"tasks":[` + strings.Join(items, ",") + `]}`
	if dir := filepath.Dir(s.storePath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	temp := s.storePath + ".tmp"
	if err := os.WriteFile(temp, []byte(payload), 0o644); err != nil {
		s.log(3, "下载任务表落盘失败: "+err.Error())
		return
	}
	_ = os.Remove(s.storePath)
	if err := os.Rename(temp, s.storePath); err != nil {
		s.log(3, "下载任务表落盘失败: "+err.Error())
	}
}

// findLocked 按任务号查表——锁内调用。
func (s *WebFetchService) findLocked(taskID int64) *WebFetchTask {
	for _, row := range s.tasks {
		if row.ID == taskID {
			return row
		}
	}
	return nil
}

// log 出声（nil 出口即静默）。
func (s *WebFetchService) log(level int, message string) {
	if s.logf != nil {
		s.logf(level, message)
	}
}

// parseWebFetchTask 任务行解析——缺省字段按初值。
func parseWebFetchTask(item map[string]any) *WebFetchTask {
	row := &WebFetchTask{TotalBytes: -1, State: "Pending"}
	row.ID = int64Of(item, "id", 0)
	row.URL = stringOf(item, "url")
	row.FileName = stringOf(item, "fileName")
	row.TargetPath = stringOf(item, "targetPath")
	row.CatKey = stringOf(item, "catKey")
	row.State = stringOf(item, "state")
	if row.State == "" {
		row.State = "Pending"
	}
	row.TotalBytes = int64Of(item, "totalBytes", -1)
	row.ReceivedBytes = int64Of(item, "receivedBytes", 0)
	row.HTTPStatus = int(int64Of(item, "httpStatus", 0))
	row.Error = stringOf(item, "error")
	row.CreatedAt = int64Of(item, "createdAt", 0)
	row.FinishedAt = int64Of(item, "finishedAt", 0)
	row.Notified = boolOf(item, "notified", false)
	row.Delivered = boolOf(item, "delivered", false)
	return row
}

// buildNotifyText 通知文案——完成 / 失败两形态。
func buildNotifyText(task *WebFetchTask) string {
	if task.State == "Completed" {
		return fmt.Sprintf("下载完成：#%d | %s | %s | 用时 %s → %s",
			task.ID, task.FileName, formatBytes(task.ReceivedBytes),
			formatDuration(task.FinishedAt-task.CreatedAt), task.TargetPath)
	}
	return fmt.Sprintf("下载失败：#%d | %s | %s | 源 %s", task.ID, task.FileName, task.Error, task.URL)
}

// listLine 清单行——任务号 / 状态 / 文件名 / 进度 / 来源猫。
func listLine(row *WebFetchTask) string {
	return fmt.Sprintf("#%d [%s] %s | %s | cat=%s", row.ID, row.State, row.FileName, formatProgress(row), row.CatKey)
}

// detailText 详情文本——单任务全字段。
func detailText(row *WebFetchTask) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("#%d [%s]\n", row.ID, row.State))
	b.WriteString("源: " + row.URL + "\n")
	b.WriteString("产物: " + row.TargetPath + "\n")
	b.WriteString("进度: " + formatProgress(row) + "\n")
	b.WriteString(fmt.Sprintf("HTTP: %d\n", row.HTTPStatus))
	b.WriteString("来源猫: " + row.CatKey + "\n")
	b.WriteString("登记: " + formatTime(row.CreatedAt))
	if row.FinishedAt > 0 {
		b.WriteString(" | 终结: " + formatTime(row.FinishedAt))
		b.WriteString(" | 用时 " + formatDuration(row.FinishedAt-row.CreatedAt))
	}
	if row.Error != "" {
		b.WriteString("\n失败: " + row.Error)
	}
	return b.String()
}

// formatProgress 进度文本——总长未知时只报已收；已知则给「已收 / 总长（百分比）」。
func formatProgress(row *WebFetchTask) string {
	if row.TotalBytes < 0 {
		return "大小未知（已收 " + formatBytes(row.ReceivedBytes) + "）"
	}
	percent := int64(0)
	if row.TotalBytes > 0 {
		percent = row.ReceivedBytes * 100 / row.TotalBytes
		if percent > 100 {
			percent = 100
		}
	}
	return formatBytes(row.ReceivedBytes) + " / " + formatBytes(row.TotalBytes) + "（" + int64Text(percent) + "%）"
}

// formatBytes 字节数格式化——B / KB / MB / GB（对齐 C# ToString("0.0")）。
func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return int64Text(bytes) + " B"
	}
	kb := float64(bytes) / 1024.0
	if kb < 1024 {
		return fmt.Sprintf("%.1f", kb) + " KB"
	}
	mb := kb / 1024.0
	if mb < 1024 {
		return fmt.Sprintf("%.1f", mb) + " MB"
	}
	return fmt.Sprintf("%.2f", mb/1024.0) + " GB"
}

// formatDuration 时长格式化——ms / s / min。
func formatDuration(ms int64) string {
	if ms < 1000 {
		return int64Text(ms) + "ms"
	}
	sec := float64(ms) / 1000.0
	if sec < 60 {
		return fmt.Sprintf("%.1f", sec) + "s"
	}
	return fmt.Sprintf("%.1f", sec/60.0) + "min"
}

// formatTime 时刻格式化——Unix 毫秒 → 本机时区可读文本。
func formatTime(unixMs int64) string {
	return time.UnixMilli(unixMs).Local().Format("2006-01-02 15:04:05")
}

// deriveFileName 文件名派生——取 URL 路径尾段并解码；非法字符清理后为空则回落 download。
func deriveFileName(rawURL string) string {
	name := rawURL
	if parsed, err := url.Parse(rawURL); err == nil {
		name = parsed.Path
	}
	if cut := strings.LastIndex(name, "/"); cut >= 0 {
		name = name[cut+1:]
	}
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune("<>:\"/\\|?*", r) {
			return -1
		}
		return r
	}, name)
	if cleaned == "" {
		return "download"
	}
	return cleaned
}

// isWebFetchTerminal 终态判定——Completed / Failed / Canceled。
func isWebFetchTerminal(state string) bool {
	return state == "Completed" || state == "Failed" || state == "Canceled"
}

// nowMillis 当前时刻——Unix 毫秒。
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// fileExists 文件存在判定（目录视为不存在）。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// trimBOMBytes 剥 UTF-8 BOM。
func trimBOMBytes(raw []byte) []byte {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}

// int64Text 十进制渲染。
func int64Text(value int64) string {
	return fmt.Sprintf("%d", value)
}

// boolText JSON 布尔字面量。
func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// atoiInt64 解析任务号（允许首尾空白）。
func atoiInt64(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("空任务号")
	}
	var value int64
	for _, ch := range trimmed {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("非法任务号")
		}
		value = value*10 + int64(ch-'0')
	}
	return value, nil
}

// stringOf 取字符串字段——非字符串 / 缺失返回空串。
func stringOf(item map[string]any, key string) string {
	if value, ok := item[key].(string); ok {
		return value
	}
	return ""
}

// int64Of 取长整字段——非数字 / 缺失返回兜底值。
func int64Of(item map[string]any, key string, fallback int64) int64 {
	if value, ok := item[key].(float64); ok {
		return int64(value)
	}
	return fallback
}

// boolOf 取布尔字段——非布尔 / 缺失返回兜底值。
func boolOf(item map[string]any, key string, fallback bool) bool {
	if value, ok := item[key].(bool); ok {
		return value
	}
	return fallback
}
