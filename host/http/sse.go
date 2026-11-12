package http

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// sseCapacity 是每连接的帧队列容量——满时丢最旧（DropOldest），慢客户端
// 因此看见帧序号落差；这是可观测信号，不改成阻塞或扩队。
const sseCapacity = 512

// sseClient 是一条 SSE 连接。
//
// 对齐 C# 版 SseClient：容量 512 / DropOldest / 单读者 / 订阅过滤不消耗帧序号 /
// 帧序号 per-连接（取号与入队同在锁内）。
type sseClient struct {
	w       http.ResponseWriter
	flusher http.Flusher
	// topics 订阅集合；nil = 全量订阅（无 ?topics= 参数）
	topics map[string]bool

	mu  sync.Mutex
	seq int
	// queue 是帧队列（容量 512）
	queue chan string
}

// newSSEClient 构造连接。
func newSSEClient(w http.ResponseWriter, flusher http.Flusher, topics map[string]bool) *sseClient {
	return &sseClient{w: w, flusher: flusher, topics: topics, queue: make(chan string, sseCapacity)}
}

// Wants 判定是否订阅某话题；nil 订阅集合视为全量。
func (c *sseClient) Wants(topic string) bool {
	if c.topics == nil {
		return true
	}
	return c.topics[topic]
}

// NextFrame 取号并封帧——单帧四行：id / event / data / 空行，换行符 `\n`。
//
// 取号与入队都在锁内完成，保证 per-连接序号顺序不乱。
func (c *sseClient) NextFrame(event, data string) string {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	return "id: " + strconv.Itoa(seq) + "\nevent: " + event + "\ndata: " + data + "\n\n"
}

// enqueue 入队一帧；队列满时丢最旧（DropOldest 的等价实现）。
func (c *sseClient) enqueue(frame string) {
	select {
	case c.queue <- frame:
		return
	default:
	}
	// 满——先出队一条最旧，再入队（单写者场景下的 DropOldest 近似）
	select {
	case <-c.queue:
	default:
	}
	select {
	case c.queue <- frame:
	default:
	}
}

// send 按订阅过滤后入队；未订阅不消耗帧序号。
func (c *sseClient) send(topic, data string) {
	if !c.Wants(topic) {
		return
	}
	c.enqueue(c.NextFrame(topic, data))
}

// serve 写响应头并消费队列直到客户端断开。
func (c *sseClient) serve(r *http.Request) {
	c.w.Header().Set("Content-Type", "text/event-stream")
	c.w.Header().Set("Cache-Control", "no-cache")
	c.w.Header().Set("Connection", "keep-alive")
	c.w.WriteHeader(http.StatusOK)
	c.flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-c.queue:
			if _, err := c.w.Write([]byte(frame)); err != nil {
				return
			}
			c.flusher.Flush()
		}
	}
}

// parseTopics 解析 ?topics=a,b,c；空参数返回 nil（= 全量订阅）。
func parseTopics(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			set[t] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// Broadcast 向全部连接推送一帧（按各自订阅过滤）。
func (s *Server) Broadcast(topic, data string) {
	s.clientsLock.Lock()
	clients := make([]*sseClient, len(s.clients))
	copy(clients, s.clients)
	s.clientsLock.Unlock()
	for _, c := range clients {
		c.send(topic, data)
	}
}

// ClientCount 当前 SSE 连接数（state 段 conn.clients 数据源）。
func (s *Server) ClientCount() int {
	s.clientsLock.Lock()
	defer s.clientsLock.Unlock()
	return len(s.clients)
}

// addClient 注册连接并补首帧（仅补给新客户端，不广播已有连接）。
//
// 🔴 首帧载荷在 clientsLock 之外构建——视图全量构建会回读连接数（ClientCount 同锁），
// Go 互斥锁不可重入（C# Monitor 可重入故无此约束）——持锁构建即自锁。
func (s *Server) addClient(c *sseClient) {
	// 首帧补发：snapshot / state / view 三段，各自按订阅过滤（构建先于登记）
	var frames []string
	if c.Wants("snapshot") {
		if snap := s.currentSnapshot(); snap != "" {
			frames = append(frames, c.NextFrame("snapshot", snap))
		}
	}
	if c.Wants("state") {
		if state, _ := s.stateCache.Load().(string); state != "" {
			frames = append(frames, c.NextFrame("state", state))
		}
	}
	if c.Wants("view") && s.viewBuilder != nil {
		if full := s.viewBuilder(); full != "" {
			frames = append(frames, c.NextFrame("view", full))
		}
	}
	s.clientsLock.Lock()
	s.clients = append(s.clients, c)
	for _, frame := range frames {
		c.enqueue(frame)
	}
	s.clientsLock.Unlock()
}

// removeClient 静默移除连接。
func (s *Server) removeClient(c *sseClient) {
	s.clientsLock.Lock()
	for i, existing := range s.clients {
		if existing == c {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			break
		}
	}
	s.clientsLock.Unlock()
}
