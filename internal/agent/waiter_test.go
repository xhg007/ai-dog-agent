package agent

import (
	"testing"
	"time"
)

func TestAsyncWaiterTimeoutAndCompletion(t *testing.T) {
	w := &asyncWaiter{}
	w.Add()
	if w.Wait(20 * time.Millisecond) {
		t.Fatal("未 Done 时 Wait 应超时返回 false")
	}
	w.Done()
	if !w.Wait(time.Second) {
		t.Fatal("Done 后 Wait 应完成")
	}
}

func TestAsyncWaiterZeroTimeoutBlocksUntilDone(t *testing.T) {
	w := &asyncWaiter{}
	w.Add()
	done := make(chan struct{})
	go func() {
		w.Wait(0)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("timeout=0 时不应在 Done 前返回")
	case <-time.After(20 * time.Millisecond):
	}

	w.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Done 后应返回")
	}
}
