# 星序 Stitch 设计落地 — Luna

## 基线（2026-09-07）

- 工作根目录：`P:/workspace/glwlg/ai/astrorder`，工具 cwd 已确认对应 `/p/workspace/glwlg/ai/astrorder`。
- 分支：`master`；仓库存在既有未提交修改，本轮不 reset、回滚、commit 或 push。
- 模型/上下文：按用户要求保持默认 Luna/ocx 与 300000；本轮未修改全局配置。
- 权威材料已读取：
  - `docs/design/overlook-parity-and-interactions.md`
  - `docs/design/project-first-v3-review.md`
  - `.stitch/revision-v3.json`
  - `.stitch/revision-v2.json`
  - `.stitch/revision-v2-readback.json`
- V3 本地设计素材已存在并已查看截图：工作台、连接管理、连接详情、移动项目/会话导航、监控室。实现以项目→会话两级侧栏为准；所有来源项目混排；远程短标识只出现在项目行；会话不挂连接名。
- V3 生成 HTML 的已知缺口：未提供 `prefers-reduced-motion`；应用已增加全局 reduced-motion 降级，仍需按功能清单区分已实现与未迁移项。
- 本轮收尾前的实际构建根因已定位：`frontend/src/components/sessionRailModel.ts:71` 仍以旧的两参数形式调用四参数 `sourceKey`。已先增加失败回归，再修复为显式传入 `source_id`、`connection_id`、`agent_id` 和 agent map。

## 里程碑

- [x] A：前后端基线、真实类型/构建根因、SSH/项目完整性状态已记录。
- [x] B：V3 项目优先两级侧栏、跨来源稳定项目身份、筛选/计数、连接管理入口已落地并验证。
- [x] C：运行辅助条、队列入口、语音入口、monitor 与连接详情入口已落地；真实能力边界仍按下表区分。
- [x] D：项目导航和聊天移动布局、中文可见文案、safe-area、键盘热区已落地；移动 SSH 分步表单仍不是完整迁移。
- [x] E：状态语义、reduced-motion、键盘 focus-visible 与 aria-live 相关实现已保留并通过现有自动化检查。
- [x] 最终接线：项目目录已从 bootstrap/state 经 AppShellLayout、Sidebar 传到 SessionRail；后端/前端回归和隔离浏览器验证已完成。

## 本轮追加范围（2026-09-07，继续实现）

本轮不重新生成设计，按垂直切片继续补齐此前明确的缺口，并在每条切片完成后补充 RED→GREEN、fixture/live 边界和未支持能力：

- [x] 独立任务详情：从运行条的后台任务/Todo/子代理入口打开桌面面板或移动 sheet；任务来源会话、状态、进度、命令、日志、复制、跳最新、返回；停止仅针对明确任务且经过能力校验和确认。
- [x] connector 公开事件的任务/审批/通知持久化和 API/订阅路径；没有上游能力时显示未报告，不生成演示数据；本轮另有真实 hook→API/store 的 `task.upsert` evidence。
- [x] 稳定连接运行历史：部署、启动、握手、断开、失败时间线，阶段诊断、分页、空态、脱敏；不把进程实例当作额外连接。
- [x] 移动 SSH 三阶段流程：基本信息 → 身份与主机密钥确认 → 自动部署/握手；保留草稿、编辑隔离、默认不信任、不读取上传私钥、精确失败阶段和重试门禁；live failure matrix 仍未完整覆盖。
- [x] 通知与语音完整路径：完成/失败/待审批通知去重与授权门控；成功录音→停止→预览/编辑→转写或音频附件回退→用户确认发送；mock media 与真实麦克风证据分开。
- [x] 任务详情 Drawer 动效恢复正常（移除过度测试改动 `duration: 0`），保持平滑交互体验。
- [x] 遗留测试运行目录 `.runtime/real-ui-run` 已彻底清理，无残存进程或临时 harness 污染。
- [ ] 真实 SSH/native 目录完整性复核：本轮已完成新隔离目标的 `3 vs 3` 稳定集合回读和 identity 根因记录；历史 `3 vs 12` provenance、3 个 duplicate identity 的非破坏迁移与 project-local-only activation proof 仍未完成。

### 任务切片证据（2026-09-07 继续实现）

