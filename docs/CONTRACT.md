# 通信契约

星序对浏览器提供 HTTP/JSON 与 WebSocket，对 Agent 提供 CLI/MCP 能力；大小内核之间使用 protobuf/gRPC。各通道使用独立身份与鉴权，不能将浏览器凭据用于连接器或内核控制。

## 契约来源

| 内容 | 权威维护位置 |
| --- | --- |
| HTTP 路由和处理器 | `app-server-go/internal/httpapi/` 及各业务模块 |
| OpenAPI 兼容性定义 | `app-server-go/internal/httpapi/openapi.json`，服务的 `/openapi.json` 与 `/docs` |
| 前端请求与共享类型 | `frontend/src/api/client.ts`、`frontend/src/domain/types.ts` |
| 浏览器事件通道 | `frontend/src/api/eventStream.ts`、`app-server-go/internal/application/eventhub.go` |
| Agent 能力目录与鉴权 | `app-server-go/internal/agentapi/` |
| 内核消息、服务与流 | `contracts/proto/astrorder/sessiond/v1/sessiond.proto` |

OpenAPI 保留公开兼容性定义，新增 Go 路由还须核对实际处理器，不把冻结基线当成全部实时能力目录。避免在说明文档里复制另一套容易过期的接口和 TypeScript 类型。

## 身份与数据

- JSON 产品字段使用 `snake_case`；消息、会话、连接和操作标识符是不透明身份，不从标题、时间或文件名派生。
- 会话以 `agent_id + session_id` 隔离，原生会话和历史由原生 Agent 拥有。App 保存产品数据、投影与队列，不建立独立的原生历史权威副本。
- 本地、WSL 与 SSH 的路径必须连同来源解析。群聊产物保存首次发布者的来源，后续相同路径按已有产物引用。
- 缺失、显式空值、空字符串、零与 false 按对应契约区分，不通过浮点数中转原生大整数标识。

## 浏览器请求与事件

HTTP API 位于 `/api/v1`。浏览器事件位于 `/ws/v1/events?after={cursor}`，用于只读事件和断线重放；命令通过 HTTP 提交，不通过浏览器事件 socket 发送。

客户端按事件身份去重并推进 durable cursor，重连后从确认游标恢复。收到指令受理、队列提交或操作创建回执，不等于模型已经完成本轮；最终结果须结合对应的 command、turn、会话状态和消息判断。

认证方式由服务与桌面适配层实现。不得在日志或文档中保存实际 token、Cookie、私钥、连接器密钥或 gRPC 凭据。

## 内核 gRPC

Go 大内核通过生成的客户端连接 Go 小内核。请求按具体 protobuf RPC 定义，不使用任意方法名和 JSON 的通用转发入口。原生扩展使用 `NativeJson` 保留原始字节和精确数字。

事件流区分快照、重放、实时就绪、实时事件、重同步与溢出；终端使用双向流传递输入、输出、大小变化、关闭与游标。修改消息结构后须重新生成 Go 代码并验证描述符、presence、大整数和流边界。

结果不确定的变更不能自动以新操作身份重新执行。进程托管、WAL 和模型配置执行属于小内核，HTTP 和产品编排属于大内核。详见 [gRPC 契约](../contracts/README.md) 与[大小内核架构](design/dual-kernel-architecture.md)。
