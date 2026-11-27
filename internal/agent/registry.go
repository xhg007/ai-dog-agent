package agent

// AgentRegistry：按 (sessionID, agentID) 保持 work agent 的常驻会话。
// 用途：
//   - agent-to-agent 私聊（send_message）需要独立 Runner 与跨调用历史；
//   - 同一 (session, agent) 串行，避免私聊上下文竞争；
//   - 不同 Agent 各自持锁，可并行。
//
// dispatch_<name> 仍由 adk.NewAgentTool 生成，不经过 Registry 的私聊历史；
// 但它与私聊共用 agentSession.mu，同一 Agent 的两份执行串行（见设计文档阶段 F）。

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
)

// agentSession 一个 work agent 的私聊会话。
type agentSession struct {
	spec   agentspec.Spec
	model  config.ModelConfig // 注册时的实际模型配置（压缩与事件用）
	agent  adk.Agent
	runner *adk.Runner

	mu      sync.Mutex // 同一 (session, agent) 串行
	history []*schema.Message

	lastCompress time.Time // 上次压缩时间（冷却防抖）

	compressTokens int64   // 私聊压缩累计 token（预算判定）
	compressCost   float64 // 私聊压缩累计成本（元）
}

// Registry 会话级 Agent 注册表。
type Registry struct {
	mu       sync.Mutex
	bus      *eventbus.Bus
	chats    *agentChatStore                     // 私聊历史持久化（nil 时不落盘）
	sessions map[string]map[string]*agentSession // sessionID -> agentID -> session
}

// NewRegistry 创建注册表。
func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]map[string]*agentSession)}
}

// SetChatStore 注入私聊历史存储目录（会话根目录 sessions/）；为空时关闭持久化。
func (r *Registry) SetChatStore(sessionDir string) {
	r.mu.Lock()
	r.chats = newAgentChatStore(sessionDir)
	r.mu.Unlock()
}

// SetBus 注入事件总线（压缩事件推送用；测试可省略）。
func (r *Registry) SetBus(bus *eventbus.Bus) {
	r.mu.Lock()
	r.bus = bus
	r.mu.Unlock()
}

// Register 注册/覆盖一个 work agent 会话。
//   - 首次注册（应用启动后首次构建）：从磁盘恢复上次私聊历史（配置指纹不符则丢弃）；
//   - 覆盖注册（配置重建）：旧历史对新的提示词/工具集不再有意义，清空内存与磁盘。
func (r *Registry) Register(sessionID string, wa *WorkAgent) *agentSession {
	as := &agentSession{
		spec:  wa.Spec,
		model: wa.Model,
		agent: wa.Agent,
		runner: adk.NewRunner(context.Background(), adk.RunnerConfig{
			Agent:           wa.Agent,
			EnableStreaming: true,
		}),
	}
	r.mu.Lock()
	m := r.sessions[sessionID]
	if m == nil {
		m = make(map[string]*agentSession)
		r.sessions[sessionID] = m
	}
	_, existed := m[wa.Spec.AgentID]
	m[wa.Spec.AgentID] = as
	chats := r.chats
	r.mu.Unlock()

	switch {
	case chats == nil:
	case existed:
		chats.remove(sessionID, wa.Spec.AgentID)
	default:
		as.history = chats.load(sessionID, wa.Spec.AgentID, specFingerprint(wa.Spec))
		if len(as.history) > 0 {
			slog.Info("恢复私聊历史", "session", sessionID,
				"agent", wa.Spec.AgentID, "messages", len(as.history))
		}
	}
	return as
}

// Lookup 按 agentID → 名称 → dispatch 工具名解析目标。
func (r *Registry) Lookup(sessionID, ref string) (*agentSession, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.sessions[sessionID]
	if m == nil {
		return nil, false
	}
	if as, ok := m[ref]; ok {
		return as, true
	}
	for _, as := range m {
		if as.spec.Name == ref || DispatchToolName(as.spec) == ref {
			return as, true
		}
	}
	return nil, false
}

// RemoveSession 删除一个会话的全部 Agent 会话与私聊历史（会话删除/服务退出时调用）。
func (r *Registry) RemoveSession(sessionID string) {
	r.mu.Lock()
	delete(r.sessions, sessionID)
	chats := r.chats
	r.mu.Unlock()
	if chats != nil {
		chats.removeSession(sessionID)
	}
}

// Count 返回指定会话已注册的 Agent 数量（测试与诊断用）。
func (r *Registry) Count(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions[sessionID])
}

// AgentObservation 观测台展示的子 Agent 快照（只读，不含私聊历史与运行上下文）。
type AgentObservation struct {
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	ModelID   string `json:"modelId,omitempty"`
	Tool      string `json:"tool"` // dispatch_<name>，前端用它关联 tool_call/tool_result
}

// ListAll 返回所有会话已注册的子 Agent 快照，按会话、名称、agentId 排序。
func (r *Registry) ListAll() []AgentObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AgentObservation, 0)
	for sessionID, m := range r.sessions {
		for _, as := range m {
			out = append(out, AgentObservation{
				SessionID: sessionID,
				AgentID:   as.spec.AgentID,
				Name:      as.spec.Name,
				Model:     as.model.Name,
				ModelID:   as.model.ID,
				Tool:      DispatchToolName(as.spec),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].AgentID < out[j].AgentID
	})
	return out
}