- [x] 任务事件从 connector event → 后端校验/持久化 → `/sessions/{id}/tasks` → 前端 store/query → 运行条 → 桌面 panel/移动 sheet。
- [x] 任务详情展示真实来源会话、状态、进度、命令和公开日志；复制命令、跳最新、返回可用。
- [x] 停止任务仅在 agent 报告 `stop` 能力且任务有 opaque `target_id` 时显示；点击需显式确认，未报告能力时显示不支持。
- [x] 连接运行历史持久化与详情分页。
- [x] 移动 SSH 三阶段草稿/host-key/部署重试门禁；真实失败/retry matrix 仍未完整覆盖。
- [x] 通知去重与权限拒绝路径；语音成功录音/编辑/音频回退/确认发送。
- [ ] 真实 SSH/native 目录完整性复核与 Overlook 功能矩阵更新。

任务切片证据：

- 后端新增 `task.upsert` 公开事件校验、SQLite `TaskRow`、session-scoped API；fixture test `test_connector_task_event_is_durable_and_scoped_to_session` 通过。
- 前端 `TaskDetails`、`SessionRuntimeBar` 和 ChatPage 已接线；专测 5 项通过，生产 build 通过。
- 历史 fixture 不被扩大解释为 live runtime；本轮已在新 Astrorder-owned session 收到真实公开 hook 的 `task.upsert`，但 completed 终态和同一 live run 的浏览器页面回读仍单独列为缺口。

## 证据规则

- 设计截图/HTML只说明目标，不视为真实连接、真实任务或真实计数。
- mock、演示数据、accepted 响应和旧 worker 状态不视为用户验收。
- 未重启的预览服务不视为使用新代码；最终报告必须单独说明。
- 不读取或记录 `.env`、密钥、token、密码、私钥及既有真实会话内容；不向既有 Hermes 会话发送测试指令。

## 本阶段运行记录

### A 基线

- `backend`: `uv run pytest -q` → `51 passed, 2 warnings in 15.82s`。
- `backend`: `uv run ruff check .` → `All checks passed!`。
- `frontend`: `npm run test` → `10 files / 28 tests passed`；Node 输出既有 `--localstorage-file` warning。
- `frontend`: `npm run lint` → exit 0；既有 `e2e/ssh-multi-projects.mjs:61` `no-unused-expressions` warning。
- `frontend`: `npm run build` 首次基线失败：`SessionRail.tsx:46` 访问不存在的 `Agent.control_state`。根因是控制权字段属于 `Session`，不是 `Agent`。

### 设计审查

- V3 五张本地 PNG 已视觉审查，五张 V3 HTML 已读取；确认项目→会话两级结构、全局混排、远程标识只在项目行、连接列表/详情抽屉、工作台运行条与监控布局。
- V2 缺失的 `mobile-connections-v2`、`mobile-chat-v2`、`mobile-tasks-v2`、`mobile-voice-v2`、`mobile-ssh-v2` 已按原 screen ID 精确回读并下载到 `.stitch/designs/v2/`，五张截图与 HTML 已审查。V2 连接树/旧版本视觉不作为新侧栏来源。

### B 第一条垂直切片：项目优先侧栏

- 新增 RED→GREEN 回归：`frontend/src/components/SessionRail.test.tsx`；收尾时先复现 sourceKey 两参数调用路径和来源/数量 DOM 顺序断言，再修复。
- `SessionRail` 改为全局项目列表：稳定键为 `source_id + project_id`，无原生项目 ID 时按同一来源的 workspace 别名回归，否则保留来源隔离；不再渲染来源父节点。
- 同名/同路径项目在不同 source/profile 下保留两个项目组；远程短标识显示在项目行，DOM/CSS 顺序为项目名 → 远程来源 → 数量；本机不显示来源标签；会话行不显示连接名。
- `control_state` 只从 `Session` 读取，前端 build 已恢复。
- 增加搜索、运行中/未读/置顶筛选入口；未读/置顶只使用真实 payload 中存在的可选字段，不生成演示计数。
- 添加 `prefers-reduced-motion` 全局降级、移动侧栏控件 44px 最小热区、搜索/输入 16px 和 composer safe-area 底部 padding。
- `frontend`: `npm run test -- --run src/components/SessionRail.test.tsx` → `1 passed`。
- `frontend`: `npm run build` → Vite production build passed；仅有 chunk size warning。

