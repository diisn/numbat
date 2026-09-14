# internal/session — 会话模型 + 存储

## 模型（model.go）

`Session{ID, Mode(one_shot/chat), Status(active/waiting_for_input/closed), Title, CreatedAt, UpdatedAt, RunIDs}`

`Clone()` 返回副本并将 `RunIDs` 深拷贝到新底层数组（`append([]string{}, ...)`）——既防副本 append 写穿原对象，也保证零值会话序列化出 `run_ids: []` 而非 `null`（契约要求，回归测试 `TestCloneKeepsEmptyRunIDs`）。Manager 的 `Get/List` 对外一律返回克隆。

## Manager（manager.go）

`Create/Get/List/Clear/Close/Update`，内存 map + `store` 兜底，发布 `SessionCreated/Closed` 事件。

- `List`：内存 + 磁盘会话并集，按更新时间倒序（对应 `session.list` RPC）。
- `Clear`：清空指定会话消息历史（对应 `session.clear` RPC）。

## Store（store.go）

文件布局（`~/.numbat/sessions/<sid>/`）：

| 文件 | 说明 |
|------|------|
| `meta.json` | 会话元数据 |
| `thread.jsonl` | 消息历史（`{ts,role,content[]}`，每行一条） |
| `notes.md` | note_save 追加的会话笔记 |

- `AppendMessages` 按顺序批量追加（`AppendMessage` 是它的单条特例）
- `ReadMessages` 读取时先 `trimOrphanToolUse` 裁掉尾部未配对的 `tool_use` 及其后消息 ——
  被中止的 run 会留下孤立 `tool_use`，不裁掉会让下一次请求被上游以 `messages.invalid` 拒绝
- `WriteCompacted` 先备份为 `thread_<ts>.jsonl.bak` 再覆盖
- **per-session 文件锁**：`sync.Map` 存 per-session `*sync.Mutex`，所有文件操作加锁，防止并发追加交错或 WriteCompacted 竞争

## 落库时机（transport/handler.go）

`session.send_message` 无论 run 成功、失败还是被中止，都会落整段新增消息
（`execCtx.Messages[prefillLen:]`，含 assistant 的 `tool_use` 与 user 的 `tool_result`）；
中途压缩时退回只落最终答复。
