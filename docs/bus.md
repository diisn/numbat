# internal/bus — JSON-RPC 2.0 Envelope

## Envelope（envelope.go）

协议帧定义：

```json
{"jsonrpc":"2.0","id":1,"method":"agent.run","params":{"goal":"hello"}}
{"jsonrpc":"2.0","id":1,"result":{...}}                    // 响应
{"jsonrpc":"2.0","result":{"type":"run.started",...}}      // 事件（无 id）
```

## RPCError

```go
type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}
```

标准错误码：`-32700` 解析错误、`-32600` 无效请求、`-32601` 方法不存在、`-32000` 服务端错误。
