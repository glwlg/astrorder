# Agent 原生浏览器能力与 Astrorder 集成实施计划

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** 在不重写、不覆盖 Hermes/Codex 原生浏览器工具的前提下，让 Astrorder 统一呈现浏览器会话、操作时间线、审批、截图/下载和人工接管，并保证 App Server 重启期间 daemon-owned Agent 与浏览器任务不中断。

**Architecture:** Agent 原生 browser/computer-use 工具继续负责动作决策和执行；Agent-specific Adapter 通过明确注册的工具描述符观察原生 tool call/result；Session Daemon 负责 Browser Scope、WAL、控制租约和可选的浏览器后端生命周期；App Server 只做持久化投影和认证 API；Browser Sidecar 只显示 exact `(agent_id, native_session_id, browser_scope_id)` 对应的状态。第一阶段为 observer-only，第二阶段才按显式能力启用 daemon-managed provider，绝不通过工具名正则、URL、标题或 workspace 猜测归属。

**Tech Stack:** Python 3.11、FastAPI、SQLAlchemy/SQLite、asyncio/websockets、Hermes plugin hooks、Codex native hooks/app-server events、React 19、TypeScript、Zustand、TanStack Query、Mantine、Vitest、Playwright。

---

## 1. 当前代码事实

1. `frontend/src/features/sidecar/sidecarStore.ts:332-377` 中的 `openBrowser()` 当前只是创建 `remote_url` 工件，并交给 `html-preview-viewer`。
2. `frontend/src/features/sidecar/viewers/html/HtmlViewer.tsx` 当前加载 iframe；代理模式调用 `/api/v1/browser/proxy`。它不是 Agent 正在操作的真实浏览器，也没有原生 tool-call identity。
3. `frontend/src/features/sidecar/registry.ts` 是工件查看器 registry；浏览器运行态不应伪装成文件工件，也不应通过扩展名/MIME 匹配。
4. `connectors/hermes/astrorder_hermes_plugin/__init__.py:56-67` 已注册 `pre_tool_call` 与 `post_tool_call`，可作为 Hermes 原生工具观察入口。
5. `connectors/codex/observer_hook.py` 已接收 `PreToolUse`、`PostToolUse`、`tool_call_id` 和 `tool_name`，但当前只保存有界元数据。
6. `backend/src/astrorder/daemon/session_daemon.py` 已有显式 runtime registry、per-session WAL 和 loopback-only IPC。
7. `backend/src/astrorder/daemon/bridge.py` 已支持 App-side native frame handler，并且只有 projector 成功后才推进 `(daemon_id, session_id)` checkpoint。
8. 当前工作树存在另一批未提交修改和新增文件。实施本计划前必须由所有者先提交、切分或放到独立 worktree；不得自动 stash、覆盖或夹带这些改动。

## 2. 核心决策

### 2.1 原生执行优先

- Hermes 的 `browser` toolset 继续处理 web-only 自动化。
- Hermes 的 `computer_use`/cua-driver 继续处理桌面级控制。
- Codex 使用其真实可用的 native browser/computer-use/MCP 能力；能力不存在时明确显示不可用，不能伪造。
- Astrorder plugin 默认只观察，不注册同名工具，不使用 `override=True`，不改变 Agent 的 tool schema 或执行结果。

### 2.2 两种 execution mode 必须分开

| 模式 | 执行者 | Astrorder 能力 | 第一阶段 |
|---|---|---|---|
| `native_observed` | Agent 自带 browser/computer-use | 时间线、状态、有限快照、下载引用 | 必做 |
| `daemon_managed` | 显式注册的 Browser Provider，由 daemon 持有 | 完整预览、人工接管、App 重启续接 | 后续 opt-in |

不得因为观察到了 `browser_*` 工具，就声称浏览器进程已经由 daemon 持有。Hermes 文档明确说明工具 transport 自己拥有私有 lifecycle；公开 session name 不是 runtime ownership 证明。

### 2.3 Web 与 desktop capability 分离

```text
browser.web        # 网页导航、DOM/AX tree、网页截图
computer.desktop   # 原生应用、系统文件选择器、桌面输入
```

不得在运行中静默从 `browser.web` 升级到 `computer.desktop`。桌面控制需要独立 capability 和审批。

### 2.4 Single-writer lease

浏览器 scope 同一时刻只有一个写入者：

```text
agent_control
  -> manual_requested
  -> manual_control
  -> resyncing
  -> agent_control
```

