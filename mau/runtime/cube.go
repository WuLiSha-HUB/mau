package runtime

// CubeMode 是时限步进模式。
type CubeMode int

const (
	// CubeTotal 达到上限即过期。
	CubeTotal CubeMode = iota
	// CubeIdle 未启动也计帧。
	CubeIdle
	// CubeInfinite 永不超时。
	CubeInfinite
)

// Cube 是帧步进的确定性时限原语。
type Cube struct {
	limit   int64
	mode    CubeMode
	elapsed int64
	started bool
	expired bool
	done    bool
}

// NewCube 创建时限 Cube。
func NewCube(limit int64, mode CubeMode) *Cube {
	return &Cube{limit: limit, mode: mode}
}

// Start 起算——清零已用帧。
func (c *Cube) Start() {
	c.started = true
	c.elapsed = 0
	c.expired = false
	c.done = false
}

// TickFrame 推进一帧。
func (c *Cube) TickFrame() {
	if c.mode == CubeInfinite || c.done {
		return
	}
	if !c.started && c.mode == CubeTotal {
		return
	}
	c.elapsed++
	if c.mode != CubeInfinite && c.limit > 0 && c.elapsed > c.limit {
		c.expired = true
	}
}

// IsExpired 是否已超时。
func (c *Cube) IsExpired() bool {
	return c.expired
}

// Complete 标记完成。
func (c *Cube) Complete() {
	c.done = true
}

// Reset 复位。
func (c *Cube) Reset() {
	c.started = false
	c.elapsed = 0
	c.expired = false
	c.done = false
}

// Elapsed 已用帧数。
func (c *Cube) Elapsed() int64 {
	return c.elapsed
}
