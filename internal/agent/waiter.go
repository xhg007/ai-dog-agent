package agent

// asyncWaiter 等待一组后台 goroutine（流式 usage 采集、delta 旁路）结束。
// Wait 带超时兜底：流副本未关闭时只放弃等待，不阻塞对话终态。

import (
	"sync"
	"time"
)

type asyncWaiter struct {
	wg sync.WaitGroup
}

func (w *asyncWaiter) Add()  { w.wg.Add(1) }
func (w *asyncWaiter) Done() { w.wg.Done() }

// Wait 等待计数归零；timeout<=0 表示不设上限。超时返回 false。
func (w *asyncWaiter) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