- 用户接管前先让 Agent 停在可确认边界。
- 用户释放后强制重新 capture；旧 element refs/tokens 全部失效。
- Agent 与用户不得并发 click/type。
- lease 必须带 opaque lease ID、版本号和过期时间，避免旧 UI 请求释放新 lease。

### 2.5 不重放有副作用的动作

`click`、`type`、`submit`、`upload`、`download`、支付、删除等动作如果 transport 断开，状态为 `unknown`。恢复时只查询当前页面状态，不自动重发动作。

### 2.6 Secret 与页面内容边界

- 密码、支付卡、CVC、验证码只走 Agent 自带 vault/direct-fill 通道。
- Browser event、WAL、SQLite、日志、截图元数据和前端 API 均不得包含 secret value。
- URL 的 userinfo、query、fragment 默认不持久化；UI 仅展示经过清理的 `display_url`。
- 网页内容是 untrusted data，不能直接批准命令、改变 lease、触发 shell 或选择 credential。
- existing browser profile 必须显式授权；不同 Agent/session 默认使用隔离 context。

## 3. 冻结的数据契约

### 3.1 Browser Scope

```json
{
  "id": "opaque-browser-scope-id",
  "agent_id": "exact-agent-id",
  "session_id": "exact-native-session-id",
  "extension_id": "hermes-native-browser",
  "execution_mode": "native_observed",
  "capability": "browser.web",
  "backend": "hermes-browser",
  "state": "agent_control",
  "control_owner": "agent",
  "lease_version": 3,
  "display_url": "https://example.com/path",
  "title": "Example",
  "latest_snapshot_id": null,
  "supports_preview": false,
  "supports_takeover": false,
  "created_at": "2026-09-12T10:00:00Z",
  "updated_at": "2026-09-12T10:00:01Z"
}
```

允许的 `state`：

```text
creating | ready | agent_control | manual_requested | manual_control |
resyncing | disconnected | error | closed
```

### 3.2 Browser Action

```json
{
  "id": "opaque-action-id",
  "scope_id": "opaque-browser-scope-id",
  "agent_id": "exact-agent-id",
  "session_id": "exact-native-session-id",
  "native_tool_call_id": "exact-tool-call-id",
  "descriptor_id": "hermes.browser.navigate",
  "kind": "navigate",
  "state": "completed",
  "mutating": true,
  "summary": "导航到 https://example.com/path",
  "error": null,
  "started_at": "2026-09-12T10:00:00Z",
  "updated_at": "2026-09-12T10:00:01Z"
}
```

允许的 `state`：

```text
started | completed | failed | unknown | cancelled
```

`summary` 必须由 descriptor 的 sanitizer 生成，不能直接 `str(params)` 或 `str(result)`。

### 3.3 Browser Snapshot

```json
{
  "id": "opaque-snapshot-id",
  "scope_id": "opaque-browser-scope-id",
  "media_type": "image/png",
  "width": 1440,
  "height": 900,
  "created_at": "2026-09-12T10:00:01Z",
  "available": true
}
```

- WAL 只保存 snapshot metadata ID，不保存大块 Base64。
- 二进制内容放入 daemon-controlled bounded snapshot cache，App Server 通过 authenticated daemon control 按 ID 读取，再交给 `AttachmentManager`。
- 默认每个 scope 只保留最近 20 张，达到容量后删除最老且未被引用的对象。

### 3.4 事件

```text
browser.scope.upsert
browser.scope.delete
browser.action.upsert
browser.snapshot.upsert
browser.control.requested
browser.control.changed
browser.resync_required
```

事件 ID 必须稳定且幂等；相同 Agent tool call 的 start/completion 使用同一 `BrowserAction.id`。

## 4. Extension Registry 设计

### 4.1 Backend descriptor registry

创建精确工具描述符，不使用正则：

```python
@dataclass(frozen=True)
class BrowserToolDescriptor:
    id: str
    exact_tool_names: frozenset[str]
    capability: str
    kind: str
    mutating: bool
    decode: Callable[[Mapping[str, Any]], BrowserObservation]


class BrowserCapabilityExtension(Protocol):
    id: str
    agent_kinds: frozenset[str]
    execution_mode: str
    descriptors: tuple[BrowserToolDescriptor, ...]

    def create_scope(self, identity: BrowserIdentity) -> BrowserScope: ...
```

`BrowserExtensionRegistry.register()` 要求：

