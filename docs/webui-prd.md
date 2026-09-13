# WebUI 产品需求文档（PRD）

> 供本人 + AI 协作执行的完整 PRD，覆盖全部规划阶段（M1/M2/M3）。代码实现按阶段分批推进，但本文档一次性成型。
> 接口事实以 [webui-api-contract.md](webui-api-contract.md) 为准（方法/事件/字段/连接语义）。
> 产品参照：Claudia / opcode（本地 agent 守护进程的 GUI 壳），对标 [tui/client.go](../internal/tui/client.go) 已有能力作为"第二客户端"。

## 1. 产品概述

**定位**：numbat-core 的浏览器客户端。与 TUI 共享同一套 JSON-RPC + 事件流协议，是同一后端的第二个客户端实现。

**目标用户**：本人（开发者）日常使用；远期可开放给团队。

**核心价值**：
1. **过程可见**——UI 是事件流的投影，工具调用/权限/思考全程透明（调研：无中途可见性的 agent 会话放弃率高 3×）
2. **人在环**——权限审批内嵌对话流，非阻塞打断
3. **可控**——run 随时可中止
4. **可回溯**——历史 run 可回放复盘

**整体范围**：从对标 TUI 的核心闭环（M1），到可观测性面板（M2），再到进阶控制台（M3）。

## 2. 设计原则（源自成熟 agent 产品调研）

| 原则 | 在本项目的落地 |
|---|---|
| UI 是事件流投影，非请求/应答 | 事件流驱动渲染，RPC 响应仅作终态确认；`llm.token`→追加文本，`tool.*`→卡片，`run.finished`→收口 |
| 过程可见性 > 结果 | 工具卡片展示 名字+参数+输出+耗时；失败显式红色态 |
| 审批是内嵌打断，非弹窗 | `permission.requested` 渲染为对话流内卡片；超时/断线默认最安全动作 |
| 流式渲染纪律 | token 即时渲染（光标动画，不用 spinner）、自动跟随滚动、Markdown 边流边渲 |
| 消息模型用 `parts[]` | 一条 assistant 消息 = `[text, tool_use, text...]`，历史与实时归并同一形状（contract §4） |
| 控制权常驻 | abort 按钮在 run 中始终可点；输入框不锁死（可排队） |

## 3. 信息架构

```
┌──────────────┬─────────────────────────────────────┐
│ 会话侧栏      │  聊天区（当前会话）                    │
│              │ ┌─────────────────────────────────┐   │
│ [+ 新会话]    │ │ user: 帮我读一下 main.go          │   │
│ 🔍 搜索       │ │ ── run r-xxx ──────────── ⏹ ── │   │
│              │ │ assistant: 我来读取文件…            │   │
│ ● 会话A(运行中)│ │ ┌ 🔧 read_file ✓ 120ms ─────────┐ │   │
│ ○ 会话B      │ │ └ (点击展开 params/output) ─────┘ │   │
│ ○ 会话C(关闭) │ │ assistant: 这个文件包含…(流式▋)    │   │
│              │ │ ┌ ⚠ 权限审批: bash ──────────────┐ │   │
│ ── M2/M3 ──  │ │ │ rm -rf tmp/                    │ │   │
│ 📊 用量(M2)   │ │ │ [允许一次][总是允许][拒绝]       │ │   │
│ 🌳 子Agent(M3)│ │ └────────────────────────────────┘ │   │
│              │ └─────────────────────────────────┘   │
│              │ [输入框…              ] [发送/排队]      │
└──────────────┴─────────────────────────────────────┘
```

## 4. 功能需求

### 里程碑 M1：核心闭环（对标 TUI）

#### C1 连接与骨架（Walking Skeleton）
- **故事**：打开页面即连上 numbat-core，断线可见、自动重连。
- **交互**：连接状态徽标（连接中/已连接/断开重连中 N/10s）；指数退避 1s→2s→5s→10s 上限；重连后重新 `event.subscribe` + 刷新会话列表。
- **契约映射**：`GET /health` 探活；WS `/ws`；`event.subscribe`（一次）；`core.ping`。
- **验收**：杀掉 numbat-core 页面显示"已断开"，重启后 10s 内自动恢复；ping 得 pong。

#### C2 会话侧栏
- **故事**：查看/新建/关闭/清空会话，切换加载历史，状态实时更新。
- **交互**：新建 `session.create{mode:"chat", title: 首条消息摘要}`；`session.created/closed` 事件驱动增删；状态徽标（active/waiting_for_input/closed，运行中由本地 run 状态推导）；搜索框本地过滤；关闭的会话发送禁用 + 提示。
- **契约映射**：`session.list`；`session.create`；`session.close`；`session.clear`（二次确认）；`session.created/closed` 事件。
- **验收**：新建会话→发消息→侧栏出现且状态随 run 变化；重连后列表不丢；关闭的会话不可发消息。

