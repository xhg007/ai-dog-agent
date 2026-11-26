package agent

import (
	"sync"
	"sync/atomic"
	"testing"

	"ai-dog-agent/internal/config"
)

// TestClaimSession_OnlyOneWinner 并发占用同一会话时只能有一个成功。
// 这是 SendMessage「检查 running + 占用」必须原子化的回归测试：
// 若退回两次临界区（先检查、解锁、再占用），本测试会稳定出现多个成功者。
func TestClaimSession_OnlyOneWinner(t *testing.T) {
	svc := &ChatService{sessions: map[string]*chatSession{"s1": {id: "s1"}}}

	const n = 64
	var (
		wg    sync.WaitGroup
		wins  atomic.Int32
		start = make(chan struct{})
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.claimSession("s1"); err == nil {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("并发占用应只有 1 个成功，got %d", got)
	}
}

// TestClaimSession_ReleaseAllowsReclaim 释放后可再次占用。
func TestClaimSession_ReleaseAllowsReclaim(t *testing.T) {
	svc := &ChatService{sessions: map[string]*chatSession{"s1": {id: "s1"}}}
	sess, err := svc.claimSession("s1")
	if err != nil {
		t.Fatalf("首次占用失败: %v", err)
	}
	if _, err := svc.claimSession("s1"); err == nil {
		t.Fatal("占用期间不应再次成功")
	}
	svc.releaseSession(sess)
	if _, err := svc.claimSession("s1"); err != nil {
		t.Fatalf("释放后应可再次占用: %v", err)
	}
}

// TestClaimSession_UnknownSession 不存在的会话返回错误。
func TestClaimSession_UnknownSession(t *testing.T) {
	svc := &ChatService{sessions: map[string]*chatSession{}}
	if _, err := svc.claimSession("nope"); err == nil {
		t.Fatal("未知会话应返回错误")
	}
}

// TestConfigChanged 配置版本变化后判定为需要重建 Agent。
func TestConfigChanged(t *testing.T) {
	cfg, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	svc := &ChatService{cfg: cfg}
	sess := &chatSession{configVersion: cfg.Version()}
	if svc.configChanged(sess) {
		t.Fatal("配置未变更时不应重建")
	}
	if err := cfg.Save(config.Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !svc.configChanged(sess) {
		t.Fatal("保存后应重建 Agent")
	}
}