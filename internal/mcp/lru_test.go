package mcp

import (
	"testing"
	"time"

	"ai-dog-agent/internal/config"
)

// seedSession 直接向 Manager 表中放入一个连接（不真正 dial），供回收逻辑测试。
func seedSession(m *Manager, id, name string, ago time.Duration, inflight int) *serverSession {
	ss := &serverSession{
		cfg:      config.MCPServerConfig{ID: id, Name: name},
		lastUsed: time.Now().Add(-ago),
		inflight: inflight,
	}
	m.sessions[id] = ss
	return ss
}

// TestPickVictimChoosesOldestIdle 回收最久未用的空闲连接，跳过在用连接。
func TestPickVictimChoosesOldestIdle(t *testing.T) {
	m := NewManager(nil)
	m.SetMaxConnections(2)
	busy := seedSession(m, "a", "busy", 10*time.Minute, 1)
	idle := seedSession(m, "b", "idle", 5*time.Minute, 0)
	seedSession(m, "c", "newer", time.Minute, 0)

	victim := m.pickVictimLocked("d")
	if victim != idle {
		t.Fatalf("应回收最久未用且空闲的连接 b，实际 %v", victim)
	}
	if _, ok := m.sessions["b"]; ok {
		t.Fatal("被回收的连接应从表中移除")
	}
	if _, ok := m.sessions["a"]; !ok {
		t.Fatal("在用连接 a 不应被回收")
	}
	if busy.inflight != 1 {
		t.Fatalf("在用计数不应变化: %d", busy.inflight)
	}
}

// TestPickVictimAllInflight 所有连接都在执行工具时不回收（本次放行）。
func TestPickVictimAllInflight(t *testing.T) {
	m := NewManager(nil)
	m.SetMaxConnections(2)
	seedSession(m, "a", "a", 10*time.Minute, 1)
	seedSession(m, "b", "b", 5*time.Minute, 2)

	if victim := m.pickVictimLocked("c"); victim != nil {
		t.Fatalf("无空闲连接时应放行，实际回收 %v", victim)
	}
	if len(m.sessions) != 2 {
		t.Fatalf("连接表不应变化，实际 %d", len(m.sessions))
	}
}

// TestPickVictimBelowLimit 未达上限时不回收。
func TestPickVictimBelowLimit(t *testing.T) {
	m := NewManager(nil)
	m.SetMaxConnections(3)
	seedSession(m, "a", "a", time.Minute, 0)
	if victim := m.pickVictimLocked("b"); victim != nil {
		t.Fatalf("未达上限不应回收，实际 %v", victim)
	}
}

// TestPickVictimUnlimited 上限 <=0 表示不限制。
func TestPickVictimUnlimited(t *testing.T) {
	m := NewManager(nil)
	m.SetMaxConnections(0)
	seedSession(m, "a", "a", time.Minute, 0)
	if victim := m.pickVictimLocked("b"); victim != nil {
		t.Fatalf("不限制时不应回收，实际 %v", victim)
	}
}

// TestBeginEndUseTracksInflight 使用计数成对增减，且不会减成负数。
func TestBeginEndUseTracksInflight(t *testing.T) {
	m := NewManager(nil)
	ss := seedSession(m, "a", "a", time.Minute, 0)

	m.beginUse(ss)
	if ss.inflight != 1 {
		t.Fatalf("beginUse 后应为 1，实际 %d", ss.inflight)
	}
	m.endUse(ss)
	if ss.inflight != 0 {
		t.Fatalf("endUse 后应为 0，实际 %d", ss.inflight)
	}
	m.endUse(ss)
	if ss.inflight != 0 {
		t.Fatalf("重复 endUse 不应减成负数，实际 %d", ss.inflight)
	}
	m.beginUse(nil)
	m.endUse(nil)
}

// TestBeginUseRefreshesLastUsed 使用连接会刷新 LRU 时间戳。
func TestBeginUseRefreshesLastUsed(t *testing.T) {
	m := NewManager(nil)
	ss := seedSession(m, "a", "a", time.Hour, 0)
	before := ss.lastUsed
	m.beginUse(ss)
	defer m.endUse(ss)
	if !ss.lastUsed.After(before) {
		t.Fatalf("beginUse 应刷新 lastUsed: before=%v after=%v", before, ss.lastUsed)
	}
}

// TestCloseSessionNilSafe 空连接关闭不 panic（测试构造/已失效连接）。
func TestCloseSessionNilSafe(t *testing.T) {
	closeSession(nil)
	closeSession(&serverSession{cfg: config.MCPServerConfig{Name: "no-session"}})
}
