# 星序 · Astrorder

群星各有所长，协作自有秩序。

星序是一套本机优先、跨设备的 **Hermes / Codex 控制工作台**。它不是第二套会话系统，也不依赖 Hermes Desktop：会话 ID、线程、模型、批准模式都以原生运行时为准，星序只做控制面、观察面和工件工作台。

电脑用侧栏 + 对话 + 右侧 Sidecar；手机用独立工作台（抽屉 / 对话 / Sheet）。界面以中文为主，支持明暗主题。

---

## 能做什么

### 原生 Agent 接入

- **本机自动发现** Hermes 与 Codex，一键接入或断开。不会用示例数据假装「已连接」。
- **SSH 远程环境**：保存主机 / alias / 端口 / 用户 / 私钥路径引用，再发现远端 Agent。不存密码或私钥内容，不关闭 host-key 校验，不拼接 shell 字符串。
- 星序只操作自己拉起的 TUI-gateway / Codex app-server；不断开 Desktop、Overlook 或其他无关进程。
- 指令走 HTTP，事件走 WebSocket。`accepted` 只表示原生运行时接受调度，不伪造 `completed`；结果不明时保持 `unknown`，不自动重发。

### 会话与对话

- 按项目分组的会话轨：搜索、置顶、筛选、新建 / 删除会话与项目。
- 权威转录：用户 / 助手 / 思考 / 工具调用分区展示，Markdown + GFM，代码与本地路径可点开。
- 输入条：文本、图片 / 文件附件、粘贴、语音、停止、队列。草稿与附件在断线后仍保留。
- 同一条已接受指令不会长出平行的乐观气泡；两条合法相同内容仍是两条。
- 流式时默认贴底；向上浏览会暂停跟随，手动回到底部后恢复。
- 附件走认证同源下载，灯箱预览图片。

### 原生控制

- **模型**：读取运行时已认证的模型目录，切换需原生 `config.set` 确认。
- **思考强度**：minimal / low / medium / high / max；桌面滑块松手后提交。
- **批准模式**：请求批准 / 帮我批准 / 完全访问，写回原生会话。
- Git 状态条：当前分支、改动统计，可打开工作区 Diff。

### 桌面 Sidecar 工作台

对话右侧是可拖宽的多 Tab 工作台，按会话记忆布局，切换会话不会串 Tab：

| 能力 | 说明 |
|---|---|
| 文件树 | 浏览当前会话工作区 |
| Monaco | 代码 / 文本编辑与写回 |
| 终端 | 本机或 SSH 工作区交互终端 |
| 浏览器 | 内嵌浏览会话相关页面 |
| Git Diff | 工作区变更树与文件对比 |
| Draw.io / Mermaid / Excalidraw | 流程图、架构图、手绘白板 |
| HTML / 3D | 静态页与 Three.js 预览 |
| 侧边聊天 | 相对主会话的旁路对话 |
| 决策状态机 | 从消息 / 工具时间线观察决策 |

对话里的工作区路径、附件和工具产物会解析成统一工件，再交给对应查看器。插件可在「插件管理」里开关和调参。

### 手机工作台

窄屏或横屏矮窗口自动进入独立壳，不把桌面 Sidecar 硬塞进 390px：

- 左缘滑开会话抽屉；抽屉内竖滑不关，斜滑切换开放会话。
- 模型、批准、连接、队列、任务用底部 Sheet，一次只做一件事。
- 附件预览、语音输入、系统通知、会话卡片切换。

### 监控室

独立路由，桌面多会话网格 / 列表，展示运行状态、最近工具活动、待批准事项，并钻回对应对话。与聊天共用同一份事件流，没有第二套对账管道。

### 安全边界

- 默认只绑回环：开发 `127.0.0.1:30001`（Vite）→ `30002`（API / WS）；生产可单进程托管 SPA + API。
- 浏览器密钥与连接器密钥必须分开。浏览器凭证只进 HttpOnly Cookie，不进 URL 或 localStorage。
- 附件、工作区文件读写都限制在会话工作区；路径穿越会被拒绝。
- 空列表就是没有已连接 Agent，不会填充假数据。

