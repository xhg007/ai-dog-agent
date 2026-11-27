package agent

// agent-smith（阶段 D）：应用级后台优化 Agent。
// 职责：读取失败/取消任务的事件与当前项目 Agent 配置，生成/修改 work agent Spec；
// 写配置必须经用户审批，保存前自动备份旧配置。
//
// smith 的 agent_id 应用级固定（%AppData%/ai-dog-agent/agent_smith_id），
// 请求体 user_id 使用当前项目的主 Agent ID。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/llm"
	"ai-dog-agent/internal/prompts"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
	"ai-dog-agent/internal/tools"
	"ai-dog-agent/internal/workspace"
)

// smithIDFile 应用级 smith agent_id 固化文件（位于配置目录）。
const smithIDFile = "agent_smith_id"

// SmithTrigger 一次优化触发。
type SmithTrigger struct {
	SessionID string // 审批与事件归属的会话
	TaskID    string // 触发任务（手动触发时可为空）
	MainID    string // 当前项目主 Agent ID，作为 user_id
}

// Smith 应用级后台优化 Agent。
type Smith struct {
	cfg   *config.Manager
	ws    *workspace.Manager
	appr  *approval.Manager
	tasks *task.Manager
	bus   *eventbus.Bus
	id    string

	mu      sync.Mutex
	running map[string]bool // projectRoot -> 运行中
}

// NewSmith 创建 smith；固定 ID 固化在配置目录的 agent_smith_id。
func NewSmith(cfg *config.Manager, ws *workspace.Manager, appr *approval.Manager,
	tasks *task.Manager, bus *eventbus.Bus) *Smith {
	s := &Smith{
		cfg: cfg, ws: ws, appr: appr, tasks: tasks, bus: bus,
		running: make(map[string]bool),
	}
	if dir, err := config.Dir(); err == nil {
		if id, err := agentspec.EnsureIDInFile(filepath.Join(dir, smithIDFile)); err == nil {
			s.id = id
		}
	}
	if s.id == "" {
		s.id = agentspec.NewAgentID() // 配置目录不可用时仅本次进程内稳定
	}
	return s
}

// ID 返回固定 smith agent_id（用于请求体与日志）。
func (s *Smith) ID() string { return s.id }

const (
	smithMaxEvents = 30  // 进入提示词的事件条数
	smithEventScan = 500 // 最多扫描的任务事件数
)

// SmithInput 一次优化的输入快照。
type SmithInput struct {
	Trigger     SmithTrigger
	Task        *store.Task
	Events      []store.TaskEvent
	Agents      []agentspec.Spec
	Tools       []tools.ToolOption
	Models      []config.ModelConfig
	Skills      []config.SkillConfig
	MCPServers  []config.MCPServerConfig
	ProjectRoot string
}

// collectInput 读取任务事件（最近 N 条）、项目 Agent 配置与 Skills/MCP 列表。
func (s *Smith) collectInput(tr SmithTrigger) (*SmithInput, error) {
	if s == nil || s.cfg == nil || s.ws == nil || !s.ws.IsSet() {
		return nil, workspace.ErrNoWorkspace
	}
	root := s.ws.Root()
	in := &SmithInput{Trigger: tr, ProjectRoot: root}
	agents, err := agentspec.List(root)
	if err != nil {
		return nil, fmt.Errorf("读取项目 Agent 配置失败: %w", err)
	}
	in.Agents = agents
	in.Tools = tools.ToolOptions()
	eff := s.cfg.Effective(root)
	in.Models = eff.Models
	in.Skills = eff.Skills
	in.MCPServers = eff.MCPServers

	if s.tasks != nil {
		taskID := tr.TaskID
		if taskID == "" && tr.SessionID != "" {
			taskID = s.latestProblemTask(tr.SessionID)
		}
		if taskID != "" {
			if t, err := s.tasks.GetTask(taskID); err == nil {
				in.Task = t
			}
			if events, err := s.tasks.TaskEvents(taskID, smithEventScan); err == nil {
				in.Events = tailEvents(filterSmithEvents(events), smithMaxEvents)
			}
		}
	}
	return in, nil
}

