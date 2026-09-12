# Astrorder 大小内核分离架构设计规范

## 1. 背景与核心问题

### 1.1 现状痛点
在 Astrorder 中，用户高频使用 AI Agent（Codex、Hermes）执行代码重构、功能迭代与服务自维护。
目前星序单进程架构下：
- **自迭代死锁**：当 Agent 会话修改星序自身代码并执行重启服务（`restart_service.py`）时，系统通过 PID 杀死整个后端进程树。
- **运行态丢失**：正在执行命令、写文件或跑测试的 Codex App-Server、Hermes CLI 子进程及 PTY/SSH 连接随之被强杀，导致多会话中断、输出丢失、状态残缺。
- **状态同步脆弱**：大内核需通过文件轮询、DB 嗅探或启发式平滑窗口判定运行状态，延迟高且易抖动。

### 1.2 架构目标
- **大小内核分离**：
  - **小内核（Session Daemon）**：极简常驻守护核，负责会话 Runtime 宿主、进程树守护、命令中断与输出增量缓冲（WAL）。极少变更，原则上不重启。
  - **大内核（App Server）**：业务功能核，负责 Web/REST API、前端资源、Sidecar 工作台、历史持久化与状态投影。支持随时高频热重载与重启。
- **无感断线重连（Detached Reconnect & Replay）**：
  - 大内核重启期间，小内核接管会话流并持续存入环形缓冲区（Ring Buffer）。
  - 大内核启动后通过增量序列号（`seq_id`）拉取缺失帧并补推给前端，前端及底层 Agent 无需中断轮次。

---

## 2. 拓扑与职责划分

