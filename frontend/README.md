# 星序 · Astrorder 前端

本目录是星序本地 Agent 控制台的 React/Vite 前端。它只通过 `docs/CONTRACT.md` 定义的 HTTP 和 WebSocket 通道工作；未认证、空列表、断线和未报告能力都会显示为真实状态，不填充示例 Agent 或消息。

## 开发

```text
npm install
npm run dev
```

Vite 固定在 `127.0.0.1:30001`，并把 `/api`、`/health` 和 `/ws` 代理到 `127.0.0.1:30002`。生产构建：

```text
npm run test
npm run lint
npm run build
```

## 页面

- `/chat`：按 `agent_id + session_id` 隔离的会话、历史分页、Markdown、附件、命令 outbox、排队、停止和审批。
- `/monitor`：共享同一 Zustand 事件状态的多会话监控卡片。
- `/agents`：真实连接能力、限制和服务端白名单运行时请求。

浏览器命令只走 HTTP；事件 WebSocket 只读并按 durable cursor 重放。会话认证由服务端 HttpOnly cookie 持有，前端不会把凭证写入 URL 或 localStorage。

## 集成浏览器测试

`npm run test:e2e` 默认连接显式的 `ASTRORDER_E2E_BASE_URL`；仅 `ASTRORDER_E2E_START_DEV_SERVER=1` 时启动 Vite（端口 30001）。FastAPI 需要另行运行。设置 `ASTRORDER_E2E_TOKEN` 只适用于隔离的认证环境。