#### C3 聊天视图（历史 + 流式）
- **故事**：进入会话看完整历史；发消息看 token 流式回复；Markdown 渲染。
- **交互**：`get_history`→parts 渲染；发送 `send_message`（串行，run 中输入进本地队列，run 结束自动发下一条）；`llm.token` 逐个追加；`llm.response` 收口；自动跟随滚动（向上滚暂停 + "回到底部"按钮）；`skill.invoked` 在用户消息打 skill 徽标。
- **契约映射**：`session.get_history`→parts（contract §4.1）；`session.send_message`；`llm.token`/`llm.response`/`run.started`/`run.finished`（contract §4.2）。
- **验收**：切换会话历史秒开（tool_use_id 正确并回 tool_result）；token 逐个渲染；run 结束后队列下条自动发出；同一会话绝无并发 send。

#### C4 工具调用卡片
- **故事**：每个工具调用显示为折叠卡片（工具名+状态+耗时），点击展开 params/output。
- **契约映射**：`tool.call_started/finished/failed`→tool_use part 状态机。
- **验收**：多工具并行各自独立卡片；失败卡片红色态；历史加载的旧卡片同样可展开。

#### C5 权限审批卡片
- **故事**：`permission.requested` 在对话流内弹审批卡片（工具名+preview），四决策按钮；处理完就地折叠保留。
- **契约映射**：`permission.requested/granted/denied`；`permission.respond{tool_use_id, decision}`（allow_once / always_allow / deny_once / always_deny）。
- **验收**：审批期间 run 挂起（无事件推进）；点"允许一次"后收到 granted 且工具执行；过期审批（respond 返 false）卡片置灰不报错。

#### C6 Run 控制
- **故事**：run 进行中标题栏常驻"停止"按钮；点击中止。
- **契约映射**：`agent.abort{run_id}`；`run.finished` 收口（status 为终止态）。
- **验收**：中止后流式停、run 组显示已中止、输入框恢复；对已完成 run 调 abort 返 false 无副作用。

#### C7 上下文压缩（低优先级）
- **故事**：会话菜单"压缩历史"，显示前后 token 对比。
- **契约映射**：`session.compact`；`context.compacted` 事件（自动/手动均显示系统提示条）。
- **验收**：手动压缩后提示条出现，后续对话正常。

#### C8 边缘态
- 断线提示条；RPC 错误内联显示不打断会话；空会话引导文案；慢流卡片骨架态；所有异步动作有 loading/错误两态。
- **验收**：无 console 未处理异常。

### 里程碑 M2：可观测性

#### O1 用量仪表盘
- **故事**：侧栏入口查看每会话/每 run 的 token 用量与 Context 占比。
- **契约映射**：`llm.usage{input_tokens, output_tokens, context_pct}` 累计。
- **验收**：Context% 进度条接近阈值时提示"建议压缩"；run 结束后用量定格。

#### O2 Trace 时间线
- **故事**：回放历史 run，看事件序（工具耗时、权限、LLM 请求/响应）。
- **契约映射**：`event.subscribe{replay_from_run: <run_id>}` 回放 `events.jsonl`（llm.token/usage/session.* 不在其中）。
- **验收**：从会话的 run 列表点开某 run → 时间线展示其事件序；可跳转到对应消息位置。
- **依赖**：F5（`run.list` 或 session.run_ids 完整化）；M1 前可用已知 run_id 单点回放。

#### O3 调试模式
- **故事**：开发调试时按 topics/scope 订阅，查看 `llm.request` 完整 prompt。
- **契约映射**：`event.subscribe{topics:["llm.*"], scope:"run:<id>"}`。
- **验收**：调试面板可切换显示完整 LLM 请求/响应；默认关闭以降噪。

#### O4 断线恢复增强
- **故事**：重连后恢复进行中 run 的可见状态。
- **契约映射**：若 UI 记得 run_id → `event.subscribe{replay_from_run}` 恢复工具/权限事件；token 流不可恢复（提示"部分输出丢失"）。
- **依赖**：完整解决需 F5（`run.list` 发现 in-flight run）。

### 里程碑 M3：进阶控制台

#### A1 子 Agent 树
- **故事**：`subagent.started/finished` 渲染为嵌套 run 组树状视图。
- **验收**：父 run 内展开看到子 run 描述与状态；子 run 完成后结果回流父 run。

#### A2 Skill 管理
- **故事**：浏览 skills 目录、查看历史 skill 触发。
- **契约映射**：`skill.invoked` 记录；skills 目录浏览/触发**需后端新增 `skills.list` RPC**（见 §7）。
- **验收**：侧栏 skill 列表，点击以 `/name` 前缀发送触发。

#### A3 agent.run 工作台
- **故事**：无会话一次性任务独立视图（`agent.run`，run 完即结束，不落会话）。
- **契约映射**：`agent.run{goal}`。
- **验收**：工作台发任务→看流式+工具→run 结束归档。

