package llm

// 图片通道降级状态——主模型请求体里的图像内容块（image_url）可用性单点。
//
// 为什么有这个（2026-11-14 实测）：本地网关 WorkBuddyShell（127.0.0.1:18100）对请求体
// 有硬上限——探针 0.5/1/2/3 MB 正常响应，4 MB / 6 MB 直接 ConnectionAborted 10053
// （对端掐连接，不返 4xx）。而前文里的图片每轮请求都会重新内联展开，一旦图片把请求体
// 顶过线，重试与自动续跑只会重复撞同一堵墙（R喵喵 判例：3 张图 ≈ 3.75 MB → 48 次请求）。
//
// 处置链：
//  ① 请求体预算（request.go）：构造前估算体积，超限先从最旧的图开始摘（保留最新）；
//  ② 带图请求仍传输失败 → 本状态标记降级，冷却期内所有请求一律摘图（会话可继续）；
//  ③ 降级期内 image-inject 直接拒绝并提示「当前视觉 API 不可用请绕行」
//     （bricks 侧经 ImageGate 读取本状态）。

import (
	"sync"
	"time"
)

// imageChannelCooldown 图片通道降级冷却——带图请求一次传输失败后的摘图窗口。
const imageChannelCooldown = 60 * time.Second

// ImageChannel 图片通道降级状态（进程级单点——同一上游、同一本地网关）。
type ImageChannel struct {
	// mu 读写锁——Mark / Down / Reset 跨线（轮执行线程 + 工具面）
	mu sync.RWMutex
	// until 降级到期时刻（零值 = 未降级）
	until time.Time
	// reason 降级原因（原始 ERR| 文本前 200 字符）
	reason string
}

// ImageChannelState 全局图片通道状态——宿主 LLM 面与工具面共用同一实例。
var ImageChannelState = &ImageChannel{}

// Mark 标记降级——记录原因与到期时刻（重复标记取较晚的到期与最新原因）。
func (c *ImageChannel) Mark(reason string, cooldown time.Duration) {
	if c == nil {
		return
	}
	if cooldown <= 0 {
		cooldown = imageChannelCooldown
	}
	until := time.Now().Add(cooldown)
	c.mu.Lock()
	if until.After(c.until) {
		c.until = until
	}
	if reason != "" {
		c.reason = reason
	}
	c.mu.Unlock()
}

// Down 查询降级态——返回（原因, 是否降级中）。
func (c *ImageChannel) Down() (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	until, reason := c.until, c.reason
	c.mu.RUnlock()
	if time.Now().Before(until) {
		return reason, true
	}
	return "", false
}

// ImageChannelDown 实现 bricks.ImageGate——宿主工具面（image-inject）读取降级态。
func (c *ImageChannel) ImageChannelDown() (string, bool) {
	return c.Down()
}

// RemainingSeconds 降级剩余秒数（未降级 = 0；向上取整，最小 1）。
func (c *ImageChannel) RemainingSeconds() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	until := c.until
	c.mu.RUnlock()
	remaining := time.Until(until)
	if remaining <= 0 {
		return 0
	}
	seconds := int(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// Reset 复位——带图请求成功后清空降级（半开自愈）。
func (c *ImageChannel) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.until = time.Time{}
	c.reason = ""
	c.mu.Unlock()
}
