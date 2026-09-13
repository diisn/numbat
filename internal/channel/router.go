package channel

// AgentConfig 是路由返回的目标 Agent 配置（名称/model/system_prompt/allowed_tools）。
type AgentConfig struct {
	Name         string
	Model        string
	SystemPrompt string
	AllowedTools []string
}

// Resolver 把 Agent 名称解析为完整配置；名称不存在时返回 false。
// 由组装层注入（如基于 internal/agents.Loader 的实现），避免 channel 包反向依赖。
type Resolver func(name string) (*AgentConfig, bool)

// Router 决定入站消息由哪个 Agent 处理。
// 路由规则按优先级：发送者 ID 精确匹配 > 通道名匹配 > 默认 Agent；
// 任一规则指定了 Agent 但解析失败时逐级回退，默认 Agent 也解析失败则返回 nil（由调用方拒绝）。
type Router struct {
	defaultAgent string
	bySender     map[string]string // senderID -> agent 名称
	byChannel    map[string]string // channelName -> agent 名称
	resolve      Resolver
}

// NewRouter 创建 Router。规则数据（defaultAgent/bySender/byChannel）由 config 的
// TOML 解析结果传入（见 internal/config.Config.Routing）。
func NewRouter(defaultAgent string, bySender, byChannel map[string]string, resolve Resolver) *Router {
	return &Router{
		defaultAgent: defaultAgent,
		bySender:     bySender,
		byChannel:    byChannel,
		resolve:      resolve,
	}
}

// Route 按优先级为 (channelName, senderID) 选择目标 Agent；无法解析时返回 nil。
func (r *Router) Route(channelName, senderID string) *AgentConfig {
	for _, name := range []string{r.bySender[senderID], r.byChannel[channelName], r.defaultAgent} {
		if name == "" || r.resolve == nil {
			continue
		}
		if cfg, ok := r.resolve(name); ok {
			return cfg
		}
	}
	return nil
}