- extension ID 唯一。
- exact tool name 在同一 Agent kind 内唯一。
- 冲突 fail closed。
- `resolve(agent_kind, exact_tool_name)` 仅做字典查找。
- 不读取 prompt、页面标题、URL 或 workspace 来匹配 extension。

### 4.2 Frontend workspace extension registry

Browser Sidecar 不是 `ArtifactViewer`。新增独立、自注册的 `WorkspaceExtensionRegistry`：

```ts
export interface WorkspaceExtension {
  id: string
  title: string
  kind: 'runtime-panel'
  capability: 'browser.web' | 'computer.desktop'
  component: ComponentType<WorkspaceExtensionContext>
  isAvailable(context: WorkspaceExtensionContext): boolean
  createTab(context: WorkspaceExtensionContext): SidecarTab
}
```

每个 extension 模块执行：

```ts
workspaceExtensionRegistry.register(browserWorkspaceExtension)
```

入口通过 Vite `import.meta.glob('./extensions/**/index.ts', { eager: true })` 做模块发现。禁止在 `SidecarHost.tsx` 增加 `if (viewerId === 'browser')`，禁止用 MIME、文件后缀或标题识别 browser panel。

## 5. 分阶段实施任务

### Task 0: 隔离当前脏工作树

**Objective:** 防止浏览器功能覆盖当前正在进行的会话模型、compaction、workspace preference 与 UI 修改。

**Files:** 不修改代码。

**Steps:**

1. 重新运行 `git status --short`。
2. 由当前改动所有者选择：先提交现有工作，或从其提交创建新 worktree。
3. 不得由实现者自动 stash、reset 或删除现有文件。
4. 在干净工作树中开始 Task 1。

**Verification:** `git status --short` 只显示本功能预期文件。

### Task 1: 冻结 browser 协议与安全不变量

**Objective:** 先让 contract test 失败，再添加 Browser Scope/Action/Event/API 契约。

**Files:**
- Modify: `docs/CONTRACT.md`
- Modify: `docs/design/dual-kernel-architecture.md`
- Create: `docs/design/agent-browser-integration.md`
- Create: `backend/tests/test_browser_contracts.py`

**Steps:**

1. 写失败测试，验证所有 ID 非空且有长度上限、state 为 allowlist、URL 清理移除 userinfo/query/fragment。
2. 运行：`cd backend && uv run pytest tests/test_browser_contracts.py -q`。
3. 预期：FAIL，browser contract 模块不存在。
4. 将本计划第 2～4 节固化到设计文档，并记录 observer-only 与 daemon-managed 的证明边界。
5. 建议提交：`docs: define agent browser integration contract`。

### Task 2: 实现 browser contracts 与 sanitizer

**Objective:** 提供无 runtime 副作用的类型校验和安全摘要生成。

**Files:**
- Create: `backend/src/astrorder/browser/__init__.py`
- Create: `backend/src/astrorder/browser/contracts.py`
- Create: `backend/src/astrorder/browser/sanitize.py`
- Modify: `backend/tests/test_browser_contracts.py`

**Required tests:**

- URL userinfo/query/fragment 被移除。
- 过长 URL、标题、error 被截断。
- secret-like assignment、Bearer token、常见 key token 被替换为 `[REDACTED]`。
- 不允许 `closed -> agent_control` 等非法状态转换。
- 原始 params/result 不进入 wire object。

**Verification:**

```text
cd backend && uv run pytest tests/test_browser_contracts.py -q
```

预期：PASS。

### Task 3: 建立 Agent browser extension registry

**Objective:** 使用 exact descriptor 完成 Agent tool 到 browser action 的映射。

**Files:**
- Create: `backend/src/astrorder/browser/registry.py`
- Create: `backend/tests/test_browser_registry.py`

**Required tests:**

1. Hermes browser descriptor 只匹配明确列出的 tool name。
2. `browser_navigate_extra`、标题中包含 browser、URL 相同均不得匹配。
3. 重复 extension ID 或 exact tool name 冲突时注册失败。
4. `browser.web` 与 `computer.desktop` 不互相回退。
5. extension 不可用时返回明确 capability 状态，而不是选择其他 Agent 的 extension。

**Verification:** `cd backend && uv run pytest tests/test_browser_registry.py -q`。

**Suggested commit:** `feat: add exact browser capability registry`。

### Task 4: 探测并锁定 Hermes hook payload

**Objective:** 只使用 Hermes 文档化 hook 字段，不从字符串结果猜 DOM、URL 或截图路径。

