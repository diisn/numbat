# internal/trace — 事件落盘

## Writer（writer.go）

订阅 14 类事件，按 `run_id` 反射提取后写入 `<runsDir>/<runID>/events.jsonl`。

- 每条记录附加 `ts` 与 `type` 字段
- **单次 JSON 序列化**：事件只做 1 次 `json.Marshal`（把 ts 和 type 预注入 map 再序列化），非 3 次序列化拼接
- 按 run 隔离目录
- `Close` 统一关闭文件句柄

## GlobalWriter

全局 trace 文件（`~/.numbat/trace/global.jsonl`），记录跨 run 的 TraceRecord。

- `Write` 加锁保护写入
- nil file 时 Write 静默跳过，Close 安全
