# internal/tools — 工具接口 + 注册表 + Invoker

## Tool 接口（tool.go）

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Invoke(ctx, params) (Result, error)
}

type Result struct {
    Content   string
    IsError   bool
    ErrorType string  // "runtime_error" | "rate_limited" | "invalid_request"
}
```

## Registry

`Register / Get / Filtered(allowed) / ToolDefinitions()`。

- `Filtered` 空集合 = 全量拷贝（工具实例无状态可共享）
- 白名单非空时只保留集合内工具（skill 场景限制可用工具）

## Invoker（invoker.go）

执行管线：

1. 发布 `ToolCallStarted` → 查工具（未知工具直接失败）
2. `perm.CheckAndWait`：需审批时发布 `PermissionRequested`（预览用 `permissions.ParamPreview`），等待用户 `permission.respond`（超时从 config 读取）→ 结果发布 `PermissionGranted` / `PermissionDenied`
3. `executeWithRetry`：带超时执行（`timeout<=0` 不设超时），成功发布 `ToolCallFinished`
4. 失败时 `runtime_error`/`rate_limited` 按指数退避重试（`retryBase` 可配置，默认 2s），最多重试 `maxRetries`=2 次
5. 超时/其它错误直接失败，不重试

## 内置工具（builtin/）

| 工具 | 文件 | 说明 | 默认权限 |
|------|------|------|---------|
| read_file | read_file.go | 拒绝 `..` 路径穿越，>512KB 截断 | Allow |
| list_dir | list_dir.go | 目录名加 `/` 后缀 | Allow |
| write_file | write_file.go | 自动 MkdirAll 父目录，拒绝 `..` | Ask |
| bash | bash.go | Windows 用 `cmd /c`，否则 `/bin/sh -c` | Ask |
| note_save | note_save.go | 追加到会话 notes.md（仅会话场景） | Allow |
| task_create/update/list/get | task.go | 委托 `task.Manager`，per-run 隔离 | — |
