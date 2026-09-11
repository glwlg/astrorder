# 星序 · 工件预览与扩展查看器系统设计规范 (Artifact Viewers & Sidecar)

## 1. 概述与设计原则

本规范定义星序（Astrorder）中会话工件（文件、附件、结构化输出等）的通用预览、交互渲染与侧边栏工作台（Sidecar）架构，以 `.drawio` 流程图在会话右侧视口交互式渲染与受控写回为首个落地场景。

### 1.1 背景与目标
1. **视图解耦（View Decoupling）**：左侧/中心消息流专注意图表达与对话时间线，右侧工作台（Sidecar）承载高交互、高密度的富媒介（流程图、原型设计、代码Diff、图表、PDF）。
2. **渐进式扩展架构（Artifact Viewers）**：避免一次性引入不可控的动态外部代码注入，第一阶段采用强类型、编译期安全的 `ArtifactViewer` 体系；第二阶段平滑演进至沙箱化微前端/iframe 运行时插件系统。
3. **工作区安全与权限闭环（Security & Isolation）**：严禁无限制的操作系统路径写入；所有文件存取基于当前会话工作区作用域（Session Workspace Boundary），支持只读预览、显式授权写回与版本乐观锁（Revision/ETag）。
4. **统一入口抽象（Unified Artifact Resolver）**：无论是 Markdown 中的绝对路径/相对路径、会话附件（Attachments）、还是工具调用产生的文件产物，均走统一的工件解析管线。

---

## 2. 总体架构分层

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           Astrorder Artifact & Sidecar Architecture              │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 1. 产物感知与解析层 (Artifact Ingestion & Resolution)                                 │
│    - MarkdownContent 链接拦截 (isLocalPath / isArtifact)                         │
│    - Message Attachments 列表                                                    │
│    - Tool Call Result 产物提取                                                   │
│    └─► 转换为规范化结构: Unified ArtifactRef                                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 2. 注册与分发层 (Artifact Viewer Registry)                                       │
│    - ArtifactViewerRegistry (单例/扩展点)                                         │
│    - 匹配规则: extensions, mimeTypes, pathRegex                                 │
│    └─► 决定渲染器: DrawioViewer | ImageViewer | MarkdownViewer | FallbackRawViewer│
├─────────────────────────────────────────────────────────────────────────────────┤
│ 3. 宿主与状态管理层 (Sidecar Host & Deck State)                                  │
│    - Zustand: sidecarStore (tabs, activeTabId, isOpen, width, dirtyState)       │
│    - 响应式侧栏: 桌面端 300px~70vw 可拖拽抽屉; 移动端 Sheet/Drawer                  │
│    - 会话生命周期隔离: 会话切换时持久化或安全卸载，杜绝跨会话串图                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 4. 渲染器沙箱与通信层 (Isolated Renderers & Protocols)                             │
│    - 嵌入协议状态机 (Draw.io postMessage protocol: configure -> init -> load)     │
│    - 域名与 Origin 校验, 禁用通配符 `*` 通信                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│ 5. 后端工作区安全存储 API (Workspace Boundary File APIs)                          │
│    - GET  /api/v1/sessions/{id}/artifacts/content?path=...                      │
│    - PUT  /api/v1/sessions/{id}/artifacts/content?path=... (With If-Match/ETag)  │
│    - 强路径校验: resolve() 后必须位于 session.workspace 物理路径下               │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 核心类型契约 (Contracts)

### 3.1 统一工件引用 (ArtifactRef)

```typescript
// frontend/src/domain/artifact.ts

export type ArtifactKind = 'workspace_file' | 'attachment' | 'remote_url' | 'inline'

export interface ArtifactRef {
  /** 唯一标识符，可用于 Sidecar Tab Key */
  id: string
  /** 归属会话与代理 */
  sessionId: string
  agentId: string
  /** 工件类型 */
  kind: ArtifactKind
  /** 相对工作区路径或展示名称 */
  name: string
  /** 本地工作区绝对/相对路径（若适用） */
  path?: string
  /** 读取内容地址 */
  readUrl: string
  /** 媒体类型 */
  mediaType: string
  /** 对应后端或内容校验修订版本（乐观锁） */
  revision?: string
  /** 是否允许编辑并回写工作区 */
  writable: boolean
}
```