**Files:**
- Create: `backend/tests/test_hermes_browser_hook_contract.py`
- Modify: `connectors/hermes/astrorder_hermes_plugin/__init__.py`
- Create: `connectors/hermes/astrorder_hermes_plugin/browser_observer.py`

**Steps:**

1. 用 disposable Hermes session 和无登录测试页捕获 `pre_tool_call`/`post_tool_call` 的真实 keyword payload shape。
2. 测试 adapter 忽略未知附加字段，保持 Hermes additive hook compatibility。
3. 只解码 descriptor 明确声明的结构化字段。
4. 若 hook 未提供 params、URL 或截图 handle，则 capability 诚实标记 `supports_preview=false`，仍可显示工具开始/结束时间线。
5. 保留现有 message/task projection；browser events 是附加事件，不能替代原生 tool message。
6. 禁止注册或 override Hermes 内置 browser 工具。

**Verification:**

```text
cd backend && uv run pytest tests/test_hermes_browser_hook_contract.py tests/test_connector_transport.py -q
```

### Task 5: 增加 Codex observer-only adapter

**Objective:** 在 Codex 真正暴露 tool identity 时投影 browser timeline，并对缺失能力诚实降级。

**Files:**
- Modify: `connectors/codex/observer_hook.py`
- Create: `backend/tests/test_codex_browser_observer.py`
- Modify: `backend/src/astrorder/native_observers.py`

**Rules:**

- 只使用 exact `tool_call_id` 和 exact `tool_name`。
- 无 tool call ID 时不得把两次动作合并。
- native hook 没有结构化结果时只报告 lifecycle，不虚构 URL、页面标题或成功结果。
- spool 继续有界、无 message body、无 params、无 secrets。

**Verification:** `cd backend && uv run pytest tests/test_codex_browser_observer.py tests/test_observer_preview.py -q`。

### Task 6: 持久化 Browser Scope 与 Action

**Objective:** App Server 重启后能够重建时间线与 scope 状态，但不把数据库变成浏览器 runtime authority。

**Files:**
- Modify: `backend/src/astrorder/models.py`
- Modify: `backend/src/astrorder/store.py`
- Create: `backend/tests/test_browser_store.py`

**Models:**

- `BrowserScopeRow`：以 opaque `id` 为主键，同时索引 `(agent_id, session_id, updated_at)`。
- `BrowserActionRow`：唯一约束 `(agent_id, session_id, native_tool_call_id, descriptor_id)`。
- 不持久化 provider credential、cookie、localStorage、CDP endpoint、raw params/result。

**Required tests:**

- start/completion 幂等 upsert。
- 相同 session ID、不同 agent ID 不冲突。
- action 不能绑定到其他 session 的 scope。
- `unknown` 不会被迟到的 `started` 降级。
- closed scope 只能由同一 exact identity 更新为 closed metadata，不能重新归属。

**Verification:** `cd backend && uv run pytest tests/test_browser_store.py -q`。

### Task 7: 接入 daemon WAL 与 App-side projector

**Objective:** Browser events 先写 daemon WAL，projector 成功后才推进 checkpoint。

**Files:**
- Create: `backend/src/astrorder/daemon/browser_projection.py`
- Modify: `backend/src/astrorder/daemon/bridge.py`
- Modify: `backend/src/astrorder/main.py`
- Create: `backend/tests/test_daemon_browser_projection.py`

**Rules:**

- 通过 `register_native_frame_handler()` 显式注册 browser frame families。
- projector 校验 frame session ID、payload session ID、agent ID 和 scope ID。
- overflow 后不投影 retained/live tail；必须调用 native snapshot/resync。
- projector 异常时 checkpoint 保持不变。
- App Server 重启不要求 daemon 或 Agent 浏览器停止。

**Verification:**

```text
cd backend && uv run pytest tests/test_daemon_browser_projection.py tests/test_daemon_bridge.py -q
```

### Task 8: 增加 Browser API 子路由

**Objective:** 提供只读状态和显式 lease 操作，不向前端暴露通用 RPC。

**Files:**
- Create: `backend/src/astrorder/browser/api.py`
- Modify: `backend/src/astrorder/api.py`（仅 include browser router）
- Create: `backend/tests/test_browser_api.py`

**Endpoints:**

