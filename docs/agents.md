# internal/agents — Agent Profile

## 加载（loader.go）

与 Skill 相同三级优先级：项目本地 `.numbat/agents` > 用户全局 `~/.numbat/agents` > 内建 embed。

TOML 格式 `[agent]`：`description / system_prompt / allowed_tools / model`

## 内建角色

[planner.toml](../internal/agents/builtin/planner.toml)、executor、reviewer

## 使用

`subagent_type` 指定角色时应用其 system prompt 与工具白名单，未找到则静默降级为默认子 Agent。
