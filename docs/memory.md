# internal/memory — 记忆层

## LoadAll（loader.go）

读取两个 context.md 文件：

| 文件 | 路径 | 作用域 |
|------|------|--------|
| 全局 | `~/.numbat/context.md` | 所有项目 |
| 项目 | `.numbat/context.md` | 当前项目 |

文件不存在返回空串。两文件内容拼接到 system prompt 的 `## Global Context` / `## Project Context` 段。

详见 [context.md](context.md) 的 `SystemPrompt`。
