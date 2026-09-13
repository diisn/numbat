package events

import (
	"context"
	"reflect"
	"sync"
)

// Event 是事件接口。
type Event any

// Handler 是事件处理函数。
type Handler func(ctx context.Context, event Event) error

// Bus 是内存事件总线。
type Bus struct {
	mu       sync.RWMutex
	handlers map[reflect.Type][]Handler
}

// New 创建一个新的事件总线。
func New() *Bus {
	return &Bus{
		handlers: make(map[reflect.Type][]Handler),
	}
}

// Subscribe 注册一个事件处理函数。
func (b *Bus) Subscribe(eventType reflect.Type, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// Publish 发布一个事件，所有 handler 都会执行，即使某个 handler 失败。
// 返回第一个遇到的错误（如有），但不会因为单个 handler 失败而中断后续 handler。
func (b *Bus) Publish(ctx context.Context, event Event) error {
	t := reflect.TypeOf(event)

	b.mu.RLock()
	handlers := make([]Handler, len(b.handlers[t]))
	copy(handlers, b.handlers[t])
	b.mu.RUnlock()

	var firstErr error
	for _, h := range handlers {
		if err := h(ctx, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
