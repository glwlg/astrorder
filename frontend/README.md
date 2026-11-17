# 星序 · Astrorder 前端

本目录是星序工作台的 React/Vite 前端。业务请求通过 [HTTP 与 WebSocket 契约](../docs/CONTRACT.md) 连接 Go App Server，桌面系统能力通过 Wails 适配层调用。

## 开发

```text
npm ci
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
- `/groups`：群聊、成员管理、共享黑板与产物。
- `/swarm`：分布式任务与拓扑。
- `/plugins`：连接、能力与服务设置。
- `/analytics`：模型网关统计与 Agent CLI 使用情况。

浏览器命令走 HTTP；事件 WebSocket 只读并按 durable cursor 重放。认证由 Go App Server 与桌面适配层共同处理，浏览器和桌面入口的凭据传递方式以 `src/api/` 和 `src/desktop/adapter.ts` 为准。

安装版构建使用 `ASTRORDER_WAILS=1`，关闭 WebView2 内的 PWA Service Worker。独立图片窗口使用 `desktop-wails/assets/image-preview.html` 和 `src/native/imagePreviewGlass.ts`；完整打包流程见[桌面构建说明](../desktop-wails/README.md)。

## 集成浏览器测试

`npm run test:e2e` 默认连接显式的 `ASTRORDER_E2E_BASE_URL`；仅 `ASTRORDER_E2E_START_DEV_SERVER=1` 时启动 Vite（端口 30001）。Go App Server 需要另行运行。设置 `ASTRORDER_E2E_TOKEN` 只适用于隔离的认证环境。