### 3.2 查看器接口定义 (ArtifactViewer)

```typescript
// frontend/src/features/sidecar/types.ts
import type { ComponentType, ReactNode } from 'react'
import type { ArtifactRef } from '../../domain/artifact'

export interface ViewerContext {
  artifact: ArtifactRef
  onSave?: (content: string | Blob, expectedRevision?: string) => Promise<string /* new revision */>
  onDirtyChange?: (isDirty: boolean) => void
  onClose?: () => void
  notifyMessage?: (msg: string, level?: 'info' | 'error' | 'success') => void
}

export interface ArtifactViewer {
  id: string
  title: string
  icon: ComponentType<{ size?: number; className?: string }>
  /** 是否支持处理此工件，返回值代表优先级，>0 表示可处理，数字越大越优先 */
  supports: (artifact: ArtifactRef) => number
  /** 渲染侧栏主视图 */
  component: ComponentType<ViewerContext>
  /** 渲染内联卡片操作徽标（例如 Markdown 链接旁边的快捷预览按钮） */
  renderBadge?: (artifact: ArtifactRef, onOpen: () => void) => ReactNode
  /** 权限与特性配置 */
  capabilities: {
    canEdit: boolean
    requiresNetwork?: boolean
  }
}
```

---

## 4. Draw.io 查看器协议与实现规范

### 4.1 通信状态机（基于 diagrams.net Embed Mode）
`embed.diagrams.net` 或本地离线 Draw.io 部署均基于 HTML5 Messaging API 协议交互：

1. **iframe 加载**：
   - 目标 URL：`https://embed.diagrams.net/?embed=1&proto=json&configure=1&spin=1&libraries=1`（或由配置指向本地静态资源）。
2. **接收 `configure` 事件**：
   - Draw.io 向宿主发送：`{"event": "configure"}`。
   - 宿主响应：
     ```json
     {
       "action": "configure",
       "config": {
         "defaultFonts": ["Inter", "PingFang SC", "Microsoft YaHei"],
         "compressXml": false
       }
     }
     ```
3. **接收 `init` 事件**：
   - Draw.io 初始化完毕，发送：`{"event": "init"}`。
   - 宿主读取本地/接口返回的 XML 内容，响应 `load` 动作：
     ```json
     {
       "action": "load",
       "autosave": 1,
       "xml": "<mxGraphModel>...</mxGraphModel>"
     }
     ```
4. **编辑与暂存（autosave / save）**：
   - 当用户在画布修改图形，Draw.io 派发：`{"event": "autosave", "xml": "..."}`。
   - 宿主捕获最新 XML 并标记工作台处于 `dirty` 状态，更新顶部状态指示灯。
5. **保存回写（save action）**：
   - 用户点击顶部“保存到工作区”或 `Ctrl+S` 时，宿主调用后端安全写回接口，带上 `If-Match: revision`。写回成功后清空 dirty 标记。

### 4.2 安全与防范措施
- **严格校验 Origin**：在 `window.addEventListener('message')` 中，必须首先校验 `event.origin` 与预设安全域（例如 `https://embed.diagrams.net` 或星序自托管静态服务域）完全一致，且 `event.source === iframeRef.current?.contentWindow`。
- **杜绝 `postMessage('*')`**：所有发往 iframe 的指令必须显式传入明确的 `targetOrigin`，禁止使用通配符。
- **iframe Sandbox 最小权限**：
  ```html
  <iframe
    sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
    allow="clipboard-read; clipboard-write"
  />
  ```

---

## 5. 后端工作区安全访问设计

在 `backend/src/astrorder/api.py` 中为会话工件提供边界受限的访问 API：

### 5.1 工件获取：`GET /api/v1/sessions/{session_id}/artifacts/content`
- **入参**：`path: str`（支持工作区相对路径或安全绝对路径）。
- **校验规则**：
  1. 鉴权通过（Cookie 或 Bearer 验证）；
  2. 获取 `session = session_repo.get(session_id)` 及其 `session.workspace`；
  3. `target_path = (Path(workspace) / path).resolve()`；
  4. **路径穿透校验**：断言 `target_path.is_relative_to(Path(workspace).resolve())`；
  5. 校验文件存在且为常规文件（`is_file()`），大小不超过单文件上限（如 50MB）；