#### A4 多会话并行
- **故事**：多标签/分屏，不同会话可同时跑（前端按会话串行，多会话并行）。
- **验收**：两个会话各自发消息，事件按 run_id 正确分流到对应视图。

#### A5 设置
- **故事**：本地配置（连接地址/端口、主题、限流提示）。
- **验收**：改连接地址后重连生效；暗/亮主题切换。

## 5. 非功能需求

- **性能**：token 流高频，DOM 更新经 `requestAnimationFrame` 批量合并；消息 >500 条启用虚拟列表；前端必须及时消费事件避免慢消费断连（缓冲 256）。
- **断线容忍**：重连不丢会话状态（`session.list` + `get_history` 恢复）；进行中 run 按 O4 策略恢复。
- **安全**：本地优先无鉴权；生产部署需 `SetAllowedOrigins` 收紧；无 HTTP API，仅 WS。
- **可访问性**：键盘可达基础（Enter 发送、Tab 切换、Esc 关闭卡片）；尊重 `prefers-reduced-motion`。

## 6. 技术架构与约束

- **栈**：React 19 + TypeScript + Vite。
- **状态**：Context + useReducer（会话列表/当前会话/连接状态/run 状态）；流式 token 用 ref + 局部 state 避免全树重渲染。不引入 Redux/Zustand。
- **单一 `ws-client` 模块（唯一协议边界）**：连接管理、envelope 编解码、id 匹配、事件分发、重连、parts 归并全在此；UI 只消费 parts 模型。
- **类型**：从 [webui-api-contract.md](webui-api-contract.md) §2–§4 手写 TS types 单文件，与 Go 结构体一一对应。
- **测试**：`ws-client` 单元测试（mock WS / 录制 fixture）；Go 侧 `gateway_test.go` 已覆盖协议契约（含 F1/F2/F3 修复回归）；E2E 手测脚本见 §8。
- **开发**：Vite `server.proxy['/ws'] → ws://localhost:7438`，直连真实 numbat-core，不建 mock server。
- **发布**：`vite build` 产物嵌入 numbat-core `/app/*`（依赖 F4）。
- **Markdown**：react-markdown（流式安全）+ 代码高亮 + 复制按钮。

## 7. 后端配合需求

| 需求 | 状态 | 影响阶段 |
|---|---|---|
| F1 WS 换行杂帧 | ✅ 已修复（`subscriber.write` 跳过 WS 换行） | — |
| F2 session.created/closed 广播 | ✅ 已修复（`SetBus` 补订阅） | M1-C2 |
| F3 重复订阅泄漏 | ✅ 已修复（`registerSubscriber` 安全替换） | — |
| F4 `/app/*` 静态 embed | ✅ 已实现（`internal/webui/embed.go` + gateway `/app/*`） | M1 发布期 |
| F5 `run.list`（运行历史摘要） | ✅ 已实现（`internal/trace/reader.go` 聚合 trace，handler `run.list`） | M2-O2/O4 |
| `skills.list` / `agents.list` RPC | 待实现 | M3-A2 |

## 8. 里程碑与交付顺序

代码分批推进，顺序如下：

**M1（核心闭环）**：C1 → C3（发送+流式）→ C2 → C4 → C5 → C6 → C8 → C7

**M2（可观测性）**：O1（可与 M1 后期并行）→ O3 → O2 → O4（依赖 F5）

**M3（进阶控制台）**：A1 → A4 → A3 → A2（依赖 skills.list）→ A5

## 9. 验收

**M1 手测脚本（对真实 numbat-core）**：
1. 新会话发"列出当前目录文件"——read_file/list_dir 卡片执行后流式回复。
2. 发"删除某个文件"——审批卡片出现，拒绝后工具卡片显示 denied。
3. run 进行中点停止——立刻收口，可继续对话。
4. 刷新页面——会话与历史完整恢复。
5. 杀 numbat-core 再重启——页面自动恢复连接与会话。

**M2/M3 增量验收**：
6. O1：长对话后 Context% 进度条接近阈值并提示压缩。
7. O2：点开历史 run 看到时间线事件序，可跳回消息。
8. O4：run 进行中刷新页面，工具/权限事件经回放恢复。
9. A1：触发子 Agent，看到嵌套 run 组树。
10. A4：两会话分屏并行，事件不串台。

## 10. 风险与开放问题

| 风险 | 缓解 |
|---|---|
| 高频 token 触发慢消费断连（缓冲 256） | `requestAnimationFrame` 批量合并 DOM 更新；必要时前端背压（节流渲染而非节流接收） |
| `send_message` 阻塞 + 断线 → UI 卡在等待 | 事件驱动为主；RPC 超时 ≥10min；断线即重连 + get_history 校正 |
| F5 未实现前 in-flight run 重连不可恢复 | M1 标注为已知限制；M2-O4 在有 run_id 时部分恢复 |
| skills.list 缺失阻塞 A2 | 降级：M3-A2 仅记录 `skill.invoked` 历史，不做目录浏览 |