// latestProblemTask 返回会话最近一个失败/取消/拒绝任务（手动触发时作为输入）。
func (s *Smith) latestProblemTask(sessionID string) string {
	tasks, err := s.tasks.ListTasks(50)
	if err != nil {
		return ""
	}
	for _, t := range tasks {
		if t.SessionID != sessionID {
			continue
		}
		switch store.NormalizeStatus(t.Status) {
		case store.StatusFailed, store.StatusCancelled, store.StatusRejected:
			return t.ID
		}
	}
	return ""
}

// filterSmithEvents 去掉用量噪音，保留状态/工具/错误/切换等对优化有用的信息。
func filterSmithEvents(events []store.TaskEvent) []store.TaskEvent {
	out := make([]store.TaskEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type == eventbus.EvLLMUsage {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// tailEvents 取尾部 n 条（TaskEvents 按 id 升序，最近事件在末尾）。
func tailEvents(events []store.TaskEvent, n int) []store.TaskEvent {
	if n <= 0 || len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

// SmithProposal smith 的结构化输出。
type SmithProposal struct {
	Action string         `json:"action"` // create | update | skip
	Name   string         `json:"name"`
	Spec   agentspec.Spec `json:"spec"`
	Reason string         `json:"reason"`
}

const (
	smithTimeout   = 120 * time.Second
	smithMaxTokens = 60_000
	smithMaxCost   = 0.4
)

// smithSystemData smith system 模板数据。
type smithSystemData struct {
	ToolNames  string
	ToolGroups string
}

// smithEventView 事件摘要（Payload 截断后交给模板）。
type smithEventView struct {
	Type    string
	Payload string
}

// smithSkillView Skill 摘要（Prompt 截断后交给模板）。
type smithSkillView struct {
	ID            string
	Name          string
	Description   string
	Enabled       bool
	PromptSummary string
}

// smithMCPView MCP 摘要（只暴露类型，不暴露 URL/Command）。
type smithMCPView struct {
	ID      string
	Name    string
	Kind    string
	Enabled bool
}

// smithPromptData smith user 模板数据。
type smithPromptData struct {
	SessionID  string
	TaskID     string
	Task       *store.Task
	Events     []smithEventView
	AgentsJSON string
	Tools      []tools.ToolOption
	Models     []config.ModelConfig
	Skills     []smithSkillView
	MCPServers []smithMCPView
}

// buildSmithPrompt 渲染 system（固定前缀）与 user（任务/配置快照）提示词。
// 文本位于 internal/prompts/smith_*.tmpl，这里只负责数据整形。
func buildSmithPrompt(in *SmithInput) (string, string, error) {
	system, err := prompts.Render("smith_system.tmpl", smithSystemData{
		ToolNames:  strings.Join(tools.KnownToolNames(), ", "),
		ToolGroups: strings.Join(tools.KnownToolGroups(), ", "),
	})
	if err != nil {
		return "", "", err
	}

	agentsJSON, err := json.MarshalIndent(in.Agents, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("序列化现有 Agent 配置失败: %w", err)
	}

	user, err := prompts.Render("smith_user.tmpl", smithPromptData{
		SessionID:  in.Trigger.SessionID,
		TaskID:     in.Trigger.TaskID,
		Task:       in.Task,
		Events:     smithEventViews(in.Events),
		AgentsJSON: string(agentsJSON),
		Tools:      in.Tools,
		Models:     in.Models,
		Skills:     smithSkillViews(in.Skills),
		MCPServers: smithMCPViews(in.MCPServers),
	})
	if err != nil {
		return "", "", err
	}
	return system, user, nil
}

func smithEventViews(events []store.TaskEvent) []smithEventView {
	out := make([]smithEventView, 0, len(events))
	for _, ev := range events {
		out = append(out, smithEventView{
			Type:    ev.Type,
			Payload: truncateForSmith(ev.Payload, 300),
		})
	}
	return out
}

func smithSkillViews(skills []config.SkillConfig) []smithSkillView {
	out := make([]smithSkillView, 0, len(skills))
	for _, sk := range skills {
		summary := ""
		if p := strings.TrimSpace(sk.Prompt); p != "" {
			summary = truncateForSmith(p, 160)
		}
		out = append(out, smithSkillView{
			ID:            sk.ID,
			Name:          sk.Name,
			Description:   sk.Description,
			Enabled:       sk.Enabled,
			PromptSummary: summary,
		})
	}
	return out
}

func smithMCPViews(servers []config.MCPServerConfig) []smithMCPView {
	out := make([]smithMCPView, 0, len(servers))
	for _, m := range servers {
		out = append(out, smithMCPView{
			ID:      m.ID,
			Name:    m.Name,
			Kind:    mcpTransportLabel(m),
			Enabled: m.Enabled,
		})
	}
	return out
}

func truncateForSmith(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// callSmithModel 用主模型（激活模型 + 备用链）非流式生成一次建议。
// 预算：120s 超时、60k tokens、0.4 元；usage 缺失时记录告警但不阻断。
func (s *Smith) callSmithModel(ctx context.Context, tr SmithTrigger, system, user string) (string, error) {
	if s.cfg == nil {
		return "", fmt.Errorf("smith 缺少配置管理器")
	}
	notify := func(from, to, reason string) {
		slog.Warn("smith 模型故障切换", "from", from, "to", to, "reason", reason)
		if s.bus != nil {
			s.bus.Emit(eventbus.Event{Type: eventbus.EvModelFallback, SessionID: tr.SessionID,
				Data: eventbus.ModelFallbackData{AgentID: s.id, From: from, To: to, Reason: reason}})
		}
	}
	cm, _, modelCfg, err := llm.BuildModelChain(ctx, s.cfg, s.id, tr.MainID, notify)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, smithTimeout)
	defer cancel()

	msg, err := cm.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return "", fmt.Errorf("smith 模型调用失败: %w", err)
	}
	if msg == nil {
		return "", fmt.Errorf("smith 模型返回空响应")
	}
	if msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		slog.Warn("smith 模型未返回 usage，预算未校验", "agentId", s.id)
		return msg.Content, nil
	}
	usage := msg.ResponseMeta.Usage
	inTok, outTok := int64(usage.PromptTokens), int64(usage.CompletionTokens)
	total := inTok + outTok
	cost := float64(inTok)/1e6*modelCfg.PriceIn + float64(outTok)/1e6*modelCfg.PriceOut
	if total > smithMaxTokens {
		return "", fmt.Errorf("%w: smith 使用 %d tokens > 上限 %d", ErrMaxTokens, total, smithMaxTokens)
	}
	if smithMaxCost > 0 && cost > smithMaxCost {
		return "", fmt.Errorf("%w: smith 成本 %.3f 元 > 上限 %.2f 元", ErrBudgetExceeded, cost, smithMaxCost)
	}
	return msg.Content, nil
}

// parseSmithProposal 从模型输出中提取 JSON（容忍 ```json 代码块与前后说明文字）。
func parseSmithProposal(raw string) (*SmithProposal, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, errors.New("smith 返回空内容")
	}
	if strings.HasPrefix(text, "```") {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, errors.New("smith 输出中未找到 JSON 对象")
	}
	var p SmithProposal
	if err := json.Unmarshal([]byte(text[start:end+1]), &p); err != nil {
		return nil, fmt.Errorf("解析 smith JSON 失败: %w", err)
	}
	return &p, nil
}

// parseAndValidate 解析并校验 smith 输出；action=skip 时不做 Spec 校验。
func (s *Smith) parseAndValidate(in *SmithInput, raw string) (*SmithProposal, error) {
	p, err := parseSmithProposal(raw)
	if err != nil {
		return nil, err
	}
	p.Action = strings.ToLower(strings.TrimSpace(p.Action))
	switch p.Action {
	case "create", "update", "skip":
	default:
		return nil, fmt.Errorf("非法 action: %q（可选 create/update/skip）", p.Action)
	}
	if p.Action == "skip" {
		return p, nil
	}
	if strings.TrimSpace(p.Spec.Name) == "" {
		p.Spec.Name = strings.TrimSpace(p.Name)
	}
	if strings.TrimSpace(p.Spec.Name) == "" {
		return nil, errors.New("smith 输出缺少 agent 名称")
	}
	if strings.TrimSpace(p.Spec.SystemPrompt) == "" {
		return nil, errors.New("smith 输出缺少 system_prompt")
	}
	if err := agentspec.Validate(p.Spec); err != nil {
		return nil, fmt.Errorf("smith Spec 校验失败: %w", err)
	}

	knownTools := make(map[string]bool)
	for _, n := range tools.KnownToolNames() {
		knownTools[n] = true
	}
	for _, t := range p.Spec.Tools {
		if !knownTools[t] && t != tools.ToolGroupDevTools {
			return nil, fmt.Errorf("smith 指定了未知工具: %s", t)
		}
	}
	if strings.TrimSpace(p.Spec.Model) != "" {
		if _, ok := llm.ResolveModel(s.cfg.Get(), p.Spec.Model); !ok {
			return nil, fmt.Errorf("smith 指定了不存在的模型: %s", p.Spec.Model)
		}
	}
	for _, ref := range p.Spec.Skills {
		if !containsSkillRef(in.Skills, ref) {
			return nil, fmt.Errorf("smith 引用了不存在的 Skill: %s", ref)
		}
	}
	for _, ref := range p.Spec.MCPServers {
		if !containsMCPRef(in.MCPServers, ref) {
			return nil, fmt.Errorf("smith 引用了不存在的 MCP Server: %s", ref)
		}
	}
	return p, nil
}

func containsSkillRef(skills []config.SkillConfig, ref string) bool {
	for _, sk := range skills {
		if sk.ID == ref || sk.Name == ref {
			return true
		}
	}
	return false
}

func containsMCPRef(servers []config.MCPServerConfig, ref string) bool {
	for _, m := range servers {
		if m.ID == ref || m.Name == ref {
			return true
		}
	}
	return false
}

// mcpTransportLabel 返回 MCP 连接类型（不暴露 URL/Command，避免密钥进入提示词）。
func mcpTransportLabel(m config.MCPServerConfig) string {
	if m.Transport != "" {
		return m.Transport
	}
	if strings.TrimSpace(m.Command) != "" {
		return "stdio"
	}
	return "http"
}

// ErrSmithRejected 用户拒绝了 smith 的配置修改。
var ErrSmithRejected = errors.New("用户拒绝了 agent-smith 的配置修改")

// ErrSmithBusy 同一项目已有 smith 在运行。
var ErrSmithBusy = errors.New("agent-smith 正在运行，请稍后再试")

// Run 执行一次优化：按项目加锁，并推送 started/applied/rejected/skipped/failed 事件。
func (s *Smith) Run(ctx context.Context, tr SmithTrigger) (*SmithProposal, error) {
	if s == nil || s.ws == nil || !s.ws.IsSet() {
		return nil, workspace.ErrNoWorkspace
	}
	root := s.ws.Root()
	st, err := s.cfg.ProjectStatus(root)
	if err != nil {
		return nil, err
	}
	if st.Status != config.ProjectTrusted {
		return nil, fmt.Errorf("项目配置未信任或已拉黑，无法运行 agent-smith")
	}
	if !s.acquire(root) {
		return nil, ErrSmithBusy
	}
	defer s.release(root)

	s.emit(tr.SessionID, eventbus.AgentSmithData{TaskID: tr.TaskID, Phase: "started"})
	p, err := s.run(ctx, tr)
	if err != nil {
		phase := "failed"
		if errors.Is(err, ErrSmithRejected) {
			phase = "rejected"
		}
		data := eventbus.AgentSmithData{TaskID: tr.TaskID, Phase: phase, Error: err.Error()}
		if p != nil {
			data.Name = p.Spec.Name
			data.Reason = p.Reason
		}
		s.emit(tr.SessionID, data)
		return p, err
	}
	if p.Action == "skip" {
		s.emit(tr.SessionID, eventbus.AgentSmithData{
			TaskID: tr.TaskID, Phase: "skipped", Reason: p.Reason,
		})
		return p, nil
	}
	s.emit(tr.SessionID, eventbus.AgentSmithData{
		TaskID: tr.TaskID, Phase: "applied", Name: p.Spec.Name, Reason: p.Reason,
	})
	return p, nil
}

// run 执行实际优化流程（调用方已持有项目锁）。
func (s *Smith) run(ctx context.Context, tr SmithTrigger) (*SmithProposal, error) {
	in, err := s.collectInput(tr)
	if err != nil {
		return nil, err
	}
	system, user, err := buildSmithPrompt(in)
	if err != nil {
		return nil, err
	}
	raw, err := s.callSmithModel(ctx, tr, system, user)
	if err != nil {
		return nil, err
	}
	p, err := s.parseAndValidate(in, raw)
	if err != nil {
		return nil, err
	}
	if p.Action == "skip" {
		return p, nil
	}
	if err := s.applyProposal(ctx, tr, in, p); err != nil {
		return p, err
	}
	return p, nil
}

// Busy 报告项目根是否已有 smith 在运行。
func (s *Smith) Busy(root string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[root]
}

// acquire 标记项目根为运行中；已有运行返回 false。
func (s *Smith) acquire(root string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[root] {
		return false
	}
	s.running[root] = true
	return true
}

// release 清除项目根的运行标记。
func (s *Smith) release(root string) {
	s.mu.Lock()
	delete(s.running, root)
	s.mu.Unlock()
}

// emit 推送 agent_smith 事件（bus 为 nil 时静默）。
func (s *Smith) emit(sessionID string, data eventbus.AgentSmithData) {
	if s.bus == nil {
		return
	}
	s.bus.Emit(eventbus.Event{Type: eventbus.EvAgentSmith, SessionID: sessionID, Data: data})
}

// applyProposal 经用户审批后先备份旧配置再保存新 Spec。
func (s *Smith) applyProposal(ctx context.Context, tr SmithTrigger, in *SmithInput, p *SmithProposal) error {
	if s.appr == nil {
		return errors.New("无审批通道，拒绝写入 Agent 配置")
	}
	if strings.TrimSpace(tr.SessionID) == "" {
		return errors.New("缺少审批归属会话")
	}
	var old *agentspec.Spec
	for i := range in.Agents {
		if in.Agents[i].Name == p.Spec.Name {
			old = &in.Agents[i]
			break
		}
	}
	rel := filepath.Join(".aidog", "agents", p.Spec.Name+".yml")
	ok, err := s.appr.RequestContext(ctx, tr.SessionID, "agent_smith", rel, smithPreview(old, p))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSmithRejected, err)
	}
	if !ok {
		return ErrSmithRejected
	}
	if _, err := agentspec.Backup(in.ProjectRoot, p.Spec.Name); err != nil {
		return fmt.Errorf("备份旧配置失败: %w", err)
	}
	if err := agentspec.Save(in.ProjectRoot, p.Spec); err != nil {
		return fmt.Errorf("保存 Agent 配置失败: %w", err)
	}
	return nil
}

// smithPreview 审批卡片展示的 before/after 摘要。
func smithPreview(old *agentspec.Spec, p *SmithProposal) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Agent: %s\n动作: %s\n原因: %s\n", p.Spec.Name, p.Action, p.Reason)
	if old != nil {
		sb.WriteString("\n修改前:\n")
		writeSpecPreview(&sb, *old)
	}
	sb.WriteString("\n修改后:\n")
	writeSpecPreview(&sb, p.Spec)
	return sb.String()
}

func writeSpecPreview(sb *strings.Builder, spec agentspec.Spec) {
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		sb.WriteString("（无法序列化）\n")
		return
	}
	sb.WriteString(truncateForSmith(string(data), 1200))
	sb.WriteString("\n")
}