- **响应头**：
  - `ETag: W/"<mtime>-<size>"`（作为客户端 revision）；
  - `Content-Type: application/vnd.jgraph.mxfile` 或相应 MIME；
  - `Cache-Control: no-cache`。

### 5.2 工件写回：`PUT /api/v1/sessions/{session_id}/artifacts/content`
- **入参**：
  - Header: `If-Match: "<revision>"`；
  - Body: JSON `{ "path": str, "content": str }`。
- **校验规则**：
  1. 路径边界检查与文件扩展名限制（如仅允许 `.drawio`, `.xml`, `.json`, `.svg`, `.md` 等）；
  2. 乐观并发校验：若文件当前 `mtime-size` 与 `If-Match` 不匹配，返回 `412 Precondition Failed`，阻止静默覆盖 Agent 并发修改；
  3. **原子安全写入**：写入同一目录下的临时文件（如 `.filename.tmp`），刷盘后 `os.replace` 替换原文件；
  4. 返回更新后的 `ETag`。

---

## 6. 前端 UI 与 Sidecar 交互改造

### 6.1 布局容器结构重构
将 `ChatPage.tsx` 中传统的 `desktop-details` 单一容器升级为通用的 `SidecarHost`：

```tsx
<div className={`chat-layout${sidecarOpen ? ' has-sidecar' : ''}`}>
  <section className="chat-column">
    <SessionRuntimeBar ... />
    <Transcript ... />
    <ChatComposer ... />
  </section>

  {sidecarOpen && (
    <aside className="desktop-sidecar">
      {/* 多标签导航栏 */}
      <SidecarTabs
        tabs={tabs}
        activeId={activeTabId}
        onSelect={setActiveTabId}
        onClose={closeTab}
      />
      {/* 活跃标签内容区 */}
      <div className="sidecar-body">
        {activeTab?.type === 'details' && <SessionDetails ... />}
        {activeTab?.type === 'artifact' && (
          <ActiveViewer
            artifact={activeTab.artifact}
            onSave={handleSaveArtifact}
          />
        )}
      </div>
    </aside>
  )}
</div>
```

### 6.2 链接与附件统一分发器
无论是 `MarkdownContent.tsx` 里的 `a` 标签，还是 `Transcript.tsx` 里的 `AttachmentList`：
```typescript
export function handleArtifactClick(source: { path?: string; url?: string; name: string }) {
  const artifact = resolveArtifact(source, currentSession)
  const viewer = artifactViewerRegistry.findViewer(artifact)

  if (viewer) {
    sidecarStore.openArtifactTab(artifact, viewer)
  } else if (artifact.kind === 'workspace_file') {
    // 回退到系统本地打开机制
    void openSystemFile(artifact.path)
  } else {
    window.open(artifact.readUrl, '_blank')
  }
}
```

---

## 7. 落地阶段与里程碑

1. **Phase 1: 基础契约与只读预览 (MVP)**
   - 实现 `ArtifactRef`、`artifactViewerRegistry` 与 `sidecarStore`；
   - 改造 `ChatPage.tsx` 侧栏，支持在右侧并列打开 SessionDetails 与只读 DrawioViewer；
   - 适配 `embed.diagrams.net` 的 `configure -> init -> load` 完整状态机并固定 origin。
2. **Phase 2: 工作区读写闭环与并发控制**
   - 增加后端带工作区越界检查的 `/api/v1/sessions/{id}/artifacts/content` 读写接口；
   - 支持 Draw.io 侧栏的“保存修改”按钮、`dirty` 状态指示及关闭前防丢确认。
3. **Phase 3: 扩展查看器矩阵**
   - 接入更多常用格式：Mermaid 流程图、SVG 矢量图、HTML 原型沙箱预览、Markdown 文档对比。
4. **Phase 4: 离线化与插件化开放**
   - 支持将 Draw.io 静态离线资源打包随星序私有分发；
   - 开放标准化 Manifest，允许用户或项目自定义外部 Viewer。
