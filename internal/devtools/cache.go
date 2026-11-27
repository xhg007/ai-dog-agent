// 轻量 TTL 缓存：替代 go-dev-mcp 使用的 patrickmn/go-cache，
// 只保留文档查询需要的最小能力（按 key 存取 + 过期时间）。
package devtools

import (
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

type ttlCache[T any] struct {
	mu    sync.RWMutex
	items map[string]cacheEntry[T]
	ttl   time.Duration
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{items: make(map[string]cacheEntry[T]), ttl: ttl}
}

// get 返回未过期的缓存值；过期条目顺手删除。
func (c *ttlCache[T]) get(key string) (T, bool) {
	var zero T
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return zero, false
	}
	if time.Now().After(entry.expires) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return zero, false
	}
	return entry.value, true
}

func (c *ttlCache[T]) set(key string, value T) {
	c.mu.Lock()
	c.items[key] = cacheEntry[T]{value: value, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// docCache 文档解析结果缓存（与 go-dev-mcp 一致的 30 分钟）。
// 缓存键按来源加前缀：godoc: / rustdoc: / pydoc:。
var docCache = newTTLCache[string](30 * time.Minute)
