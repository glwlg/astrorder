# 星序 · Astrorder — 产品与技术全景架构规范

**品牌定义**：星序 · Astrorder。寓意：*群星各有所长，协作自有秩序*。
**产品定位**：面向工程落地与沉浸式人机协作的**本机优先、多智能体协同控制工作台与工件处理中心**。原生直连 Codex、Hermes、Grok 与远程 SSH 服务器，跨桌面端（Electron / Web）与移动端（独立触屏工作台）提供一致的控制与观察面。

---

## 1. 系统架构分层

### 1.1 大小内核分离架构 (Dual-Kernel Architecture)
- **常驻小内核（Session Daemon，Port 30009）**：
  - 极简稳定常驻进程，负责底层 Agent Runtime（Codex app-server、Hermes CLI、Grok、SSH 远端连接及本地 PTY 终端）子进程树守护与命令中断。
  - 内存 WAL 环形缓冲区（Ring Buffer）：即使大内核升级重启，输出与事件流无损缓冲，重连后基于 `seq_id` 增量补推，实现自迭代自维护永不断联。
- **业务大内核（App Server，FastAPI / Uvicorn，Port 30001 / 30002）**：
  - 承载 Web/REST API、WebSocket 双工流、前端单页应用（SPA）托管、工作区工件解析及 SQLite 数据持久化。支持高频热更新与重启，不干扰底层正在执行的 Agent 任务。
- **跨平台客户端**：
  - **桌面客户端（Electron）**：原生窗口管理器、系统托盘、多窗口画廊（图片/工件独立弹出）、快捷启动与自检脚本。
  - **移动端独立壳（Responsive Touch Shell）**：针对 390px 级竖屏与移动视口专门构建手势抽屉、底部 Sheet 交互与离线队列缓冲，非桌面界面的机械压缩。

### 1.2 核心技术栈
- **前端**：React 18 · TypeScript · Vite · Mantine UI · Zustand · Monaco Editor · xterm.js · Lucide / Tabler Icons · react-markdown + remark-gfm
- **后端**：Python 3.11+ · FastAPI · Pydantic · SQLAlchemy · SQLite · Uvicorn · Asyncio
- **客户端**：Electron · Node.js
- **测试保障**：Vitest · Playwright (CDP) · pytest · Ruff

---

## 2. 核心功能规范

### 2.1 多智能体统一接入与环境管理
1. **真原生直连**：会话 ID、线程上下文、模型选择与审批模式完全以原生底层运行时为准，星序只做控制面、观察面和工件台，不建立割裂的第二套会话对账体系。
2. **多 Agent 运行时支持**：
   - **Codex**：通过 Codex app-server JSON-RPC 协议进行会话生命周期托管与双向调度，支持模型与思考深度调节、三档审批流（每次询问 / 自动批准 / 完全访问）以及 CDP 图像附件传输。
   - **Hermes**：通过受控插件包装器与 TUI-gateway 原生接入，支持多轮次流式观察与模型切换。
   - **Grok**：支持远端与本地 Grok 运行时深度推理及上下文同步。
   - **SSH 远端环境**：支持通过私钥引用与别名跨网段托管远程开发机上的 Agent，无明文凭证存储、无 shell 拼接注入风险。
3. **跨 Agent 协作交接（Handoff）**：支持在不同智能体之间无缝继承上下文与关键工程产物，多 Agent 协同攻关。

### 2.2 沉浸式 Sidecar 工件工作台
主会话右侧是具备会话状态记忆的多 Tab 自由分屏工作台：
1. **工作区文件树（FileTreeViewer）**：树状直观浏览当前项目源码，严格受限于工作区沙箱安全边界。
2. **代码编辑器（MonacoViewer）**：IDE 级高亮、语法检测、代码只读审查与即时写回。
3. **交互终端（XtermViewer）**：基于真实 PTY 终端与 SSH 管道，随时接管环境操作。
4. **内置浏览器（BrowserViewer）**：边开发边预览 Web 页面，即时闭环验证。
5. **Git Diff 审查（GitDiffViewer）**：工作区变更树、分支状态追踪与文件行级 Diff。
6. **流程图与白板（Draw.io / Mermaid / Excalidraw）**：可视化系统架构、时序交互与手绘草图。
7. **HTML / 3D 渲染（HtmlPreviewViewer / ThreejsViewer）**：静态网页即时渲染与 Three.js 3D 模型三维交互预览。
8. **旁路侧聊（SidechatViewer）**：不污染主会话上下文的轻量级技术研讨频道。

### 2.3 全景监控室 (Monitor Room)
- 独立路由与全局多任务阵列，聚合展示所有正在执行、待审批（Waiting Approval）、已就绪与异常的会话。
- 决策与工具调用审计流透明可见，支持秒级一键钻取回原始对话与 Sidecar 现场。

---

## 3. 安全与运行基准

1. **凭证隔离与双轨防线**：浏览器访问凭证仅采用 HttpOnly Cookie 存储，与 Agent 连接器通讯密钥物理隔离，防范 XSS 窃取。
2. **工作区边界保护**：所有本地/远程文件读写、命令执行均强校验会话工作区物理路径，坚决防范路径穿越（Path Traversal）。
3. **真实运行时原则**：拒绝在生产环境填充假数据或伪造「已连接」状态；空状态即代表无活动连接，透明可靠。
