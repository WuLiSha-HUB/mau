package runtime

import "sync"

// Inbox 是跨线程双缓冲队列：后台 goroutine 只写，驱动帧排空。
type Inbox[T any] struct {
	mu    sync.Mutex
	items []T
}

// Enqueue 入队——任意 goroutine 可调。
func (i *Inbox[T]) Enqueue(v T) {
	i.mu.Lock()
	i.items = append(i.items, v)
	i.mu.Unlock()
}

// Drain 排空——驱动 goroutine 在帧内调用；回调异常不中断后续项。
func (i *Inbox[T]) Drain(fn func(T)) {
	i.mu.Lock()
	items := i.items
	i.items = nil
	i.mu.Unlock()
	for _, v := range items {
		fn(v)
	}
}

// Len 当前积压条数（观测用）。
func (i *Inbox[T]) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.items)
}
