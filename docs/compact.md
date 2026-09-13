# internal/compact — 上下文压缩 + 截断

## Compactor（compactor.go）

六段式摘要 prompt：Original Goal / Completed Steps / Key Constraints & Discoveries / Current File State / Remaining TODOs / Critical Data。

把消息历史转为文本后调用 LLM 生成摘要；`summaryTokens = len/4` 估算。

## TruncateToolResults（budget.go）

tool_result 超过 `maxToolResultChars`（默认 8000）字符时保留前半部分，追加 `[... N chars omitted. Full output in run events.]`。

不修改原始 `Message` 对象（深拷贝后截断），原始内容保留在事件流中。

## 触发时机

`handler.autoCompactNeeded` 判断会话历史文本累计 > `compactThreshold`（默认 6000 字符），在下一轮 `session.send_message` 开始时触发。

压缩后写入两条消息：`[user 摘要, assistant "Understood..."]` 替换整个历史。
