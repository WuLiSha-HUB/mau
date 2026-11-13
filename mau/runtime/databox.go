package runtime

import (
	"reflect"
	"sort"
	"sync"
)

// DataBox 是三区数据中台：服务区（类型单例）、数据区（scope/key）、信号区（原子沿）。
// 并发契约：数据区与信号区可跨 goroutine 读写；业务状态由驱动 goroutine 独占。
type DataBox struct {
	mu       sync.RWMutex
	services map[reflect.Type]any
	data     map[string]map[string]any
	signals  map[string]bool
}

// NewDataBox 创建空数据中台。
func NewDataBox() *DataBox {
	return &DataBox{
		services: make(map[reflect.Type]any),
		data:     make(map[string]map[string]any),
		signals:  make(map[string]bool),
	}
}

// Bind 绑定服务实例——类型单例，重复绑定即替换。
func (d *DataBox) Bind(instance any) {
	if instance == nil {
		return
	}
	t := reflect.TypeOf(instance)
	d.mu.Lock()
	d.services[t] = instance
	d.mu.Unlock()
}

// TryResolve 解析服务实例；未绑定返回 false。
func (d *DataBox) TryResolve(target any) bool {
	t := reflect.TypeOf(target)
	if t == nil {
		return false
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	d.mu.RLock()
	v, ok := d.services[t]
	d.mu.RUnlock()
	if !ok {
		return false
	}
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return false
	}
	rv.Elem().Set(reflect.ValueOf(v))
	return true
}

// Entry 是数据区一条盒子（观测面投影用——值为原始值，摘要化归消费侧）。
type Entry struct {
	// Scope 盒子域
	Scope string
	// Key 盒子键
	Key string
	// Value 原始值
	Value any
}

// Set 写入数据区——覆盖已有值。
func (d *DataBox) Set(scope, key string, value any) {
	d.mu.Lock()
	m, ok := d.data[scope]
	if !ok {
		m = make(map[string]any)
		d.data[scope] = m
	}
	m[key] = value
	d.mu.Unlock()
}

// Get 读取数据区。
func (d *DataBox) Get(scope, key string) (any, bool) {
	d.mu.RLock()
	m, ok := d.data[scope]
	if !ok {
		d.mu.RUnlock()
		return nil, false
	}
	v, ok := m[key]
	d.mu.RUnlock()
	return v, ok
}

// GetString 读取字符串值（缺失返回空串）。
func (d *DataBox) GetString(scope, key string) string {
	v, ok := d.Get(scope, key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetBool 读取布尔值（缺失返回 false）。
func (d *DataBox) GetBool(scope, key string) bool {
	v, ok := d.Get(scope, key)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// Remove 删除数据区条目——不存在静默成功；返回是否真的删除了条目（C# DataBox.Remove 同口径）。
//
// 与 Set false 的语义差异：删除后 Get 未命中，消费侧「盒不存在 = 未采样」的条件天然为假；
// 写 false 则是「采样过且为假」——会话边界信号清理用本原语，不用覆盖写。
func (d *DataBox) Remove(scope, key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.data[scope]
	if !ok {
		return false
	}
	_, exists := m[key]
	if exists {
		delete(m, key)
	}
	return exists
}

// Signal 置位事件沿——消费前多次投递合并。
func (d *DataBox) Signal(name string) {
	d.mu.Lock()
	d.signals[name] = true
	d.mu.Unlock()
}

// TryPeek 预检事件沿——不消费。
func (d *DataBox) TryPeek(name string) bool {
	d.mu.RLock()
	v := d.signals[name]
	d.mu.RUnlock()
	return v
}

// TryPoll 消费事件沿——置位返回 true 并清除。
func (d *DataBox) TryPoll(name string) bool {
	d.mu.Lock()
	v := d.signals[name]
	if v {
		d.signals[name] = false
	}
	d.mu.Unlock()
	return v
}

// Capture 返回数据区全量截面（scope 字典序 + key 字典序——map 遍历顺序随机，输出需确定性）。
//
// 返回值为独立切片；Value 是原始值（写入方语义：Set 后不再原地修改，跨 goroutine 读安全）。
func (d *DataBox) Capture() []Entry {
	d.mu.RLock()
	out := make([]Entry, 0, len(d.data))
	for scope, m := range d.data {
		for key, value := range m {
			out = append(out, Entry{Scope: scope, Key: key, Value: value})
		}
	}
	d.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out
}