---

## 技术栈

| 层 | 选型 |
|---|---|
| 前端 | React / TypeScript / Vite · Mantine · TanStack Query · Zustand · Monaco · xterm |
| 后端 | FastAPI / Pydantic / SQLAlchemy / SQLite / Uvicorn |
| 连接器 | `connectors/hermes` 插件 + 自有 TUI-gateway；`connectors/codex` 走 Codex app-server JSON-RPC |
| 测试 | Vitest / Playwright · pytest / Ruff |

协议见 [`docs/CONTRACT.md`](docs/CONTRACT.md)。星序是控制器：`Session.id` 就是原生持久会话 / 线程 ID。

---

## 运行

前置：Python 3.11、`uv`、Node/npm。生产和联调都只绑回环，不要把服务暴露到公网。分别设置 `ASTRORDER_BROWSER_SECRET` 与 `ASTRORDER_CONNECTOR_SECRET`。

```text
cd backend && uv sync --extra dev
cd ../frontend && npm ci
```

开发（两个终端）：

```text
cd backend
ASTRORDER_HOST=127.0.0.1 \
ASTRORDER_PORT=30002 \
ASTRORDER_BROWSER_SECRET='<browser-secret>' \
ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
ASTRORDER_ALLOWED_ORIGINS='http://127.0.0.1:30001,http://localhost:30001' \
uv run astrorder-server

cd frontend
npm run dev
```

打开 `http://127.0.0.1:30001`。Vite 代理 `/api`、`/health`、`/ws` 到 30002。5173 留给其他项目。

单进程部署：先 `cd frontend && npm run build`，再让 FastAPI 托管静态资源：

```text
cd backend
ASTRORDER_HOST=127.0.0.1 \
ASTRORDER_PORT=30002 \
ASTRORDER_BROWSER_SECRET='<browser-secret>' \
ASTRORDER_CONNECTOR_SECRET='<connector-secret>' \
ASTRORDER_ALLOWED_ORIGINS='http://127.0.0.1:30002' \
ASTRORDER_STATIC_DIR='../frontend/dist' \
uv run astrorder-server
```

Windows 可用 `scripts/start_silent.py` / `scripts/setup_shortcuts.py` 做后台启动和桌面快捷方式。

---

## 验证

```text
cd backend
uv run pytest -q
uv run ruff check .
uv run ruff check ../connectors --output-format=concise
uv run ruff check ../scripts --output-format=concise

cd ../frontend
npm run test
npm run lint
npm run build
```

真实浏览器联调必须用私有临时 SQLite、测试密钥和 `scripts/inert_connector.py`，不要拿用户会话做门禁。安全门禁脚本：`scripts/verify_isolated_security.py`。Playwright：`npm run test:e2e`（需指定本机 Chromium 与隔离服务地址）。

本机 Hermes 连接会在当前 profile 的 `plugins/astrorder-hermes` 写入经过一致性检查的 wrapper，并启用插件；不改 Hermes core、其他 profile、Desktop 或 Overlook。移除时先断开星序，再 `hermes plugins disable/remove astrorder-hermes`。

---

## 文档

- 产品目标：[`docs/PRODUCT.md`](docs/PRODUCT.md)
- 协议：[`docs/CONTRACT.md`](docs/CONTRACT.md)
- 工件 / Sidecar 设计：[`docs/design/artifact-viewers-and-drawio-sidecar.md`](docs/design/artifact-viewers-and-drawio-sidecar.md)
- 待优化清单：[`docs/tasks/optimization-backlog.md`](docs/tasks/optimization-backlog.md)
- 连接器能力与限制：[`connectors/README.md`](connectors/README.md)
- 联调报告：[`docs/reports/integration.md`](docs/reports/integration.md)
- 本机 Hermes / SSH 实机结果：[`docs/reports/hermes-local-ssh.md`](docs/reports/hermes-local-ssh.md)

`Hermes-plugins/overlook` 仅作只读交互参考，不修改运行中的中继或 Desktop 插件。