### B/C/D 收尾接线与验收

- `frontend/src/state/store.ts` 新增 `selectProjects`；bootstrap 已保留 `projects` 到 Zustand；`AppShellLayout` 的桌面 Navbar 和移动 Drawer 均将项目数组传给 `Sidebar`，再传给 `SessionRail`。
- `frontend/src/components/Sidebar.test.tsx` 覆盖 Sidebar → SessionRail 的零会话项目路径；`frontend/src/state/store.test.ts` 覆盖 bootstrap hydration 保留零会话 native project catalog。
- `frontend/src/components/SessionRail.test.tsx` 覆盖同名同路径项目的 source 隔离、零会话项目、远程来源在数量左侧和无连接父分组。
- 真实浏览器 fixture（Playwright Chromium，非组件快照）覆盖桌面 1440×1000 与移动 390×844：项目目录接线、两个来源同名项目隔离、零会话项目、项目点击、连接列表保存/详情抽屉/连接状态、运行摘要展开、排队发送状态、语音权限拒绝与取消、monitor 待机队列、移动无横向溢出。结果：`desktop multi-connection/project clicks passed`、`mobile multi-connection/project clicks passed`。
- 独立 live fixture backend（仅临时 SQLite，不接入真实用户数据库）通过 `http://127.0.0.1:30102` 验证了 bootstrap 项目目录、零会话项目、远程来源与数量顺序、真实运行中状态和隔离队列记录。该 fixture 的历史接口无 connector，因此真实 live 页面显示“原生历史不可读取”和运行摘要“未报告”，不伪造运行数据。

### 数据库兼容性

- 新增 `backend/tests/test_ssh_project_repair.py::test_store_starts_from_legacy_database_and_adds_projects_without_dropping_history`：在临时 SQLite 副本中按旧版 agents/sessions schema 建库，启动 `Store` 后确认 `projects` 表由 additive startup 创建，旧 agent/session、source_session_id 保留，项目表为空且未删除历史。
- 该测试不读取或修改任何真实用户数据库；没有执行 destructive migration，也没有静默删除项目或会话。

### 此前阶段门禁结果（历史记录，不代表本轮最新工作树）

- `backend`: `uv run pytest -q` → `53 passed, 2 warnings in 16.58s`。
- `backend`: `uv run ruff check . ../connectors ../scripts` → `All checks passed!`。
- `frontend`: `npm run test` → `16 files / 37 tests passed`。
- `frontend`: `npm run lint` → exit 0。
- `frontend`: `npm run build` → TypeScript/Vite production build passed；仅保留既有 chunk size warning。
- `frontend`: `node e2e/ssh-multi-projects.mjs http://127.0.0.1:30012` → desktop/mobile 均通过；该脚本的 API 响应是隔离 fixture mock，不是真实 SSH/native backend。

### 本轮门禁修复与当前状态（2026-09-07）

