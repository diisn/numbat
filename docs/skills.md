# internal/skills — Skill 加载器

## 加载（loader.go）

**查找优先级**：项目本地 `.numbat/skills` > 用户全局 `~/.numbat/skills` > 内建（`//go:embed builtin/*.md`）

**两种形态**：扁平文件 `name.md` 与目录式 `name/SKILL.md`

## 解析

- frontmatter 正则提取 `name` / `description`（支持 `>`/`|` 折叠）/ `allowed_tools`（`- ` 列表）
- 正文为 `SystemPromptTemplate`

## 渲染

`RenderPrompt` 把 `$ARGUMENTS` 替换为用户参数。

## 内建 Skill

init / orchestrate / review / summarize

## 触发

TUI 输入 `/name args` → core 端 `session.send_message` 检测 `/` 前缀 → `Resolve` + `RenderPrompt`，渲染结果作为 **system prompt 覆盖**（记忆层仍追加在末尾），`allowed_tools` 作为本次 run 工具白名单。未命中 skill 时按普通消息处理。发布 `SkillInvoked` 事件。