```text
GET  /api/v1/browser/capabilities?agent_id=...
GET  /api/v1/sessions/{session_id}/browser-scopes?agent_id=...
GET  /api/v1/browser/scopes/{scope_id}?agent_id=...&session_id=...
GET  /api/v1/browser/scopes/{scope_id}/actions?agent_id=...&session_id=...&before=...
POST /api/v1/browser/scopes/{scope_id}/control/request
POST /api/v1/browser/scopes/{scope_id}/control/release
POST /api/v1/browser/scopes/{scope_id}/snapshot
```

**API constraints:**

- 全部需要 browser authentication；mutation 继续校验 Origin。
- scope ID 单独不足以授权，必须同时校验 exact agent/session identity。
- observer-only scope 对 takeover/snapshot 返回 `409 capability unavailable`。
- 不提供 `POST /browser/rpc`、任意 tool name 或任意 CDP method 透传。
- lease conflict 返回 `409`，未知执行结果返回 `202` + `state=unknown`，不自动重试。

**Verification:** `cd backend && uv run pytest tests/test_browser_api.py -q`。

### Task 9: 实现 daemon Browser Broker 骨架（默认关闭）

**Objective:** 为后续 daemon-managed provider 建立 ownership/lease/snapshot cache，但不立刻迁移 Agent 原生浏览器。

**Files:**
- Create: `backend/src/astrorder/daemon/browser_broker.py`
- Create: `backend/src/astrorder/daemon/browser_runtime.py`
- Modify: `backend/src/astrorder/daemon/session_daemon.py`
- Modify: `backend/src/astrorder/config.py`
- Create: `backend/tests/test_daemon_browser_broker.py`
- Modify: `backend/tests/test_daemon_config.py`

**Config:**

```text
daemon_browser_enabled=false
browser_snapshot_limit_per_scope=20
browser_snapshot_max_size=<bounded positive integer>
browser_manual_lease_ttl=<bounded duration>
```

**Rules:**

- 默认关闭，只有 daemon secret 存在时才可注册。
- 通过明确 `register_runtime("browser", ...)` 或独立明确 registry 注册，不按 session 标题判断。
- snapshot cache 只接受受控 bytes、允许的 image MIME 和尺寸上限。
- shutdown 只关闭 broker 自己创建的 provider/browser，不关闭用户现有 Chrome。
- existing-profile attach 是单独 opt-in，不由 `full_access`/YOLO 隐式授权。

**Verification:** `cd backend && uv run pytest tests/test_daemon_browser_broker.py tests/test_daemon_config.py -q`。

### Task 10: 实现 single-writer lease

**Objective:** 人工接管和 Agent 操作不能并发。

**Files:**
- Modify: `backend/src/astrorder/daemon/browser_broker.py`
- Modify: `backend/tests/test_daemon_browser_broker.py`
- Create: `backend/tests/test_browser_control_lease.py`

**Required tests:**

1. Agent 控制时，第二个 writer 获取 lease 返回 conflict。
2. 相同 request ID 幂等返回同一 lease。
3. 旧 lease ID/version 不能释放新 lease。
4. TTL 到期进入 `resyncing`，不能直接恢复 agent control。
5. `release` 必须成功 capture 新快照后才返回 `agent_control`。
6. capture 失败保持 `resyncing` 并 fail closed。
7. App Server 断开不释放 daemon-owned lease。

**Verification:** `cd backend && uv run pytest tests/test_browser_control_lease.py -q`。

### Task 11: 增加 frontend workspace extension registry

**Objective:** Browser panel 由 runtime extension 自注册，不继续伪装成 HTML 工件。

**Files:**
- Create: `frontend/src/features/plugins/extensionRegistry.ts`
- Create: `frontend/src/features/plugins/extensionDiscovery.ts`
- Create: `frontend/src/features/plugins/extensionRegistry.test.ts`
- Modify: `frontend/src/features/sidecar/types.ts`
- Modify: `frontend/src/features/sidecar/sidecarStore.ts`
- Modify: `frontend/src/features/sidecar/SidecarHost.tsx`
- Modify: `frontend/src/features/plugins/PluginsPage.tsx`

**Rules:**

- 新增 `SidecarTab.type='extension'` 和 `extensionId`；payload 只含 exact identity。
- `SidecarHost` 通过 registry 查找 component，不出现 browser-specific `if/else`。
- Browser extension enablement 与 ArtifactViewer enablement 都显示在 Plugins 页面，但 registry 各自保持类型边界。
- extension discovery 使用 eager module discovery；重复 ID 失败并在测试暴露。
- session `sessionMemories` 继续保存 Browser tab 的打开/激活状态。

**Verification:**