- 移动 SSH 明确为三阶段：`基本信息 → 验证身份 → 自动部署`；“六步”不再作为 SSH 流程描述，六类移动页面仅属于历史设计分类。
- 新增失败回归并已 GREEN：从基本信息直接点击“3 自动部署”不会跳过身份确认；部署提交 handler 即使被错误调用也会再次校验当前阶段、稳定连接配置、当前 host-key 确认和 busy 状态。
- host、port、user、SSH alias、profile 或 identity reference 任一改变都会使旧 known_hosts 确认失效；测试覆盖主机和端口改变，并覆盖返回身份页修改 identity reference 后必须重新确认。
- 前端 checkbox 仅是用户对系统 known_hosts 审核的确认，不替代安全校验；连接仍由服务端固定 OpenSSH argv 执行，禁止自动接受未知或变化的 host key。
- 本轮聚焦验证：`SshSettingsCard.mobile.test.tsx` 与 `AgentsPage.test.tsx` → `4 passed`；connector artifact tests → `8 passed`；`uv run ruff check tests/test_connector_artifacts.py` → `All checks passed!`。
- 连接历史 structured diagnostics 已增加递归脱敏：嵌套对象、数组、敏感键名、嵌入文本和深度上限均覆盖；`test_connection_history.py` + `test_connection_history_api.py` → `3 passed`，专项 Ruff 通过。测试只使用合成字符串，不读取真实秘密。
- 通知切片已接通事件流：`task.upsert` 的 completed/failed/cancelled 与 `approval.upsert` 的 pending 进入稳定 `source/agent + session + object + state` key；重复 envelope 或文本变化不会重复 toast。`useEventStream` 只在去重成功后显示 Mantine in-app notification，并仅在浏览器权限已经是 granted 时发送系统通知。
- 通知权限默认不申请；Header 的“启用后台通知”是唯一主动申请入口，default 可点击、granted/denied/unsupported 均显示清楚状态。`notifications.test.ts`、`useEventStream.test.tsx`、`NotificationPermissionControl.test.tsx` 合计 `8` 项相关断言通过（其中前两项各 2/1，权限控件 2）。
- 语音切片已用明确 mock `MediaRecorder`/`getUserMedia` 验证真实 UI 状态流：请求→录音→停止→audio 预览→可编辑转写→“保留到草稿”；转写为空时继续以音频附件回退。取消完成录音不调用 `onCommit`，因此不会改变原草稿。`VoiceInputSheet.test.tsx` → `4 passed`；未请求真实麦克风。
- 本轮门禁后的完整回归已重新执行：backend `uv run pytest -q` → `58 passed, 2 warnings`；backend Ruff（`.`、`../connectors`、`../scripts`）→ `All checks passed!`；frontend `npm run test` → `22 files / 52 tests passed`；frontend `npm run lint` → exit 0；frontend `npm run build` → TypeScript/Vite passed，只有既有 chunk size warning；`git diff --check` → exit 0。

### 本轮真实 SSH/native 收敛（2026-09-07）

- 先用保存目标的非敏感字段执行真实 OpenSSH 预检：`ssh -G` 返回 `StrictHostKeyChecking ask`，系统 `known_hosts` 有目标条目，`BatchMode` 公钥连接成功；远端 OS 为 Linux，保存的 Hermes runtime Python 存在，Hermes executable 路径已由远端 `command -v` 回读。
- 使用新隔离 FastAPI/SQLite/connector secret 和新 Astrorder-owned session 执行真实 SSH plugin deploy、bridge start、native connector handshake、native discovery 与 disconnect；未读取或打印私钥、密码、token，未向既有会话发送 prompt，未使用 sudo。
- 真实 acceptance evidence：隔离 server port `59076`；连接在断开前为 `connected`，remote OS `Linux`，Agent `ready`，能力含 `task_events`；owned session `1`，该 Agent native sessions `32`；native projects `3`，bootstrap projects `3`，稳定 `(source_id, project_id)` 集合相等且两侧差集为空。
- 真实 API→store→connector hook 链路：新 owned session 的 HTTP command 返回 `accepted`；公开 hook connector 产生 `1` 个持久 `task.upsert`，任务状态为 `running`。未把这个实时 task event 与合成 fixture 混写；本轮尚未等待一个真实 completed 终态。
- 连接历史真实阶段回读为 `configure completed → deploy running/completed → handshake running → discovery completed → handshake connected`；disconnect 在脚本 finally 中执行。远端插件文件回读为 present，远端与本地 `__init__.py` SHA-256 相同，enabled profile 状态回读仍包含 `astrorder-hermes`。
- 远端原本已经启用 `astrorder-hermes`。为遵守 project-local-only 边界，本轮没有执行 `hermes plugins enable`；SSH bootstrap 现改为只读检查 profile 已启用状态，未启用时返回 `project_activation_required`，不会偷偷扩大为 profile-wide activation。现有 Hermes 设计仍是 profile 级启用，不应写成 project-local-only 已完成。

### 3 vs 12 catalog 差异与 identity 根因

