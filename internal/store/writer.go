package store

import (
	"log/slog"
	"sync"
)

// AsyncWriter 异步写入器：写操作经单 goroutine 串行提交，不阻塞对话主链路；
// 落库失败仅记 slog 警告（可观测性数据允许偶发丢失，FR-5.2）。
type AsyncWriter struct {
	store  Store
	ops    chan func(Store) error
	done   chan struct{}
	wg     sync.WaitGroup
	closed bool
	mu     sync.Mutex
}

// NewAsyncWriter 启动后台写入循环。
func NewAsyncWriter(s Store, buffer int) *AsyncWriter {
	if buffer <= 0 {
		buffer = 256
	}
	w := &AsyncWriter{
		store: s,
		ops:   make(chan func(Store) error, buffer),
		done:  make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *AsyncWriter) loop() {
	defer w.wg.Done()
	for {
		select {
		case op := <-w.ops:
			if err := op(w.store); err != nil {
				slog.Warn("store 异步写入失败", "err", err)
			}
		case <-w.done:
			return
		}
	}
}

// Submit 提交一个写操作（非阻塞；队列满时丢弃并告警）。
func (w *AsyncWriter) Submit(op func(Store) error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	select {
	case w.ops <- op:
	default:
		slog.Warn("store 写入队列已满，操作被丢弃")
	}
}

// Close 停止接收新操作，排空队列后返回（不关闭底层 Store）。
func (w *AsyncWriter) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	close(w.done)
	w.wg.Wait()
	// 排空残留
	for {
		select {
		case op := <-w.ops:
			if err := op(w.store); err != nil {
				slog.Warn("store 排空写入失败", "err", err)
			}
		default:
			return
		}
	}
}

// Store 底层只读访问（查询用；写请走 Submit）。
func (w *AsyncWriter) Store() Store { return w.store }
