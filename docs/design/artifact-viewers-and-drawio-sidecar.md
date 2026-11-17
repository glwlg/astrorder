# 工件查看器与 Sidecar

Sidecar 是会话与群聊内的多标签工作台，承载文件、代码、终端、浏览器、差异与图形预览。它的展开入口、会话详情和悬浮工作区变更面板各自独立。

## 来源与状态

工件解析必须保留来源身份和路径，不能仅凭操作系统路径格式推断来源。群聊中转述的产物路径通过产物目录回到首次发布者；读取和写入使用解析后的实际目标。

标签、当前选中项和布局由 `sidecarStore.ts` 维护。群聊和单聊的状态分别隔离，群聊内点击文件应留在对应群聊。切换或打开查看器时保持主消息流与输入器的可用状态。

## 查看器注册

查看器类型、文件匹配和打开行为以 `frontend/src/features/sidecar/registry.ts` 为准，具体实现位于该目录下的 `viewers/`。不要在文档中维护第二份固定数量或能力完成清单。

- 文件树和代码编辑器负责目录浏览、文本预览及受控保存。
- 终端通过后端和 Session Daemon 连接真实 PTY 或远程通道。
- 浏览器与 HTML 预览遵守各自的导航、嵌入和沙箱边界。
- 图形查看器处理 Draw.io、Mermaid、Excalidraw 和 Three.js 等格式。
- 侧边聊天使用独立会话，生命周期与消息状态由专用查看器维护。

文件读取、写入和下载调用当前 API 客户端与 Go 文件服务；路径校验、目标解析和并发写入检查由实际处理器决定，不把历史设计中的路由当成有效接口。

## 维护入口

| 职责 | 位置 |
| --- | --- |
| 宿主与标签栏 | `SidecarHost.tsx`、`SidecarTabs.tsx` |
| 工件结构、解析与匹配 | `types.ts`、`resolver.ts`、`registry.ts` |
| 状态隔离与会话记忆 | `sidecarStore.ts` |
| 文件来源与传输 | `FileSourceContext.ts`、`fileTransfer.ts` |
| 群聊打开与来源解析 | `frontend/src/features/chat/GroupSidecarPanel.tsx`、`GroupFileSourceScope.tsx` |
| 后端目标文件系统 | `app-server-go/internal/targetfs/` |

未标明根目录的前端文件均位于 `frontend/src/features/sidecar/`。