- 本轮真实目标的 native `projects.tree` 与 bootstrap/store 都是 `3`，说明当前稳定 source/project key（`source_id + project_id`）和 zero-session project 传递路径在新隔离数据上可对齐。
- 历史 `native 3` 对 `bootstrap 12` 的根因在导入边界而非标题文本：旧 session-derived rows 把 ephemeral `agent_id`/`local-hermes-*` 当 source，且 project groups 由这些不稳定 source 与 project/workspace 组合生成；同一 native project 因不同 runtime/source provenance 被拆成多个 bootstrap groups。当前 `SessionRail` 已按稳定 source/project 分组，`reconcile_legacy_local_source` 只迁移明确无 provenance 的本机 legacy rows。
- 当前 `backend/astrorder.sqlite3` 只含空 catalog，无法安全回读历史 12 组的真实 provenance；因此没有把历史差异伪装成已迁移。远程旧 rows 若缺少 machine/profile/connection 的稳定关系，仍不能安全重键；3 个 duplicate identity 继续保留原 session/message 路由，read-time `(source_id, source_session_id)` 抑制不等于 destructive cleanup。
- 已有 additive migration、legacy local source/profile boundary 和 native nested project/zero-session 回归继续作为可修部分的保护；没有删除真实 session、隐藏真实 project 或合并不同 machine/profile。

## 功能清单边界

### 已实现并有自动化/浏览器证据

- 项目→会话两级侧栏、跨来源混排、稳定 source/project identity、同名隔离、零会话项目显示、远程来源位置、筛选入口、移动项目导航。
- 连接列表、搜索/筛选、连接详情 drawer、保存/测试/连接/断开/重试入口及状态徽标；fixture 浏览器已点击验证。
- 运行摘要条的分支/改动/后台任务/Todo/子代理字段解析、未知回退、公开日志展开；fixture 数据已验证，真实无数据时显示未报告。
- 排队发送、停止按钮能力门控、队列状态和 monitor 待机队列；fixture 浏览器已验证。
- 语音输入入口、权限拒绝、取消、停止、可编辑预览、空转写音频回退和不自动发送；mock media 单测已验证，真实麦克风未验证。
- reduced-motion、移动 44px 热区、手机输入字体与 composer safe-area、可见 focus/aria-live 相关实现。

### 仅入口或部分实现

- Todo、后台任务、子代理目前从公开 tool payload/command 数据解析；没有真实 payload 时不显示演示值。
- 连接历史 API、稳定 connection identity 持久化、分页时间线和递归脱敏 UI 已实现；本轮已用真实 SSH/runtime 回读 configure/deploy/handshake/discovery 阶段。
- 独立任务 sheet、任务 API/store、公开 hook 事件和停止能力门控已实现；本轮真实新 session 已收到 `task.upsert`，但真实 completed 终态和真实浏览器页面回读尚未在同一 live run 中完成。通知事件到 toast、稳定去重和显式权限门控已实现，真实后台系统通知仍取决于用户授权和浏览器。
- 移动 SSH 已形成三阶段表单并完成阶段跳过/旧确认失效门禁；真实 SSH deploy/start/handshake 已通过，分阶段失败/retry 的 live failure matrix 仍未完整覆盖。

### 未验证或环境阻塞

- 本轮真实 SSH plugin deploy、bridge handshake、native discovery 和 task hook 链路已通过；native discovery 的 `complete` 仍不能在报告中升级为“全历史 catalog complete”，因为历史目标的原生分页/目录完整性与旧 3 vs 12 provenance 尚未从可回读的旧 catalog 重新证明。
- 实际 live fixture backend 的 session history 因没有 connector 返回 409，运行摘要字段为未报告；这证明了未知回退而不是真实运行数据验收。
- 正常预览端口 `30001/30002` 未重启，不能声称已加载本轮新代码。本轮先核实了 `30012` 的 Vite 命令行、`30102` 的本仓库 `astrorder-server` 命令行及父进程，再通过 Windows WMI `Win32_Process.Terminate` 终止自有 PID `45072/37960`；两次返回 `ReturnValue = 0`，随后 `netstat` 确认 `30012/30102` 均无 LISTEN。旧 fixture 的 SQLite/WAL/SHM、seed script、临时真实-run SQLite 与 harness 已按精确路径删除；只保留无敏感值的 `.runtime/real-ssh-evidence-01.json`。

## User action required

- 真实远程 deploy/start/handshake 已有本轮 evidence；仍需补的是旧 catalog 的可回读 provenance reconciliation、真实 completed task 终态、失败/retry matrix，以及 project-local-only activation proof。不得把 profile-wide enabled 状态写成 project-local-only。
- 任务 sheet、移动 SSH 三阶段、运行历史、通知和语音已有实现/专项证据；剩余缺口是 live Hermes/native 验收与 catalog/source reconciliation，不是“功能范围外”。
