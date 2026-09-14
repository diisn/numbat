package config

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// McpServer 描述一个 MCP server 的启动配置。
type McpServer struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// ChannelConfig 描述一个外部 IM 通道（Channel 适配器）的接入配置。
type ChannelConfig struct {
	Name      string `toml:"name"`       // 通道名，全局唯一
	Type      string `toml:"type"`       // 平台类型：feishu | telegram
	Enabled   bool   `toml:"enabled"`    // 是否启用；缺省不启动
	AppID     string `toml:"app_id"`     // 飞书：应用 app_id
	AppSecret string `toml:"app_secret"` // 飞书：应用 app_secret
	Token     string `toml:"token"`      // Telegram：bot token
	ChatID    string `toml:"chat_id"`    // 可选：Telegram 仅处理该 chat 的消息 / 默认接收者
}

// RoutingConfig 描述入站消息到 Agent 的路由规则（由 channel.Router 消费）。
type RoutingConfig struct {
	DefaultAgent string            `toml:"default_agent"` // 默认 Agent 名称（优先级最低）
	BySender     map[string]string `toml:"by_sender"`     // 发送者 ID -> Agent 名称（优先级最高）
	ByChannel    map[string]string `toml:"by_channel"`    // 通道名 -> Agent 名称
}

// Config 保存 numbat-core 的运行时配置。
// 加载顺序：默认值 -> ~/.numbat/config.toml -> ./.numbat/config.toml -> numbat_* 环境变量 -> .env 文件。
type Config struct {
	Host                 string          `toml:"host"`
	GatewayPort          int             `toml:"gateway_port"`
	RateLimit            float64         `toml:"rate_limit"` // WS 单连接限流：每秒消息数；0 禁用
	RateBurst            float64         `toml:"rate_burst"` // 限流突发上限
	LogLevel             string          `toml:"log_level"`
	AnthropicAPIKey      string          `toml:"anthropic_api_key"`
	DefaultModel         string          `toml:"default_model"`
	MaxSteps             int             `toml:"max_steps"`
	BaseURL              string          `toml:"base_url"`
	McpServers           []McpServer     `toml:"mcp_servers"`
	MaxTokens            int             `toml:"max_tokens"`
	ContextWindow        int             `toml:"context_window"`
	PermissionTimeoutSec int             `toml:"permission_timeout_sec"`
	ToolTimeoutSec       int             `toml:"tool_timeout_sec"`
	AutoCompactThreshold float64         `toml:"auto_compact_threshold"`
	ToolResultLimit      int             `toml:"tool_result_limit"`
	ToolResultKeep       int             `toml:"tool_result_keep"`
	MaxSubagentDepth     int             `toml:"max_subagent_depth"`
	Channels             []ChannelConfig `toml:"channels"` // 外部 IM 通道接入；缺省为空（不启动）
	Routing              RoutingConfig   `toml:"routing"`  // 通道消息路由规则
}

// Load 加载配置：默认值 → ~/.numbat/config.toml → ./.numbat/config.toml → 环境变量。
// AutoCompactThreshold 缺省为 0（禁用自动压缩）。
func Load() (*Config, error) {
	cfg := &Config{
		Host:                 "127.0.0.1",
		GatewayPort:          7438,
		LogLevel:             "INFO",
		DefaultModel:         "claude-sonnet-4-6",
		MaxSteps:             20,
		MaxTokens:            8192,
		ContextWindow:        200000,
		PermissionTimeoutSec: 60,
		ToolTimeoutSec:       120,
		ToolResultLimit:      8000,
		ToolResultKeep:       4000,
		MaxSubagentDepth:     2,
	}

	home, err := os.UserHomeDir()
	if err == nil {
		if err := loadFile(filepath.Join(home, ".numbat", "config.toml"), cfg); err != nil && !os.IsNotExist(err) {
			slog.Warn("config: failed to load user config", "error", err)
		}
	}

	if err := loadFile(filepath.Join(".numbat", "config.toml"), cfg); err != nil && !os.IsNotExist(err) {
		slog.Warn("config: failed to load project config", "error", err)
	}

	dotEnv, err := readDotEnv(filepath.Join(".numbat", ".env"))
	if err != nil && !os.IsNotExist(err) {
		slog.Warn("config: failed to load .env", "error", err)
	}
	applyEnv(cfg, dotEnv)

	return cfg, nil
}

func loadFile(path string, cfg *Config) error {
	_, err := toml.DecodeFile(path, cfg)
	return err
}

// applyEnv 应用环境配置，优先级：系统环境变量 > .env 文件 > config.toml > 默认值。
// 同一用途存在多个历史变量名时，按 names 顺序取第一个有值的。
func applyEnv(cfg *Config, dotEnv map[string]string) {
	if v := pickEnv(dotEnv, "NUMBAT_HOST"); v != "" {
		cfg.Host = v
	}
	if v := pickEnv(dotEnv, "NUMBAT_RATE_LIMIT"); v != "" {
		cfg.RateLimit = atofOr("NUMBAT_RATE_LIMIT", v, cfg.RateLimit)
	}
	if v := pickEnv(dotEnv, "NUMBAT_RATE_BURST"); v != "" {
		cfg.RateBurst = atofOr("NUMBAT_RATE_BURST", v, cfg.RateBurst)
	}
	if v := pickEnv(dotEnv, "NUMBAT_GATEWAY_PORT"); v != "" {
		cfg.GatewayPort = atoiOr("NUMBAT_GATEWAY_PORT", v, cfg.GatewayPort)
	}
	if v := pickEnv(dotEnv, "NUMBAT_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := pickEnv(dotEnv, "ANTHROPIC_API_KEY", "NUMBAT_ANTHROPIC_API_KEY"); v != "" {
		cfg.AnthropicAPIKey = v
	}
	if v := pickEnv(dotEnv, "NUMBAT_LLM_DEFAULT_MODEL", "NUMBAT_DEFAULT_MODEL"); v != "" {
		cfg.DefaultModel = v
	}
	if v := pickEnv(dotEnv, "NUMBAT_MAX_STEPS"); v != "" {
		cfg.MaxSteps = atoiOr("NUMBAT_MAX_STEPS", v, cfg.MaxSteps)
	}
	if v := pickEnv(dotEnv, "NUMBAT_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := pickEnv(dotEnv, "NUMBAT_COMPACT_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.AutoCompactThreshold = f
		} else {
			slog.Warn("config: invalid NUMBAT_COMPACT_THRESHOLD, must be within [0,1]", "value", v)
		}
	}
}

// pickEnv 取第一个有值的变量：先查系统环境变量，再查 .env 文件。
func pickEnv(dotEnv map[string]string, names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	for _, name := range names {
		if v := dotEnv[name]; v != "" {
			return v
		}
	}
	return ""
}

// atoiOr 解析整数，失败时保留原值并告警（否则配置写错会毫无痕迹）。
func atoiOr(name, value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		slog.Warn("config: invalid integer, using fallback", "env", name, "value", value, "fallback", fallback)
		return fallback
	}
	return n
}

// atofOr 解析浮点数，失败时保留原值并告警。
func atofOr(name, value string, fallback float64) float64 {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		slog.Warn("config: invalid float, using fallback", "env", name, "value", value, "fallback", fallback)
		return fallback
	}
	return f
}

// readDotEnv 把 .env 读成键值表，供 applyEnv 按统一优先级取值。
// 文件不存在时返回 os.ErrNotExist，由调用方决定是否忽略。
func readDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		// 去除值两侧可能的引号
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values, scanner.Err()
}
