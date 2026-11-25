# 星序 · Astrorder 前端

本目录是星序工作台的 React/Vite 前端。业务请求通过 HTTP 与 WebSocket 连接 Go App Server（详见[架构说明](../docs/ARCHITECTURE.md)），桌面系统能力通过 Wails 适配层调用。

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

- `/`、`/chat`：按 `agent_id + session_id` 隔离的会话工作台与首页。支持**办公模式**（「今日办公工作台」首页、个人办公画像、多文档资料包协同、8 大场景起草模板、所见即所得富文档写作台、Word/Excel 图表透视/PPT/PDF 预览导出）与**研发模式**（工作区文件树、Monaco 编辑、PTY/SSH 终端、Git Diff/Worktree、SQL 内联执行、Swarm 团队、定时任务、附件、排队、停止和审批）。
- `/meetings`：实时会议工作台（本地 R2T2 / 云端多级转写、边听边总结、发言人标记、崩溃草稿恢复、行动项闭环与桌面悬浮录音胶囊联动）。
- `/digest`：工作周报与复盘工作台（按时间跨度自动汇总会议纪要、办公文档、AI 对话与研发活动，多套模板一键成稿并支持直存工作空间）。
- `/inbox`：统一待办与行动收件箱（聚合个人待办、会议行动项、定时提醒与 AI 待确认请求）。
- `/monitor`：共享同一 Zustand 事件状态的多会话监控矩阵（研发模式）。
- `/groups`：多 Agent 群聊、成员管理、共享黑板与产物（研发模式）。
- `/analytics`：模型网关用量与 Agent CLI 统计（研发模式）。
- `/backups`：配置与工作区备份恢复（研发模式）。
- `/accounts`：Agent 账号与配额切换（研发模式）。
- `/settings`、`/plugins`：采用「一般配置 / 高级配置」双层结构的设置中心，涵盖常规外观、个人办公画像、统一 AI 供应商管理、常用集成、快捷键、可插拔 Agent 引擎与功能插件、连接与工具链管理。
- `/mobile`：独立触屏移动端工作台与 PWA 入口。

浏览器命令走 HTTP；事件 WebSocket 只读并按 durable cursor 重放。认证由 Go App Server 与桌面适配层共同处理，浏览器和桌面入口的凭据传递方式以 `src/api/` 和 `src/desktop/adapter.ts` 为准。

安装版构建使用 `ASTRORDER_WAILS=1`，关闭 WebView2 内的 PWA Service Worker。独立图片窗口与悬浮会议胶囊分别使用 `desktop-wails/assets/image-preview.html` 与 `desktop-wails/assets/meeting-capsule.html`；完整打包流程见[桌面构建说明](../desktop-wails/README.md)。

## 集成浏览器测试

`npm run test:e2e` 默认连接显式的 `ASTRORDER_E2E_BASE_URL`；仅 `ASTRORDER_E2E_START_DEV_SERVER=1` 时启动 Vite（端口 30001）。Go App Server 需要另行运行。设置 `ASTRORDER_E2E_TOKEN` 只适用于隔离的认证环境。