```text
cd frontend && npm test -- src/features/plugins/extensionRegistry.test.ts src/features/sidecar/sidecar.test.ts
```

### Task 12: 实现 Browser Sidecar

**Objective:** 显示真实 Agent browser scope，而不是 iframe 中的另一个网页。

**Files:**
- Create: `frontend/src/features/browser/types.ts`
- Create: `frontend/src/features/browser/api.ts`
- Create: `frontend/src/features/browser/BrowserViewer.tsx`
- Create: `frontend/src/features/browser/BrowserToolbar.tsx`
- Create: `frontend/src/features/browser/BrowserTimeline.tsx`
- Create: `frontend/src/features/browser/browser.css`
- Create: `frontend/src/features/browser/index.ts`
- Create: `frontend/src/features/browser/BrowserViewer.test.tsx`
- Modify: `frontend/src/api/client.ts`
- Modify: `frontend/src/domain/types.ts`
- Modify: `frontend/src/state/store.ts`
- Modify: `frontend/src/features/sidecar/SidecarWelcomeMenu.tsx`

**UI behavior:**

- 无 scope：显示“等待 Agent 启动浏览器”，并列出当前 Agent 的 capability；不默认打开 Bing。
- observer-only：显示时间线和 `supports_preview=false`，不展示不可用的接管按钮。
- managed + preview：显示 latest snapshot、sanitized URL、title、状态和 writer。
- `unknown` 动作用醒目但非成功的状态显示，提供“重新获取页面状态”，不提供“重试动作”。
- Browser tab 默认不自动弹开；有新 scope/action 时只更新 badge，除非用户开启自动打开配置。
- 错误可 toast；正常 snapshot/save 不 toast。

**Verification:**

```text
cd frontend && npm test -- src/features/browser/BrowserViewer.test.tsx src/features/sidecar/sidecar.test.ts
```

### Task 13: 将现有 iframe 浏览器降级为“网页预览”

**Objective:** 消除“HTML iframe 等于 Agent 浏览器”的误导和控制冲突。

**Files:**
- Modify: `frontend/src/features/sidecar/sidecarStore.ts`
- Modify: `frontend/src/features/sidecar/viewers/html/HtmlViewer.tsx`
- Modify: `frontend/src/features/sidecar/viewers/html/index.ts`
- Modify: `backend/src/astrorder/api.py`
- Create: `backend/tests/test_html_preview_proxy_policy.py`

**Rules:**

- `openBrowser()` 改为打开 Browser runtime extension；不再创建 `remote_url` + `html-preview-viewer`。
- HTML viewer 明确命名“网页预览”，仅用于用户显式打开 URL/HTML artifact。
- `/api/v1/browser/proxy` 不得作为 Agent automation backend；迁移为清晰的 preview route 或标记 deprecated。
- 审计 SSRF、redirect、private-network 和响应大小策略；不要因为 iframe 需要就无边界剥离所有安全头。
- 若本地开发预览需要 loopback，必须由 workspace/port allowlist 明确授权，不能开放任意内网请求。

**Verification:** Browser extension tests 中断言不会请求 HTML proxy；preview policy tests 覆盖 redirect 与内网边界。

### Task 14: Snapshot、下载与附件集成

**Objective:** 安全显示截图、把下载物变成受控 attachment，不读取任意本地路径。

**Files:**
- Modify: `backend/src/astrorder/daemon/browser_broker.py`
- Modify: `backend/src/astrorder/attachments.py`
- Modify: `backend/src/astrorder/browser/api.py`
- Create: `backend/tests/test_browser_snapshots.py`
- Create: `backend/tests/test_browser_downloads.py`

**Rules:**

- 只接受 daemon 返回的 bytes + filename + MIME；App 再做大小/MIME 校验。
- native hook 返回的任意路径字符串不能直接交给 `AttachmentManager` 读取。
- snapshot endpoint 使用 authenticated same-origin attachment URL。
- 下载文件名清理路径分隔符；禁止 traversal。
- 数据库和事件只保存 attachment ID，不保存原始绝对路径。

**Verification:** 测试 path-free payload、恶意文件名、超大截图、错误 MIME、跨 scope snapshot ID。

### Task 15: 审批与 Vault 边界

**Objective:** Astrorder 只承载审批 UI，不接管 Agent host 的审批政策或 secret material。

**Files:**
- Modify: `connectors/hermes/astrorder_hermes_plugin/__init__.py`
- Create: `backend/tests/test_browser_approval_boundary.py`
- Modify: `frontend/src/features/browser/BrowserViewer.tsx`

