# 星序 · 工件预览与 Sidecar 多功能工作台系统设计规范 (Artifact Viewers & Sidecar)

## 1. 概述与核心设计原则

Sidecar 是星序（Astrorder）构建在主对话流右侧的**多视口工件处理与工程工作台**。它解决传统 Chatbot 界面中「代码只能看不能改」、「终端分离割裂」、「流程图无法交互渲染」的痛点。

### 1.1 核心设计原则
1. **意图与产物分离（Decoupled Viewports）**：主视口专注意图交互、决策推理与指令时间线；右侧 Sidecar 承载高交互、高密度的富媒介（文件树、Monaco 代码编辑、xterm 终端、Git Diff、动态浏览器、流程图白板）。
2. **会话级状态隔离与记忆**：每个会话拥有独立的 Sidecar Tab 列表、当前活动 Tab 与分屏宽度，切换会话时平滑恢复现场，杜绝跨会话状态串扰。
3. **工作区物理边界沙箱（Workspace Boundary Isolation）**：所有文件的读、写、浏览与终端工作目录强绑定于会话当前配置的 `workspace` 物理路径，拒绝任意越界读写。
4. **统一工件解析管线（Unified Artifact Ingestion）**：无论来源于 Markdown 代码块、本地文件路径链接、Agent 工具调用输出，还是用户上传的文件附件，均被解析为规范化 `ArtifactRef` 实例。

---

## 2. 完整查看器矩阵 (Viewer Matrix)

星序 Sidecar 现已完整集成 9 类核心工件查看与交互引擎：

| 查看器标识 | 对应实现 | 核心能力与交互规范 |
|---|---|---|
| **filetree** | `FileTreeViewer` | 虚拟化工作区目录树，支持文件夹展开折叠、文件过滤搜索、一键派生为 Monaco 编辑器或终端 |
| **editor** | `MonacoViewer` | 完整 Monaco IDE 核心，支持语言语法高亮、只读比对、行号标注与受控原子写回工作区 |
| **terminal**| `XtermViewer` | 基于 xterm.js + Session Daemon 本地 PTY / 远程 SSH 通道，全功能支持 ANSI 颜色与键盘快捷键 |
| **browser** | `BrowserViewer` | 内嵌安全沙箱 Web 视图，支持页面后退/前进/刷新、移动端视口模拟与本地前端开发页即时预览 |
| **git_diff**| `GitDiffViewer` | 工作区 Git 状态分析、文件修改/暂存列表、行级红绿高亮差异对比与一键回退检查 |
| **diagram** | `DrawioViewer` / `MermaidViewer` / `ExcalidrawViewer` | 多引擎白板与架构图预览：支持原生 Draw.io XML 双向写回、Mermaid 实时渲染、Excalidraw 手绘流程梳理 |
| **html_preview** | `HtmlPreviewViewer` | 独立 iframe 沙箱，阻断父窗口 DOM 穿透，即时渲染 HTML/CSS/JS 静态原型 |
| **threejs** | `ThreejsViewer` | WebGL / Three.js 3D 模型与交互场景全屏/分屏三维空间渲染 |
| **sidechat**| `SidechatViewer` | 会话旁路研讨频道，支持从主会话任意轮次分叉探讨，避免上下文污染 |

---

## 3. 架构分层与通信状态机

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           Astrorder Artifact & Sidecar Architecture              │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 1. 产物感知与解析层 (Artifact Ingestion & Resolution)                                 │
│    - MarkdownContent 本地路径拦截 ([path](path:line))                               │
│    - 消息附件与工具产物识别 (tool_result -> file/patch/command)                      │
│    └─► 转换为规范化结构: Unified ArtifactRef                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 2. 状态机与宿主容器 (Sidecar Host & Deck State)                                  │
│    - Zustand: sidecarStore (tabs, activeTabId, isOpen, width, sessionHistory)   │
│    - 桌面端：320px ~ 70vw 自由拖拽分屏，支持双击折叠与快捷键 (Ctrl/Cmd + B)           │
│    - 移动端：自适应下沉为独立抽屉或弹出式 Sheet                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 3. 渲染器沙箱与通信协议 (Sandbox & Protocol Handling)                             │
│    - Monaco 模型同步与变更检测                                                    │
│    - xterm WebSocket 二进制/文本全双工流式传输                                      │
│    - postMessage 协议状态机 (Draw.io: configure -> init -> load -> save)         │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 4. 工作区安全存储 API (Workspace Boundary File APIs)                              │
│    - GET  /api/v1/sessions/{id}/artifacts/content?path=...                      │
│    - PUT  /api/v1/sessions/{id}/artifacts/content?path=... (With If-Match/ETag)  │
│    - 强路径校验: Path.resolve() 必须位于 session.workspace 规范物理路径下          │
└─────────────────────────────────────────────────────────────────────────────────┘
```
