package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
)

// probeAgent 在事件流被消费期间模拟「真正执行」，用于观察并发峰值。
// 执行放在 goroutine 里：agentWithIdentity 的锁必须覆盖事件流，而不是只覆盖 Run 返回。
type probeAgent struct {
	cur *int32
	max *int32
}

func (p *probeAgent) Name(context.Context) string        { return "probe" }
func (p *probeAgent) Description(context.Context) string { return "probe" }

func (p *probeAgent) Run(_ context.Context, _ *adk.AgentInput,
	_ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	out, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer gen.Close()
		n := atomic.AddInt32(p.cur, 1)
		for {
			old := atomic.LoadInt32(p.max)
			if n <= old || atomic.CompareAndSwapInt32(p.max, old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(p.cur, -1)
		gen.Send(&adk.AgentEvent{})
	}()
	return out
}

// drain 消费到事件流结束。
func drain(iter *adk.AsyncIterator[*adk.AgentEvent]) {
	for {
		if _, ok := iter.Next(); !ok {
			return
		}
	}
}

// TestAgentWithIdentitySharesLock 同一把锁上的两次 dispatch 串行（峰值并发=1）。
func TestAgentWithIdentitySharesLock(t *testing.T) {
	var cur, peak int32
	var lock sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := &agentWithIdentity{
				Agent: &probeAgent{cur: &cur, max: &peak}, agentID: "a1", lock: &lock,
			}
			drain(a.Run(context.Background(), &adk.AgentInput{}))
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&peak); got != 1 {
		t.Fatalf("同一锁的 dispatch 应串行，峰值并发=%d", got)
	}
}

// TestAgentWithIdentityNilLockParallel 无锁时并行（explorer 等不注册会话的路径）。
func TestAgentWithIdentityNilLockParallel(t *testing.T) {
	var cur, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := &agentWithIdentity{Agent: &probeAgent{cur: &cur, max: &peak}, agentID: "explorer"}
			drain(a.Run(context.Background(), &adk.AgentInput{}))
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&peak); got < 2 {
		t.Fatalf("无锁时应可并行，峰值并发=%d", got)
	}
}