**Required assertions:**

- Astrorder approval response 必须绑定 Agent 提供的 request ID/digest。
- stale、timeout、App unavailable 默认 deny。
- plugin 不注册 auto-allow policy。
- vault fill event 只包含 origin、filled field count 和状态。
- password、card、CVC、TOTP 不出现在 connector event、WAL、Store、API 或日志。
- existing-profile grant 不由普通 action approval 替代。

### Task 16: App restart 与模糊结果集成测试

**Objective:** 证明大小内核语义对 browser 同样成立。

**Files:**
- Create: `backend/tests/test_daemon_browser_restart.py`
- Create: `frontend/e2e/browser-sidecar.spec.ts`
- Modify: `backend/tests/mobile_fixture_server.py`

**Scenario:**

1. 启动 disposable daemon、App Server 和 fake native browser provider。
2. 创建 exact Agent/session/browser scope。
3. 发出 navigate start/completion 和 snapshot metadata。
4. 停止 App Server，不停止 daemon/provider。
5. App 离线期间继续发出 action completion。
6. 重启 App Server。
7. 验证 WAL replay、scope/action projection、checkpoint 和 Sidecar。
8. 模拟 mutating action 返回前断线，验证 action=`unknown` 且 provider call count 仍为 1。
9. 模拟 WAL overflow，验证不投影 live tail，直到 full snapshot/resync 完成。

**Verification:**

```text
cd backend && uv run pytest tests/test_daemon_browser_restart.py -q
cd frontend && npx playwright test e2e/browser-sidecar.spec.ts
```

### Task 17: 真实 Hermes browser 验收

**Objective:** 用无凭据测试网站验证 Hermes 原生 browser tool、connector、daemon WAL 与 Browser Sidecar 的完整链路。

**Files:**
- Create: `scripts/verify_native_hermes_browser.py`
- Create: `docs/reports/agent-browser-integration.md`

**Scenario:**

- 使用 isolated port、isolated Hermes profile、isolated browser profile。
- 测试页包含导航、表单、下载和一次可确认的 DOM 变化，不包含真实账号。
- 由 Hermes 原生 browser tool 执行；Astrorder 不调用另一个 Playwright 页面冒充 Agent。
- 验证 exact `tool_call_id` 关联、action started/completed、Sidecar timeline 和下载 attachment。
- App-only restart 期间让 Agent 浏览器继续执行，再验证 replay。
- 清理只删除测试创建的 scope/profile/attachments/process。

**Acceptance evidence:** 记录 opaque IDs、动作数、provider process identity、App/daemon PID continuity 和 pass/fail；不记录页面表单内容、cookie 或 secret。

### Task 18: Codex 与 computer-use capability 验收

**Objective:** 分别证明能力存在，或诚实记录不可用边界。

**Files:**
- Create: `scripts/verify_native_codex_browser.py`
- Create: `scripts/verify_hermes_computer_use.py`
- Modify: `docs/reports/agent-browser-integration.md`

**Rules:**

- Codex 没有可观察的原生结构化浏览器事件时，报告 observer metadata-only，不通过推测补齐。
- Hermes `computer_use` 单独以 `computer.desktop` 展示。
- Windows elevated window、Session 0、existing profile 等限制原样显示，不称为 Astrorder 成功。
- web-only 与 desktop runs 使用不同 scope 和 capability ID。

### Task 19: 全量质量门与生产 opt-in

**Objective:** 在不影响现有四 runtime 的前提下完成 rollout。

**Files:**
- Modify: `README.md`
- Modify: `docs/reports/dual-kernel-phase2.md`
- Modify: production public config only after backup and explicit deployment approval

**Quality gates:**

```text
cd backend && uv run pytest -q
cd backend && uv run ruff check src tests
cd backend && uv run python -m compileall -q src/astrorder
cd frontend && npx tsc -b --pretty false
cd frontend && npm test
cd frontend && npm run build
git diff --check
```

**Production rollout:**

1. 备份 SQLite、public config 和 encrypted credential artifact；不得提交备份。
2. 首先只部署 observer-only 和 Sidecar，保持 `daemon_browser_enabled=false`。
3. 验证 Hermes/Codex/SSH/PT​​Y 原有 production smoke tests。
4. 再明确启用一个 disposable daemon-managed browser provider。
5. App-only restart 验证 daemon/provider PID 不变。
6. 检查 active commands/scopes 为预期值。
7. 扩大 rollout 前保留一键回退到 observer-only 的 public config。

