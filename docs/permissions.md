# internal/permissions — 策略评估 + 审批

## 策略匹配（policy.go）

- `MatchesOutsideCWD`：判断 bash 命令是否命中 outside-cwd 启发式规则（绝对路径、`~`、`..`、`$HOME`、`cd` 等 6 条正则）
- **正则预编译**：6 条 outside-cwd 正则在包 `init` 时编译一次，运行时直接匹配，避免每次重新编译
- `ParamPreview`：生成参数摘要用于权限弹窗预览

## Manager（manager.go）

`CheckAndWait` 六阶段决策流程：

1. **Phase 1**：deny_patterns 命中 → `auto_deny`
2. **Phase 2**：outside-cwd 命中 → 标记 `forceAsk`（不可被缓存绕过）
3. **Phase 3**：缓存查找（仅当非 forceAsk）→ 先查 `sessionAlways`（sessionKey = `sessionID::toolName`），再查 `persistentAlways`（按工具名）
4. **Phase 4**：allow_patterns 命中 → `auto_allow`（仅当非 forceAsk）
5. **Phase 5**：tool default（Allow → `auto_allow`；Deny → `auto_deny`，仅当非 forceAsk）
6. **Phase 6**：ASK 路径（forceAsk 或 default=Ask）→ 注册 `pending[toolUseID]` channel → 调 `onAsk` 发布 `PermissionRequested` → 等待审批或超时自动拒绝

`Respond` 通过 channel 回传决策；`always_allow/deny` 同时写 `sessionAlways` 与 `persistentAlways`（当前 wiring 中 policy 文件路径为空，不落盘）。
