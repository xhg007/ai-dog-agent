package task

// 回归测试：任务终结后清理检查点序号计数器。

import (
	"testing"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
)

func TestFinishClearsCheckpointSeq(t *testing.T) {
	m, _, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, err := m.Start("s1", "x")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitEvent(t, ch, eventbus.EvTaskState)

	if got := m.NextCheckpointSeq(id); got != 1 {
		t.Fatalf("首个 seq 应为 1，实际 %d", got)
	}
	if got := m.NextCheckpointSeq(id); got != 2 {
		t.Fatalf("第二个 seq 应为 2，实际 %d", got)
	}

	if err := m.Finish("s1", id, store.StatusCompleted, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	m.mu.Lock()
	_, ok := m.seq[id]
	_, cur := m.current["s1"]
	m.mu.Unlock()
	if ok {
		t.Fatal("任务结束后应清理 seq 计数器")
	}
	if cur {
		t.Fatal("任务结束后应清理 current 映射")
	}
}