```
┌─────────────────────────────────────────────────────────────┐
│                      客户端 UI (Browser / Mobile / Desktop)  │
└──────────────────────────────┬──────────────────────────────┘
                               │ HTTP / WebSocket (Port: 30001)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ 大内核：Astrorder App Server (可高频迭代、随时重启)          │
│ · 宿主：FastAPI / Uvicorn (Port: 30001)                      │
│ · 职责：                                                     │
│   1. 前端静态资产与 UI 路由 (Vite dist / index.html)        │
│   2. 业务 REST API (项目分组、历史分页、附件管理、文件检索)    │
│   3. Sidecar 扩展能力 (Monaco、Draw.io、Git Diff、文件树)     │
│   4. 数据落地 (SQLite: astrorder.sqlite3)                   │
│   5. 作为 Client 连接小内核 IPC，转推事件到前端 WebSocket    │
└──────────────────────────────┬──────────────────────────────┘
                               │ 本地轻量 IPC (UDS 或 Loopback TCP: 30009)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ 小内核：Astrorder Session Daemon (驻留守护核，极简稳定)      │
│ · 宿主：纯 Python asyncio 守护进程 (Port: 30009)             │
│ · 职责：                                                     │
│   1. 真实 Runtime 宿主 (Codex app-server、Hermes、SSH、PTY)  │
│   2. 会话执行状态机 (running / waiting_approval / idle / error)│
│   3. 内存 WAL 环形缓冲池 (Ring Buffer, 保证大内核断线无损)    │
│   4. 审批转发与中断执行 (turn/interrupt, approval response)   │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. 进程与生命周期模型

### 3.1 进程所有权转移
- **旧模式**：`App Server (30001)` -> 直接 `Popen` 派生 `codex app-server` / `hermes-agent` / `ssh`。
- **新模式**：`Session Daemon (30009)` 成为所有 Agent 运行时、子进程、SSH 隧道与 PTY 的直接父进程。
- **重启范围**：`restart_service.py` 严格受限于仅查找并终止端口 `30001` 的 App Server 进程；禁止向 Daemon（PID 或 30009 端口）发送退出信号。

### 3.2 离线托管模式（Detached Mode）
1. 当大内核停止，小内核检测到 IPC 连接断开（Socket EOF / Reset）：
   - 小内核不终止任何子进程。
   - 子进程继续正常输出 stdout/stderr/JSON-RPC。
   - 小内核持续把输出封装为带单调递增 `seq_id` 的事件帧，写入每个会话独立的内存 Ring Buffer。
   - 若遇到审批请求（如 Codex command approval），将其保存在待决池（Pending Approvals）中，等待大内核上线或超时策略。

---

## 4. IPC 通信协议规范 (WebSocket/JSON-RPC 30009)

大内核与小内核之间采用本地 WebSocket 全双工流式传输，通信编码一律为 UTF-8 JSON。

### 4.1 消息基本信封 (Frame Envelope)
小内核推向大内核的所有事件均包装为标准 Envelope：
```json
{
  "session_id": "01a08904-37d0-70e3-ade5-ec1bb61cd045",
  "seq_id": 1042,
  "timestamp": 1726051200.123,
  "event": "token_chunk",
  "payload": {
    "delta": "const app = express();"
  }
}
```

### 4.2 控制与同步指令（大内核 -> 小内核）

#### 1. `daemon.handshake` & `session.sync`（连接与断线回放）
大内核启动后发起握手，并上报当前持久化层各会话已确认的最大 `seq_id`：
```json
{
  "action": "session.sync",
  "request_id": "req-sync-01",
  "sessions": {
    "01a08904-37d0-70e3-ade5-ec1bb61cd045": 1020,
    "01a08904-44aa-7111-bbee-cc2233445566": 55
  }
}
```
**小内核响应**：
小内核返回该会话在 `seq_id > 1020` 之后的所有暂存事件数组，随后恢复实时推流。

#### 2. `session.spawn`（启动/托管运行时）
```json
{
  "action": "session.spawn",
  "session_id": "01a08904-37d0-70e3-ade5-ec1bb61cd045",
  "agent_type": "codex",
  "connection_id": "local",
  "cwd": "/home/luwei/workspace/OpsCore",
  "params": {
    "model": "gpt-5-codex",
    "approval_policy": "on-request",
    "sandbox": "workspace-write"
  }
}
```

#### 3. `session.send`（投递轮次）
```json
{
  "action": "session.send",
  "session_id": "01a08904-37d0-70e3-ade5-ec1bb61cd045",
  "turn_id": "turn-9988",
  "prompt": "帮我更新一下前端打包配置",
  "attachments": []
}
```

#### 4. `session.interrupt`（停止轮次）
```json
{
  "action": "session.interrupt",
  "session_id": "01a08904-37d0-70e3-ade5-ec1bb61cd045",
  "turn_id": "turn-9988"
}
```

#### 5. `session.approve`（决策审批）
```json
{
  "action": "session.approve",
  "session_id": "01a08904-37d0-70e3-ade5-ec1bb61cd045",
  "request_id": "call-approval-771",
  "decision": "accept"
}
```

#### 6. `daemon.status`（权威状态嗅探）
```json
{
  "action": "daemon.status"
}
```
**小内核响应**：
返回当前所有由小内核接管的活跃会话以及其权威运行状态（`running` / `waiting_approval` / `idle` / `error`），消灭大内核的盲目轮询与延迟。

---

## 5. 缓冲与回放协议 (Memory WAL Ring-Buffer)

### 5.1 环形队列配置
- 每个活跃会话在小内核中分配固定上限的环形队列（默认容量：`CAPACITY = 2000` 帧）。
- 帧以单调递增整数 `seq_id` 标定（从 1 开始）。
- 超出容量时丢弃最老的非关键帧（仅保留关键快照点或截断）。

### 5.2 重连一致性保证
1. 大内核数据库维护每个会话的 `last_synced_seq_id`。
2. 大内核重启完成后与小内核握手：
   - 若 `last_synced_seq_id >= min_seq_id`：小内核执行完整增量补发（Catch-up），大内核转推给前端。
   - 若 `last_synced_seq_id < min_seq_id`（极罕见长时间下线溢出）：小内核标记 `overflow=true`，大内核触发全量会话历史快照刷新。

---

## 6. 实施路线图

### Phase 1: 小内核守护进程原型（Daemon Prototype）
- 创建独立可执行脚本 `backend/src/astrorder/daemon/session_daemon.py`。
- 实现基于标准库 `asyncio` 的 WebSocket 服务，支持多会话 Ring Buffer 与序列号递增。
- 实现 `session.sync` 与回放协议单元测试。

### Phase 2A: App Server Bridge 与控制协议（已实现）
- 大内核作为 daemon 的订阅端：按 daemon epoch + session checkpoint 持久化和回放 canonical connector event。
- daemon 在 `session.sync` 完成后才开始 live 推流；WAL overflow 明确隔离，不能将 retained tail 当完整历史。
- `session.spawn` / `session.send` / `session.interrupt` / `session.approve` 经显式 runtime registry 路由；大内核可用独立 IPC socket 发送 control request。
- 该阶段默认关闭，不启动 daemon，也不迁移或接管当前原生 runtime。

### Phase 2B-1: Codex transport owner（已生产启用）
- `CodexDaemonRuntime` 将每个显式 daemon-owned native thread 的 Codex app-server 作为 daemon 子进程持有，并通过 `initialize` / `thread/resume` 验证后接收 turn control。
- Codex raw notification 写入 WAL；大内核通过 handler registry 按 exact `agent_id` + native thread ID 投影，handler 未就绪时不得确认 checkpoint。
- 启用可执行 runtime 需要独立 daemon IPC secret；socket 先 `daemon.handshake`，runtime registry 也拒绝未认证配置。
- 提供显式 CLI / factory opt-in，未改动当前 local/remote Codex UI connection 的默认 transport。

### Phase 2B-2: 生产接入与其余 runtime（已生产启用）
- `CodexConnection`、local Hermes、SSH Hermes 与 browser PTY 均有显式 daemon-owned control/projection adapter；旧 App-owned runtime 不做隐式迁移。
- Codex attachments、model/effort、approval、interrupt、disconnect/reattach 保持原生 identity；Hermes/SSH command completion、connector event、native history page 经 daemon WAL/bridge 投影。
- Hermes plugin 与 SSH reverse tunnel 的 connector endpoint 指向 daemon loopback listener；connector secret 与 daemon IPC secret 分离且都只从环境读取。
- 产品默认ownership开关保持关闭；当前production已显式启用。Codex、local Hermes、SSH Hermes和PTY均完成真实生产生命周期、附件、控制及restart验收。

### Phase 3: 服务管理与工具链解绑（Lifecycle Scripts）
- 重构 `scripts/restart_service.py`，加入仅对大内核端口（30001）的精准过滤。
- 增加小内核独立托管命令（如 `scripts/start_daemon.py`、`scripts/status_daemon.py`）。
- 已实现 authenticated graceful daemon shutdown；daemon secret 不进入命令行参数，production restart 不触碰 daemon listener。

### Phase 4: 闭环验证（Dogfooding Acceptance）
- 已在隔离端口用真实 Chrome 与真实 Codex app-server 创建 daemon-owned session，从 UI 下发临时文件修改任务；任务运行中停止 App Server，daemon/Codex 继续执行并写入 WAL。
- 重启同一 App Server 后无需显式 connect API 即恢复 Codex projection，同一 session 显示 user/tool/assistant transcript 并回到 idle；生产服务与现有用户 runtime 未参与该验收。

### Phase 5: 大小模型与成本路由（已生产启用）
- App在native command dispatch前按结构信号决策：普通短输入使用small；附件、长输入或上一命令failed/unknown使下一命令使用large；后续普通输入自动降回small。
- 路由不检查会话标题或prompt语义，不重试已发送命令。新session readiness race只允许对同一target执行一次幂等model/effort control+readback重试。
- 每Agent按UTC日持久化cost units ledger；small/large默认成本分别为1/5。预算不足以承担large时强制small，不虚构供应商美元价格。
- model、provider与reasoning必须由native readback确认后才记录ledger并发送command；失败command持久化为failed且native send调用为零。
