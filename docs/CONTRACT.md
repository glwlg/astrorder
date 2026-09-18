# Protocol v1 — 核心通信协议与数据契约规范

所有 JSON 通信字段命名遵循 `snake_case`。接口基址为 `/api/v1`。时间戳统一遵循 ISO-8601 UTC（格式：`YYYY-MM-DDTHH:mm:ss.sssZ`）。
消息、会话与指令标识符均采用由 `agent_id + session_id` 作用域隔离的不透明字符串，绝不采用易冲突的文本派生 ID。连接器源事件通过 `(agent_id, event.id)` 去重，服务端游标（`cursor`）保持全局严格单调递增。

---

## 1. 原生会话权威性原则 (Native Session Authority)

星序是 Codex、Hermes、Grok 与 SSH 远端服务器的**控制面与观察面**，坚决不构建平行割裂的第二套会话对账系统：
- `Session.id` 始终严格对齐底层原生的持久会话或线程 ID（如 Codex Thread ID、Hermes Session Key）。
- 智能体命名空间（`agent_id`）用于隔离跨智能体 ID 碰撞，绝不将私有内部信息拼接至会话 ID 中。
- 本地数据库（SQLite）仅作为高速缓存与离线工件投影，原生状态才是不可动摇的权威真值。

---

## 2. 核心共享类型 (Core Shared Types)

### 2.1 智能体定义 (Agent)
```typescript
type AgentKind = 'hermes' | 'codex' | 'grok' | 'ssh'
type AgentStatus = 'disconnected' | 'connecting' | 'connected' | 'ready' | 'error'
type ControlState = 'ready' | 'owned' | 'waiting_approval' | 'unknown'

interface Agent {
  id: string
  kind: AgentKind
  name: string
  status: AgentStatus
  capabilities: string[]
  limitation: string | null
  source_id?: string | null
  connection_id?: string | null
  profile_name?: string | null
  runtime_id?: string | null
  control_state: ControlState
  daemon_mode?: boolean
  updated_at: string
}
```

### 2.2 会话定义 (Session)
```typescript
type SessionStatus = 'idle' | 'running' | 'waiting_approval' | 'error'
type ApprovalMode = 'ask' | 'auto' | 'full'
type ReasoningEffort = 'minimal' | 'low' | 'medium' | 'high' | 'max'

interface Session {
  id: string
  agent_id: string
  title: string
  workspace?: string | null
  status: SessionStatus
  project_id?: string | null
  project_name?: string | null
  selected_model?: string | null
  selected_reasoning_effort?: ReasoningEffort | null
  selected_approval_mode?: ApprovalMode | null
  history_state: 'local' | 'pending' | 'ready'
  updated_at: string
}
```

### 2.3 消息定义 (Message)
```typescript
type MessageRole = 'user' | 'assistant' | 'tool' | 'system'
type MessageKind = 'text' | 'thinking' | 'tool_call' | 'tool_result' | 'file'

interface Message {
  id: string
  session_id: string
  agent_id: string
  role: MessageRole
  kind: MessageKind
  text: string
  attachments?: Attachment[]
  tool?: {
    name: string
    arguments?: Record<string, any>
    result?: any
  } | null
  command_id?: string | null
  created_at: string
}
```

---

## 3. 关键 REST 与 WebSocket 接口规范

### 3.1 鉴权与初始化
- `POST /api/v1/auth/session`：通过有效访问凭证换取 HttpOnly Session Cookie。
- `GET /api/v1/auth/session`：查询当前浏览器连接的会话鉴权有效性。
- `GET /api/v1/bootstrap`：拉取星序全局启动快照（智能体列表、活动项目、会话列表、审批待决队列与最新事件游标）。

### 3.2 会话与指令控制
- `POST /api/v1/sessions/{id}/commands`：向底层 Agent 运行时提交用户指令（纯文本、附件或工具执行），返回受理凭证（`state: 'accepted'`）。
- `POST /api/v1/sessions/{id}/stop`：向底层 Agent 运行时发送中断请求（映射为 Codex `turn/interrupt` 或 Hermes 停止信号）。
- `PATCH /api/v1/sessions/{id}/model`：动态调整当前会话的模型绑定及思考深度（`reasoning_effort`）。
- `PATCH /api/v1/sessions/{id}/approval-mode`：配置审批拦截模式（`ask` 询问 / `auto` 智能 / `full` 完全信任）。

### 3.3 工件与工作区边界 API
- `GET /api/v1/sessions/{id}/artifacts/content?path=...`：读取受控工作区内的文件内容（带 ETag / SHA-256 校验）。
- `PUT /api/v1/sessions/{id}/artifacts/content?path=...`：受控写回工作区文件，强校验会话工作区物理路径，防范目录穿越。

### 3.4 交互终端与 PTY Relay
- `POST /api/v1/terminals`：创建当前会话作用域的交互式终端实例。
- `GET /api/v1/terminals/{id}`：获取终端运行状态与输出缓冲。
- `DELETE /api/v1/terminals/{id}`：优雅释放与终止指定终端子进程。

### 3.5 多智能体群组（Bot Groups）API
- `GET /api/v1/bot-groups`：查询当前用户定义的多智能体协作群列表。
- `POST /api/v1/bot-groups`：创建包含指定成员（`machine_id`、`agent_id`、`alias`）的协作群。
- `GET /api/v1/bot-groups/{id}/messages`：拉取群内交互消息与多方流式讨论记录。
- `POST /api/v1/bot-groups/{id}/messages`：向群组提交指令，自动解析 `@成员` 并驱动多智能体协同响应。
- `POST /api/v1/bot-groups/{id}/stop`：紧急打断并终止当前群内正在进行的智能体接力执行。

### 3.6 会话分叉与 Git Worktree 隔离
- `POST /api/v1/sessions/{id}/fork`：根据指定会话一键派生新会话。若设置 `worktree: true`，系统将自动基于 Git Worktree 在宿主机上检出独立工作区路径，实现多分支安全并行探索。

### 3.7 双工事件流 (Event Stream)
- `GET /ws/v1/events?after={cursor}`：浏览器全双工事件通道。支持断线自动回补重传缺失游标后的增量事件帧（`message.upsert`、`session.upsert`、`task.upsert`、`command.upsert`）。