**Suggested final commits:**

```text
feat: observe native agent browser sessions
feat: add daemon browser broker and control leases
feat: add browser sidecar extension
security: isolate browser previews and secret transport
 test: verify browser replay and native integrations
 docs: document agent browser production rollout
```

## 6. 验收标准

- [ ] Agent 仍调用原生 browser/computer-use 工具，没有同名 override。
- [ ] Browser extension 通过 registry 自注册，不使用工具名正则或 agent-kind if/else 链。
- [ ] 每个 scope 绑定 exact `(agent_id, native_session_id, browser_scope_id)`。
- [ ] App Server 重启不会停止 daemon-owned Agent/browser task。
- [ ] 重连只 replay 事件并获取当前 snapshot，不 replay mutating actions。
- [ ] observer-only 与 daemon-managed 在 API/UI 中明确区分。
- [ ] Browser Sidecar 不再用 iframe/Bing 冒充 Agent 浏览器。
- [ ] manual takeover 使用 single-writer lease；释放后强制 resnapshot。
- [ ] 不同 Agent/session 默认不共享 profile/cookie/storage。
- [ ] secrets 不进入模型上下文之外的 Astrorder event/WAL/DB/API/log。
- [ ] snapshot/download 通过受控 bytes 和 attachment ID 传输，不读取 hook 返回的任意路径。
- [ ] WAL overflow 时隔离 live tail，直到 full native resync。
- [ ] Hermes、Codex、web browser 与 desktop computer-use 的能力证明分别记录。
- [ ] Backend、frontend、E2E、真实 native integration 与 production smoke 全部通过。

## 7. 风险与取舍

1. **Hermes hook payload 可能不含完整参数或 screenshot handle。** 第一阶段必须诚实降级为 timeline-only，不能解析字符串猜值。
2. **Codex 的 native hooks 可能只提供 lifecycle metadata。** 无法证明页面状态时 `supports_preview=false`。
3. **Agent 自带 browser transport 生命周期不等于 daemon ownership。** observer-only 只能证明 App restart 后 Agent runtime 未停；要跨 Agent reset 保留浏览器，必须使用真正 daemon-managed provider。
4. **人工接管会让 Agent 的 element refs 失效。** release 后必须 capture；否则继续操作风险不可接受。
5. **截图和视频会快速放大 WAL/上下文。** 二进制不入 WAL，历史有界，UI 按需读取。
6. **URL 本身可能泄露凭据。** persistence 默认去掉 userinfo/query/fragment。
7. **现有 HTML proxy 有独立安全面。** Browser integration 不复用它；preview policy 应单独审计并收紧。
8. **当前工作树已有并行修改。** 实施前必须隔离，所有文件冲突按真实 diff 调和，不能覆盖其他 Agent 的改动。

## 8. 开放问题（进入 daemon-managed 阶段前必须决定）

1. 第一款 managed provider 是复用 Hermes Browser Use local driver、连接现有 CDP，还是独立 MCP provider？优先复用 Agent 已安装且可验证的 active project，不新增重复浏览器栈。
2. Browser Sidecar 的实时预览采用定时 snapshot，还是 provider 自带 stream？MVP 建议按需 + 低频 snapshot，避免提前引入视频协议。
3. 人工接管 MVP 是否只支持打开 provider 原生 live view，还是在 Sidecar 中发送坐标/DOM actions？建议先 live view + lease，随后再做 in-Sidecar input。
4. HTML preview proxy 是否仍需支持任意公网 URL？如果保留，必须单独定义 SSRF/redirect/response-size policy。
5. production existing-profile attachment 是否需要支持？默认答案应为否，直到有独立 grant UI、origin audit 和撤销路径。

## 9. 参考资料

- Hermes Browser Automation：`https://hermes-agent.nousresearch.com/docs/user-guide/features/browser`
- Hermes Computer Use：`https://hermes-agent.nousresearch.com/docs/user-guide/features/computer-use`
- Hermes Credential Vault：`https://hermes-agent.nousresearch.com/docs/user-guide/features/credential-vault`
- Hermes Tools Runtime：`https://hermes-agent.nousresearch.com/docs/developer-guide/tools-runtime`
- Hermes Plugins/Hooks：`https://hermes-agent.nousresearch.com/docs/user-guide/features/plugins`
- Astrorder dual-kernel design：`docs/design/dual-kernel-architecture.md`
- Astrorder protocol contract：`docs/CONTRACT.md`
