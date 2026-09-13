package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/youngyangyang04/numbat/internal/agents"
	"github.com/youngyangyang04/numbat/internal/channel"
	"github.com/youngyangyang04/numbat/internal/compact"
	"github.com/youngyangyang04/numbat/internal/config"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
	"github.com/youngyangyang04/numbat/internal/mcp"
	"github.com/youngyangyang04/numbat/internal/permissions"
	"github.com/youngyangyang04/numbat/internal/session"
	"github.com/youngyangyang04/numbat/internal/subagent"
	"github.com/youngyangyang04/numbat/internal/tools"
	"github.com/youngyangyang04/numbat/internal/tools/builtin"
	"github.com/youngyangyang04/numbat/internal/trace"
	"github.com/youngyangyang04/numbat/internal/transport"
	"github.com/youngyangyang04/numbat/internal/util"
	"github.com/youngyangyang04/numbat/internal/webui"
)

// App 组装 numbat-core 的所有组件。
type App struct {
	config *config.Config
	server *transport.Server
	bus    *events.Bus
}

// New 创建 App。
func New(cfg *config.Config) *App {
	return &App{config: cfg}
}

// Run 启动 daemon。
func (a *App) Run(ctx context.Context) error {
	bus := events.New()
	a.bus = bus

	var provider llm.Provider = llm.NewAnthropicProvider(a.config.AnthropicAPIKey, a.config.DefaultModel, a.config.BaseURL, a.config.MaxTokens, a.config.ContextWindow)

	// 将可配置参数传递给各包
	compact.SetBudget(a.config.ToolResultLimit, a.config.ToolResultKeep)
	subagent.SetMaxDepth(a.config.MaxSubagentDepth)

	registry := tools.NewRegistry()
	registry.Register(builtin.ReadFileTool{})
	registry.Register(builtin.ListDirTool{})
	registry.Register(builtin.WriteFileTool{})
	registry.Register(builtin.BashTool{})

	policyPath := util.ResolveNumbatPath("policy.toml")
	perm := permissions.NewManager(policyPath, time.Duration(a.config.PermissionTimeoutSec)*time.Second)
	toolTimeout := time.Duration(a.config.ToolTimeoutSec) * time.Second
	invoker := tools.NewInvoker(registry, perm, bus, toolTimeout)

	// Trace：将事件流写入 ~/.numbat/runs/<runID>/events.jsonl
	traceDir := util.ResolveNumbatPath("runs")

	// run 级工具（任务/笔记）不进基础注册表，避免跨 run 共享：
	// - 任务工具：每次 run 由 handler 的 runToolInvoker 绑定 <runsDir>/<runID>/.tasks（per-run 隔离）；
	// - note_save：仅会话场景由 handler 绑定当前 session 的 Store（写入 notes.md，后续轮次注入）。
	// 无会话的 agent.run 因此不提供 note_save（与 Python 版一致）。

	// 子 Agent 任务注册表（后台并行任务 + agent_result 查询），跨 run 共享。
	// spawn_agent 工具不放进基础注册表：由 handler 的 runToolInvoker 按当前 runID 构造，
	// 使 subagent.started/finished 携带正确的 parent_run_id（与 Python 版 build_registry 一致）。
	subagentTasks := subagent.NewTaskRegistry()
	subagentLoader := agents.NewLoader()

	// MCP 外部工具接入：连接配置中的 MCP servers 并注册其工具
	var mcpClients []*mcp.Client
	for _, s := range a.config.McpServers {
		mc := mcp.NewClient(s.Command, s.Args, nil)
		if err := mc.Connect(ctx); err != nil {
			slog.Warn("mcp connect failed", "server", s.Name, "error", err)
			continue
		}
		mcpClients = append(mcpClients, mc)
		defs, err := mc.ListTools(ctx)
		if err != nil {
			slog.Warn("mcp list_tools failed", "server", s.Name, "error", err)
			_ = mc.Close()
			mcpClients = mcpClients[:len(mcpClients)-1] // 移除已关闭的
			continue
		}
		for _, d := range defs {
			if registry.Get(d.Name) != nil {
				slog.Warn("mcp tool name conflict, skipped", "server", s.Name, "tool", d.Name)
				continue
			}
			registry.Register(mcp.NewTool(mc, d))
		}
		slog.Info("mcp server connected", "server", s.Name, "tools", len(defs))
	}
	// 优雅关闭：在 server.Run 返回后关闭所有 MCP 子进程
	defer func() {
		for _, mc := range mcpClients {
			_ = mc.Close()
		}
	}()

	sessionsRoot := util.ResolveNumbatPath("sessions")
	sessionStore := session.NewStore(sessionsRoot)
	sessionManager := session.NewManager(sessionStore, bus)

	traceWriter := trace.NewWriter(traceDir)
	traceWriter.Subscribe(bus)
	defer traceWriter.Close()

	// 全局 trace：三层（IPC/Event/LLM）写入 ~/.numbat/traces/daemon.jsonl
	globalTracePath := util.ResolveNumbatPath("traces", "daemon.jsonl")
	globalTrace, err := trace.NewGlobalWriter(globalTracePath)
	if err != nil {
		slog.Warn("global trace writer init failed", "error", err)
	} else {
		globalTrace.Subscribe(bus)
		defer globalTrace.Close()
	}

	// 用 TracingProvider 包装，记录 LLM 层 trace
	if globalTrace != nil {
		provider = &llm.TracingProvider{
			Inner: provider,
			Trace: func(direction, layer, kind string, data any) {
				globalTrace.Write(trace.TraceRecord{
					Ts:        time.Now().UTC().Format(time.RFC3339),
					Direction: direction,
					Layer:     layer,
					Kind:      kind,
					Data:      data,
				})
			},
			IncludePayload: true,
		}
	}

	server := transport.NewServer(fmt.Sprintf("%s:%d", a.config.Host, a.config.Port))
	server.SetBus(bus)
	server.SetRunsDir(traceDir)
	if globalTrace != nil {
		server.SetGlobalTrace(globalTrace)
	}

	handlers := transport.NewHandlers(transport.HandlerDeps{
		Provider:         provider,
		MaxSteps:         a.config.MaxSteps,
		Invoker:          invoker,
		Bus:              bus,
		Perm:             perm,
		Session:          sessionManager,
		ToolTimeout:      toolTimeout,
		RunsDir:          traceDir,
		RunCanceler:      transport.NewRunCanceler(),
		CompactThreshold: a.config.AutoCompactThreshold,
		SubagentTasks:    subagentTasks,
		SubagentLoader:   subagentLoader,
	})
	for method, h := range handlers {
		server.Register(method, h)
	}

	a.server = server

	gateway := transport.NewGateway(fmt.Sprintf("%s:%d", a.config.Host, a.config.GatewayPort))
	gateway.SetRPCServer(server)

	// 内嵌前端产物挂在 /app/*（见 internal/webui）。
	// 未执行 npm run build 时产物为空，仅告警不影响其余接口。
	if dist, err := webui.Dist(); err != nil {
		slog.Warn("webui assets unavailable", "error", err)
	} else if webui.Built() {
		gateway.SetWebUI(dist)
		slog.Info("webui available", "path", fmt.Sprintf("/app/ (http://%s:%d/app/)", a.config.Host, a.config.GatewayPort))
	} else {
		slog.Warn("webui not built; run `npm run build` in webui/ to enable /app/", "hint", "cd webui && npm run build")
	}

	// 外部 IM 通道接入（可选）：配置了启用的通道时才启动 channel manager 与 router；
	// 缺省配置为空，不启动（不影响既有 TCP/WS 行为）。
	if cm := newChannelManager(a.config, subagentLoader); cm != nil {
		go func() { _ = cm.Start(ctx) }()
	}

	slog.Info("starting numbat-core", "host", a.config.Host, "port", a.config.Port, "gateway_port", a.config.GatewayPort)

	errCh := make(chan error, 2)
	go func() { errCh <- server.Run(ctx) }()
	go func() { errCh <- gateway.Run(ctx) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

// newChannelManager 按配置组装通道管理器：无启用通道时返回 nil。
// 路由规则经 channel.Router 消费；Agent 名称经 agents.Loader 解析为完整配置。
func newChannelManager(cfg *config.Config, loader *agents.Loader) *channel.Manager {
	var enabled []config.ChannelConfig
	for _, c := range cfg.Channels {
		if c.Enabled && c.Name != "" {
			enabled = append(enabled, c)
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	resolve := func(name string) (*channel.AgentConfig, bool) {
		p := loader.Load(name)
		if p == nil {
			return nil, false
		}
		return &channel.AgentConfig{
			Name:         p.Name,
			Model:        p.Model,
			SystemPrompt: p.SystemPrompt,
			AllowedTools: p.AllowedTools,
		}, true
	}
	router := channel.NewRouter(cfg.Routing.DefaultAgent, cfg.Routing.BySender, cfg.Routing.ByChannel, resolve)

	// 本阶段通过注入的 handler 回调承接 Agent 执行（模块间回调解耦，channel 包不直接
	// 依赖 loop/llm/transport）。TODO: 接入 transport 的 agent.run / session.send_message
	// 流程——按 AgentConfig 覆盖 model/system_prompt/工具白名单，并把结果文本回发。
	handler := func(ctx context.Context, msg channel.InboundMessage, agent *channel.AgentConfig) (string, error) {
		return fmt.Sprintf("(占位回复，Agent 执行尚未接入) channel=%s agent=%s: %s",
			msg.ChannelName, agent.Name, msg.Content), nil
	}

	mgr := channel.NewManager(router, handler)
	for _, c := range enabled {
		var ch channel.Channel
		switch c.Type {
		case "telegram":
			ch = channel.NewTelegramChannel(c.Name, c.Token)
		case "feishu":
			ch = channel.NewFeishuChannel(c.Name, c.AppID, c.AppSecret)
		default:
			slog.Warn("channel: unknown type, skipped", "channel", c.Name, "type", c.Type)
			continue
		}
		if err := mgr.Register(ch); err != nil {
			slog.Warn("channel: register failed", "channel", c.Name, "error", err)
		}
	}
	return mgr
}
