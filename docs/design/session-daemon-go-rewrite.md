# Session Daemon Go 全量重构开发方案

> 状态：待实施的开发与验收规范，不是已完成的重构报告。
> 交付方式：完整实现、完整验收、整版切换。禁止按 Agent、环境或功能拆成不完整的生产版本。
> 执行要求：按任务进行测试先行的实现与独立审查；内部阶段只是工程依赖顺序，不是分期上线许可。

**目标：**将星序小内核的全部现有宿主能力迁移为 Go 实现，保留原生 Agent 行为和大内核协议兼容性，同时消除小内核内的跨会话阻塞、慢订阅者阻塞及恢复盲区。

**架构：**独立 Go Session Daemon 继续作为本机与远程 Agent、SSH、PTY 的运行时所有者。Python 大内核保留业务、状态投影和浏览器服务；原生 Agent 继续拥有其聊天历史与配置真值。Go 候选版本只能在隔离环境开发验证，全部门禁通过后一次性替换 Python 小内核。

**技术方向：**Go、兼容现有 JSON/WebSocket IPC、原生进程与 PTY、受控 OpenSSH 子进程、有限队列与每会话事件日志。依赖版本在实施开始时审计并锁定，不在本文猜测最新版本。

---

## 1. 决策与不可变约束

### 1.1 “一次性完成”的定义

一次性完成是**功能完整性和生产发布的原子边界**，不是要求所有代码在一个提交内完成。

- 允许分模块编写、测试、审查和提交；生产仍使用完整的旧 Python 小内核。
- Go 首次生产上线必须同时覆盖本机 Codex/Hermes/Grok、远程 Codex/Hermes/Grok、PTY、连接器接入、原生控制、附件、历史、模型配置执行与生命周期管理。
- 保留用户的启用/禁用配置；“全部实现”不意味着强行启用用户禁用的功能。
- 禁止 Go 只处理 Codex、其余转回旧 Python Daemon；禁止用 Python 小内核代理来伪装 Go 已实现。
- 禁止以隐藏按钮、关闭环境、删除测试、固定返回成功或临时轮询历史代替功能完成。
- 任一原有支持能力未实现、关键真实环境未验证或回滚未演练，整个版本不准发布。
- 不采用生产中新旧小内核按会话分流的长期双栈模式。隔离测试可以同时运行两个版本，但不能共同控制同一原生运行时。

### 1.2 会话安全红线

- 大内核的发布、重启、崩溃不能终止 Go 小内核及其托管进程。
- 替换或重启小内核前，必须读取**已认证的小内核权威状态**，检查 `running` 和 `waiting_approval`；另检查尚未结束的创建、发送、停止、审批等操作。
- 存在上述工作时，默认等待排空；只有用户明确同意中断才允许继续。
- 状态无法读取、不完整或存在歧义，按有活动工作处理，必须取得用户明确同意；此时不能声称已经安全排空。
- 本文和“同意 Go 重构”均不构成中断现有会话的授权。
- 不承诺将 Python 小内核已持有的管道、ConPTY、SSH 句柄无缝移交给 Go。切换需要维护窗口，闲置会话依据原生 ID重新挂接；正在执行的轮次不得偷偷重启后重发。
- 禁止按进程名批量杀死 Codex/Hermes/SSH。停止动作必须限定到已验证的自身所有权。

### 1.3 范围边界

本次必须完成小内核全量实现，以及让它可运行、可安装、可升级所需的大内核桥接和服务脚本适配。

本次不全面重构前端、不重写 FastAPI、不替换原生 Agent、不改变模型请求链路、不迁移用户原生会话数据库。前端“刷新后才看到回复”作为跨层验收场景；若发现大内核或前端确切缺陷，以最小伴随修复解决，不将其冒充小内核性能收益。

## 2. 源码基线与已确认问题

### 2.1 基线来源

审阅时仓库 HEAD：`3cafe7fd4253a74f2951d61c3333054b68921b05`。工作区存在其他任务未提交修改，因此 HEAD 不代表全部被审阅文件的精确内容。实施任务 T0 必须对实际源文件形成不可变基线与散列清单，不覆盖其他任务变更。

主要依据：

- `backend/src/astrorder/daemon/session_daemon.py`
- `backend/src/astrorder/daemon/model_config.py`
- `backend/src/astrorder/daemon/bridge/client.py`
- `backend/src/astrorder/daemon/runtimes/{codex,hermes,grok,ssh,pty}/`
- `backend/src/astrorder/connections.py`、`backend/src/astrorder/ssh_transport.py`
- `scripts/{daemon_service,production_daemon,service_lifecycle,desktop_service}.py`
- `backend/tests/test_session_daemon.py` 和全部 `test_daemon_*.py` 及相关原生集成测试
- `docs/design/dual-kernel-architecture.md`

已有架构文档、模块头部中的“Phase 1”等文字存在历史性描述。**实际代码、调用方与测试共同决定迁移清单，不能仅根据旧文档或 daemon 目录大小估算工作量。**

### 2.2 源码事实与诊断边界

| 源码事实 | 对重构的要求 |
|---|---|
| `publish()` 在 `_publish_lock` 内依次 `await socket.send()` | 事件接收不得等待慢订阅者；拆成有限队列和独立写循环 |
| `session.sync` 在相同发布锁内构造并发送回放，随后注册订阅 | 新实现必须保留无缝接续语义，但不持全局锁执行网络写 |
| `_request_runtime()` 在 `_runtime_lock` 中等待 native query；其他运行时操作也需逐一审计锁范围 | 不同环境/会话不可被慢查询或初始化串行拖住 |
| `_SessionWAL` 是有界内存 `deque`，并非磁盘持久 WAL | 不能声称现有实现小内核崩溃后可恢复这些帧；Go 的磁盘日志属于显式增强 |
| 大内核 Bridge 按 `daemon_id` 维护 checkpoint，识别 overflow 和 daemon 更换 | 保留 epoch、游标、溢出与重建协议，不只保留字段名字 |
| runtime 类型为 `codex`、`pty`、`hermes`、`ssh`、`codex-ssh`、`grok`、`grok-ssh` | 必须全部覆盖，不能把远程 Hermes 的 `ssh` 擅自改名 |
| 大内核环境恢复代码也存在顺序调用和共享锁 | Go 上线本身不能宣称已经解决所有启动排队 |
| 前端有重连和游标，但单模块缺少应用层停滞检测 | 需要端到端验证；当前未确认具体一次断流的根因 |

这些是代码结构证据，不是已测得的性能占比。本文中的性能数字均为**待验证验收预算**，不是现有系统实测结果。

## 3. 全量功能迁移矩阵

每行必须在机器可读 `parity-manifest.json` 中细化为“现有入口、实际支持环境、输入/输出 fixture、Go 实现、测试证据、负责人、状态”。禁止仅以“已实现该 runtime”一项勾选通过。

| 能力域 | 必须保留的行为 | 现有来源 |
|---|---|---|
| IPC | loopback 绑定、认证、request_id 关联、错误信封、大小限制、握手与重连 | `daemon/session_daemon.py` |
| 事件 | 原始 payload、会话 seq、状态、回放、overflow、实时接续、daemon epoch | 同上、`daemon/bridge/client.py` |
| Connector | `/ws/v1/connector`、独立凭据、hello、事件、指令返回、断开、待绑定事件、Agent 控制会话 | 同上；connector 相关测试 |
| 本机 Codex | app-server 启动/初始化、创建/恢复、原生查询、send/steer/interrupt、compact/review、审批、设置、改名/删除、桌面停止事件观察 | `runtimes/codex/runtime.py`、`desktop.py` |
| 远程 Codex | SSH 环境/路径处理、进程启动、恢复、完整原生 RPC/通知/审批、连接级释放 | `runtimes/codex/remote.py` |
| 本机 Hermes | 官方启动器/插件、gateway.ready、创建/恢复、发送/停止、历史分页、附件、原生模型/推理/审批控制、改名/删除、compaction 事件 | `runtimes/hermes/`、`connections.py`、Hermes 插件 |
| 远程 Hermes | SSH 建链、反向隧道、插件就绪、远程历史/附件、控制、连接级与会话级生命周期 | `runtimes/ssh/runtime.py`、`ssh_transport.py` |
| 本机/远程 Grok | ACP/实际现有传输协议、创建/加载、prompt、取消、模型列表和切换、推理设置、原始更新与配置重载 | `runtimes/grok/runtime.py`、`remote.py` |
| PTY | 本机 shell、输入/输出、resize、interrupt、close、断开浏览器后继续托管、退出事件、输出回放 | `runtimes/pty/runtime.py`、`core/terminal_service.py` |
| Native controls | 原有 models/commands/model/reasoning/approval 读写，能力发现与不支持错误 | `runtimes/hermes/native_control.py`、`native/controls.py` |
| 模型配置 | `model_config.plan/apply/reload`；本机和远端 TOML/JSON 验证、原子替换、备份、失败回滚、权限和凭据落地 | `daemon/model_config.py` |
| 外部会话 | CLI/TUI/原生桌面产生的会话仍可发现、归属项目、读取历史；不强制先 spawn | App 原生读取器、runtime query、现有历史回退 |
| 生命周期 | 独立进程、单实例锁、可信身份、status、维护/停机、活跃会话保护、配置、安装升级回滚 | `scripts/daemon_service.py` 等 |

**不支持也是契约：**某个 Agent 当前不支持的动作必须保持明确的不支持行为，不要求虚构支持；但不得把原本支持的动作改成“不支持”。按能力矩阵验证，而不是假设每个 Agent 实现全部动作。

### 3.1 必须审计 daemon 目录之外的依赖

Python runtime 目前依赖 `LocalHermesController`、`SshNativeRuntime`、`TerminalSession`、`astrorder_codex_connector.app_server`、native controls 及配置/附件工具。相关的**小内核执行职责**必须迁入 Go，而不是把 import 去掉后丢失行为。

可保留：Python 大内核 bridge/controller 代理、投影代码、Hermes 原生 Python Agent 与其插件、Agent 自身所需 Python。

不可保留：Go 外壳启动旧 `astrorder.daemon.session_daemon`；以常驻 Python worker 实现小内核核心；继续依靠旧 Python runtime 来完成 native 控制、PTY 或 SSH 托管。

远端配置/文件处理若现用星序注入 Python 脚本，迁移为受限的 Go 一次性 helper 或 Go 侧受控 SSH 文件操作。一次性 helper 不是第二个常驻小内核，不取得 Agent 生命周期所有权。Hermes 原生协议确实要求插件执行的 Agent 内动作仍由原生插件完成，不要求把 Hermes 本体改写为 Go。

## 4. 目标结构与技术选择

建议新增目录（均为待创建）：

```text
session-daemon-go/
  go.mod
  go.sum
  cmd/astrorder-sessiond/main.go
  cmd/astrorder-runtime-helper/main.go
  internal/config/
  internal/protocol/
  internal/server/
  internal/sessions/
  internal/events/
  internal/journal/
  internal/operations/
  internal/process/
  internal/transport/ssh/
  internal/runtime/codex/
  internal/runtime/hermes/
  internal/runtime/grok/
  internal/runtime/pty/
  internal/connector/
  internal/modelconfig/
  internal/observability/
  internal/testkit/
  tests/contract/
  tests/integration/
  tests/fault/
  tests/benchmark/
  testdata/contract/
  testdata/parity-manifest.json
```

### 4.1 依赖原则

- Go 标准库优先；WebSocket 库必须支持单读单写、取消、读写 deadline、消息上限和 ping/pong。
- SQLite 驱动优先评估可原生交叉构建方案；Windows/Linux 上验证事务、WAL 和故障恢复后固定版本。不能为了 CGO-free 牺牲稳定性。
- Windows 使用 ConPTY；Linux 使用真实 PTY。不能用普通 stdin/stdout 管道冒充终端。
- SSH 首版优先保留系统 OpenSSH 的配置、known_hosts、密钥和 ssh-agent 语义，由 Go 管理进程与隧道；不能静默关闭主机密钥验证。若采用内置 SSH 库，必须先证明这些能力等价。
- 远端 helper 需要平台/架构探测、校验和验证、受限缓存目录和确定版本。不能首次发消息时临时下载不固定版本的可执行文件。
- 构建目标至少覆盖当前 Windows 宿主及 Debian/WSL 远端所需 helper；按真实环境记录 OS/架构。整套 Daemon 的 Linux 构建纳入 CI，原生进程与 PTY 在对应 OS 运行，不把交叉编译成功当运行验证。

### 4.2 并发模型

- 全局 registry 锁只保护索引增删，不等待磁盘、网络、进程启动或 native RPC。
- 相同 `(connection_id, agent_type)` 的初始化使用 singleflight；不同连接可并发。
- 每个会话有单一状态协调器，保证状态和 seq 顺序；native I/O 在可取消任务中运行，完成后回送结果。
- 中断与审批不能排在一个长时间 prompt 完成之后。会话协调器发起运行操作后继续接收控制消息。
- 连接级和全局并发均有限额；慢历史查询、模型配置写入、环境探测使用独立资源预算，不占满实时转发资源。
- 每个订阅者单独有限队列、单写协程；网络回压不能持有 session/registry 锁。
- 每条任务都必须有明确所有者、deadline、取消和清理路径，不能依赖无限 goroutine 吞掉积压。

## 5. 协议兼容规范

### 5.1 保留的传输

- 控制 IPC：`ws://127.0.0.1:30009/`（端口可配置），不是 HTTP REST health 服务。
- Connector：`/ws/v1/connector`，沿用实际认证和消息格式。
- 保留 loopback 限制、独立 daemon/connector 密钥、现有默认消息上限语义；不把原生模型密钥放到 argv、URL、状态或日志。
- 请求仍使用 `action`、`request_id`；不得因为“Go 重写”改成不兼容的通用 JSON-RPC 2.0。
- 未知 action、非法 JSON、错误类型、缺字段、空值/缺省值、错误输出都进入契约测试。

### 5.2 必须覆盖的 action

控制/运行时：

```text
daemon.handshake  daemon.status  daemon.shutdown
session.sync  session.create  session.spawn  session.observe_status
runtime.request  runtime.disconnect
model_config.plan  model_config.apply  model_config.reload
```

会话 action：

```text
session.send  session.compact  session.review  session.steer
session.interrupt  session.approve  session.settings  session.resize
session.disconnect  session.history_page  session.delete  session.rename
session.close  session.models  session.commands
session.model.read  session.model.set  session.reasoning.set
session.approval.read  session.approval.set
```

`runtime.request` 内层方法和 Connector 消息不能只按外层 action 计入完成；T0 逐项抽取调用方、runtime query 实现和测试，枚举所有现有 native 方法、通知和返回字段，包括调用方使用的分叉/列表/读取等能力。未登记的新调用必须使契约覆盖检查失败。

### 5.3 信封和身份

保持：

```json
{
  "session_id": "opaque-native-session-id",
  "seq_id": 1,
  "timestamp": 0.0,
  "event": "native-event-name",
  "payload": {}
}
```

此处仅展示形状，不代表真实事件数据。

- Agent/connection/session/turn/approval/command/request ID 按不透明值保留，不根据标题、时间或数组位置猜身份。
- `timestamp` 保持现有秒单位和来源语义；内部性能测量另用单调时钟。
- JSON 使用保留数字精度的解码策略；未知 native payload 保留原始结构，不经 float64 随意往返。seq 的可表达范围必须与 Python/JS 调用方契约一致并检查越界。
- 协议兼容新增字段采用明确 capability/version；旧字段、原有状态集合不直接替换。
- 新增内部 `recovering`/`stopping` 等生命周期不擅自塞进旧 `status` 枚举；通过可选扩展字段表示，无法确认运行态时不得伪报 idle。

### 5.4 兼容扩展

为维护切换和幂等性，可以新增受认证的维护入口和 operation 查询能力。它们必须有独立 capability，配套大内核/脚本适配，并进入同一发布包。正常旧调用不要求无条件改用新字段。

## 6. 事件、持久性与恢复

### 6.1 保证范围

Go 新实现提供：

1. 会话内有序，跨会话不强求全局总序。
2. 已接受事件先写入小内核日志，再对外发送；大内核确认进度仍以投影持久化完成为准。
3. 传输为至少一次，消费按 `(daemon_id, session_id, seq_id)` 幂等；不声称跨 Agent exactly-once。
4. 日志是有限保留的传输恢复数据，不是替代 Agent 原生聊天历史的第二权威库。
5. 小内核退出后的子进程是否存活依赖平台和句柄模型。日志存活不等于原运行进程能够接管，必须单独报告。

### 6.2 日志实现

建议独立 `sessiond.sqlite3` 存放于平台正式数据目录的 `session-daemon/` 下，不与 App 数据库共享写事务，不放仓库 `.runtime/`。

- 核心键：`daemon_epoch + session_id + seq_id`；状态快照与事件写入保持一致。
- 单写协调器进行小批次事务；按会话公平调度；设置总字节数、单会话字节数、保留期限和队列上限。
- 明确 SQLite WAL、busy timeout 和同步级别。默认可靠模式采用持久提交后发布；任何放松持久性策略必须明确公布可丢失窗口，不得默默降级。
- 写失败/磁盘满：不推进成功 checkpoint，不返回虚假成功。拒绝新工作并暴露 degraded；对在途 Agent 输出采用有界缓冲与明确告警，必要时受控中断，不能无限内存缓存或静默丢弃。
- 清理只在规定保留规则下进行；不能因为 socket 已写出就删除，发送不等于应用成功。
- 原始敏感内容不写诊断日志；事件库采用最小权限，遵循用户原生内容保留策略。

### 6.3 原子回放接续

在短临界区捕获会话高水位 H 并注册实时积压队列；在锁外按顺序发送 `checkpoint < seq <= H` 的回放，再发送 `seq > H` 的积压，然后转实时。snapshot 与接续必须针对同一 epoch。

- 注册/回放之间产生的事件不能漏掉。
- 回放期间消费者变慢，不能反过来堵住其他订阅者。
- 原有 `session.sync.result` 形状保持兼容；大回放不得突破消息上限。需要分块时用可协商扩展和配套 Bridge，不能随意分包使旧客户端误解析。
- 对过旧游标返回明确 overflow。Bridge 必须用原生快照/历史完成重建，再衔接新的高水位，而不是跳过以后永久不恢复。
- 新会话在 status 与 sync 间出现也必须可发现，不能因未包含在最初请求字典中永久失联。

### 6.4 慢消费者与半开连接

- 每个消费者独立 write deadline；队列满则关闭该消费者并要求重连回放，不丢帧后继续假装健康。
- ping/pong 检测传输生存；另有事件日志高水位/投影进度检查以识别“socket OPEN 但消费停滞”。
- Agent 暂时不输出不等于断流，不能仅因没有 token 自动重发用户消息。
- 分别记录 socket 收到、日志提交、大内核投影、浏览器应用时间，定位哪一跳停滞。

### 6.5 epoch 与重启

每次小内核进程启动生成新 `daemon_id`，遵循现有 Bridge 的重启识别。旧 epoch 日志保留用于审计/恢复，不能直接伪装为新 epoch 的实时运行状态。

重启后先校验原生会话和进程所有权；能安全恢复的按原生 ID 重新绑定。无法接管的标为可见错误/待恢复，不根据旧持久 status 继续显示 running 或 idle。维护切换前要求 App 已投影到旧 epoch 高水位；无法补齐时阻止无损切换或明确提示需原生历史重建。

## 7. 命令与状态机

- 状态兼容 `running`、`waiting_approval`、`idle`、`error`；每个转换必须有真实 native 事件或可验证动作结果依据。
- 区分请求接收、接受、native 派发、运行、完成；请求超时不等于底层未执行。
- 引入持久 operation 台账：有稳定 command ID 的优先使用；创建等无稳定 ID 的由大内核生成 operation ID并在重试时保留。
- 同 ID同 payload 返回既有结果/进度；同 ID不同 payload 拒绝；台账按 epoch/作用域及保留策略管理。
- native 不支持幂等且在“已派发但未记录结果”崩溃时，标为结果不确定并查询原生状态，不能自动再发 prompt。
- 创建超时后查 operation/native identity，回收晚完成孤儿；不盲目新建第二个会话。
- 等待审批时保存原始 approval/request ID，App 离线不自动批准；重连恢复审批，重复提交不能执行两次。
- Connector 旧连接的迟到 close 不能清掉同 Agent 的新 socket；使用连接 generation 校验。
- 删除成功要求原生删除、runtime 解绑和 App 投影一致；rename/settings/model 的成功以原生 readback 证明。
- 连接就绪只能触发只读发现，不能发送隐藏 smoke prompt、创建隐藏会话。

## 8. 各运行时实施要求

### 8.1 Codex

完整移植本机和 SSH app-server 传输，包含请求 ID分发、通知、双向审批请求、错误返回、stderr 隔离、线程创建/恢复、模型绑定、review/compact/steer/interrupt。审查输出原文透传，不能修改 `code-comment` 格式。

保留临时 thread 对改名/元数据修改的特殊约束、delete 时原生打开句柄检查、已有桌面 stop 观察逻辑。桌面桥接不可被视为“非核心”直接删除。未知通知原样转发或显式诊断，不静默吞掉。

### 8.2 Hermes

复用现有官方启动器与 Agent 原生插件协议，而不是绕开插件抓 stdout 代替结构化事件。实现 gateway.ready、hello/control 绑定、stream start/delta/end、工具、thinking、附件、审批及 compaction。

本机/远端都验证原生模型、推理、审批读写、历史分页、改名和删除。历史优先遵守原生存储真值；未由 daemon spawn 的外部会话仍能由大内核只读回退读取。不得通过复制历史到新数据库声称完成兼容。

### 8.3 Grok

按现有协议实现，不把 Grok 当 Codex 字段别名。覆盖原生 initialize/new/load/prompt/cancel 等实际调用、更新通知、模型列表、模型/推理设置与配置 reload。远程运行路径、cwd、环境继承和退出都必须实测。

### 8.4 SSH 与远端 helper

保留 host/user/port/key/config 等实际支持配置、known_hosts 验证、远程工作区约束、代理/环境处理及 UTF-8路径。

- 反向隧道明确绑定 `127.0.0.1`；检查端口冲突和远端转发失败。
- 单条 SSH 故障隔离到该连接；其他本机与远端会话继续输出。
- shell 参数转义、换行、空格、非 ASCII 路径和凭据不进入命令行的要求全部测试。
- helper 只能执行白名单操作，不提供任意 shell/RPC 入口；结果遵守原有配置/文件契约。
- WSL 依据现有真实接入方式测试，不擅自把 SSH 接入改成 wsl.exe，或反之。

### 8.5 PTY 与进程所有权

Windows ConPTY、Linux PTY 分开实现和运行测试。PTY 原始输出需正确处理跨块 UTF-8、ANSI、二进制/字节编码和流控，不能截断字符。

Windows Daemon 必须脱离 App 的 Job/进程树生命周期；子进程使用归属于 Daemon 的精确所有权机制，验证 App 退出与桌面退出行为。Linux 验证进程组、信号和回收；不能使用会随 App 父进程死亡而杀死整个 Daemon 的配置。

明确区分浏览器断开、App 断开、runtime disconnect、会话 close 与 Daemon shutdown。清理只针对自己的 PID/句柄，并验证 PID复用，不得按映像名粗暴终止。

### 8.6 模型配置不是可延期项

虽然长期可以重新评估职责位置，本次必须保持 `model_config.plan/apply/reload` 完整可用，不能为了让小内核更小删掉。

保留已管理文件集合（Codex config/catalog/magpie catalog、Grok config）、BOM/编码处理、校验、hash、备份保留、文件权限、Windows 凭据/环境落地、远端 environment.d 行为。多文件更新失败必须补偿恢复；并发修改有冲突检测，不能覆盖用户新编辑。重载不允许打断正在执行的轮次，pending/reloaded 返回反映真实状态。

## 9. 大内核、前端与发布脚本适配范围

### 9.1 必须随迁移交付

- `backend/src/astrorder/daemon/bridge/client.py`：兼容 epoch、回放、溢出重建、新扩展和幂等操作，不收到就提前确认。
- `backend/src/astrorder/daemon/bridge/{codex_projection,hermes_projection,hermes_compaction_projection}.py`：现有事件完全兼容，防重复、不丢最终消息。
- `backend/src/astrorder/daemon/runtimes/*/control.py` 和 `runtimes/pty/relay.py`：这些是 App 侧代理，可以保留 Python，明确迁移后的目录/职责；禁止误删。
- `scripts/daemon_service.py`、`production_daemon.py`、`service_lifecycle.py`：启动 Go 二进制、单实例、身份验证、权威状态、受控停机。
- `scripts/desktop_service.py`、`run_production.py`、安装打包入口：保持 App-only restart，打包完整 Go 程序与必要 helper。
- 生产配置/安装包必须同步更新，但维护窗口前不覆盖运行中的小内核程序。

当前 `DAEMON_MODULE_MARKER = astrorder.daemon.session_daemon` 只能识别 Python 进程。新身份必须组合精确可执行路径、进程启动时间、PID、监听端口、认证握手、build ID/epoch；不能让 Go 伪造 Python 命令行，也不能仅按进程文件名信任任意监听者。过渡脚本可以识别旧版本以安全停止/回滚，但生产运行只选择一个实现。

### 9.2 伴随性能修复

已确认的大内核连接恢复串行点可作为独立任务修复：`core/environment_connections.py`、`main.py`。不同连接并发、同连接互斥、探测复用，不动原生所有权；必须与 Go 核心性能分开报告。

前端只在复现证明需要时修改 `api/eventStream.ts`、`hooks/useEventStream.ts` 和相关状态逻辑，处理游标应用失败、假连接、重建恢复。广泛页面虚拟化/组件拆分另立项目，不塞入本次小内核完成定义。

## 10. 开发任务与发布门禁

每个代码任务遵循：添加可失败测试 → 实际运行确认失败 → 最小实现 → 单测/契约/相关集成通过 → 独立审查。任务允许细拆成小提交，以下里程碑均不允许单独上线。

### T0：冻结现有功能与协议基线

- 新建 `session-daemon-go/testdata/parity-manifest.json`，枚举所有 action、runtime query、Connector 类型、CLI参数、环境变量、配置字段和退出语义。
- 新建 `tests/contract/` 的双实现测试入口及脱敏 fixtures。
- 对现有 Python 测试做映射；记录原有失败与原因，不把原有失败当作 Go 豁免。
- 记录现有工作区源文件散列、构建信息和平台；固定本机/Debian/WSL 真实验收清单。
- 门禁：每个现有能力有可执行判定；基础差分测试必须能抓出故意缺字段/丢帧的错误实现。

### T1：Go 工程、协议与生命周期骨架

- 实现配置校验、认证、单实例、Go 可信身份、状态、维护与安全停机。
- 端口冲突、外部监听者、错误凭据、重复启动、缺配置用例先失败再实现。
- 修改服务脚本以支持隔离启动候选；生产默认不切换。
- 门禁：候选在隔离端口运行，不能误连或停止生产 Agent。

### T2：会话、日志、订阅与故障恢复

- 实现 session coordinator、journal、独立 subscriber queue、sync 接续、epoch、operation 台账。
- 先写并发发布/回放间隙/慢订阅者/重复请求/磁盘满/崩溃边界测试。
- 门禁：事件有序、消费幂等、失败明确，状态和日志一致；持续输出不被慢客户端拖住。

### T3：完整本机运行时

- Codex、Hermes、Grok、PTY 按第 3、8 节全部移植，不只完成发送消息。
- 每实现一项关联 parity fixture 与 Go 测试，使用隔离原生会话验证 native readback。
- 门禁：本机完整能力矩阵通过，App 离线期间真实 Agent 继续执行并可回放。

### T4：完整远程运行时及配置执行

- 移植 SSH、远程 Codex/Hermes/Grok、附件/历史、helper 和 model_config。
- Debian/WSL 分别覆盖连接、创建、控制、历史、附件、取消、审批（实际支持时）、配置写回与清理。
- 门禁：远程全部能力通过，拔掉一条 SSH 不影响其他连接；没有核心 Python worker 回退。

### T5：全栈对接、安装包与安全检查

- 完成 Bridge、projection、PTY relay、生命周期和安装包适配。
- 如需要，最小修复环境恢复与前端事件接续，独立测试和报告。
- 门禁：完整安装包可在干净环境启动；大内核和桌面常规升级不替换/重启小内核。

### T6：全量差分、真实验收、性能及回滚演练

- 执行第 11 节矩阵，输出原始测试证据、指标和已知差异。
- 真实 Agent 测试使用授权的临时工作区和会话，可能产生模型费用，未经授权不向现有会话发送 smoke prompt。
- 任一必测环境不可用则阻止发布，不用 fake 宣称通过。
- 门禁：完整性、安全性、可靠性、性能、回滚全部通过，才可申请生产窗口。

### T7：一次性整版切换

- 严格执行第 12 节，发布 Go 完整包以及必要匹配 App/脚本版本。
- 无缺失才解除维护模式；失败整版回滚，不将某个 runtime 路由回 Python 补洞。

## 11. 验证计划与量化标准

### 11.1 复用测试来源

现有 Python 内存对象单测不能直接证明 Go 行为。保留其作为旧实现基线，并把行为迁入黑盒 IPC测试和 Go 单测；App 侧测试继续运行。

至少映射以下实际文件：

- `backend/tests/test_session_daemon.py`
- `backend/tests/test_daemon_bridge.py`
- `backend/tests/test_daemon_connector_proxy.py`、`test_daemon_connector_disconnect.py`
- `backend/tests/test_daemon_codex_runtime.py`、`test_daemon_remote_codex_runtime.py`
- `backend/tests/test_daemon_codex_control.py`、`test_daemon_codex_projection.py`、`test_daemon_codex_api.py`
- `backend/tests/test_daemon_hermes_runtime.py`、`test_daemon_hermes_native_control.py`、`test_daemon_hermes_restart.py`
- `backend/tests/test_daemon_hermes_history.py`、`test_daemon_hermes_attachment_runtime.py`、`test_daemon_hermes_attachments.py`
- `backend/tests/test_daemon_hermes_projection.py`、`test_daemon_hermes_control.py`、`test_daemon_hermes_app.py`
- `backend/tests/test_daemon_ssh_runtime.py`、`test_daemon_ssh_history.py`、`test_daemon_ssh_restart.py`、`test_daemon_ssh_control.py`、`test_daemon_ssh_app.py`
- `backend/tests/test_daemon_grok_runtime.py`
- `backend/tests/test_daemon_pty_runtime.py`、`test_daemon_terminal_relay.py`、`test_daemon_terminal_endpoint.py`
- `backend/tests/test_daemon_attachment_frame.py`
- `backend/tests/test_daemon_service_scripts.py`、`test_production_daemon.py`、`test_daemon_config.py`
- `backend/tests/test_native_controls_daemon.py`、`test_native_codex_daemon_e2e.py`

此列表是入口而非穷尽证明；T0 还要检索 `model_config`、SSH transport、connector、native controls 和安装相关测试。

### 11.2 契约与差分

相同 fixtures 分别请求隔离 Python 和 Go server，比对结果、错误、状态、事件、native 调用轨迹。仅可规范化随机 daemon ID、随机创建 ID和时间等明确白名单字段；不排序会话内事件、不抹掉错误、不忽略丢失字段。

错误场景覆盖超大/畸形 JSON、错误身份、重复 request/command、迟到回复、初始化失败、native 退出、权限拒绝、路径逃逸、未知动作和不支持能力。

### 11.3 真实环境矩阵

| 环境 | Codex | Hermes | Grok | 额外项 |
|---|---|---|---|---|
| 本机 Windows | 全部现有支持能力 | 全部现有支持能力 | 全部现有支持能力 | ConPTY、中文路径、长路径、App/桌面退出 |
| Debian 远端 | 同上 | 同上 | 同上 | SSH 中断/恢复、文件权限、反向隧道 |
| WSL 远端 | 同上 | 同上 | 同上 | 实际接入方式、路径边界、环境初始化 |

每个适用格至少证明：连接不创建隐藏会话、创建/恢复、连续输出、工具结果、取消、模型控制、历史、清理；附件、审批、compact/review/steer 等按真实支持矩阵验证。另覆盖外部创建会话的发现和项目归属。

### 11.4 必测故障场景

- App 停止时 Agent 继续输出，App 恢复自动补齐，不需要手动连接/刷新。
- 输出过程中订阅者读速率降到零，其他会话不被阻塞。
- sync 同时有新事件/新会话，既不丢失也不永久重复。
- WebSocket 半开、SSH 单端断开、进程退出、磁盘满、日志损坏尾部、消费投影失败。
- request 超时但 native 已完成，重试不重复创建/发送。
- 待审批状态重启 App 后仍可正确批准/拒绝，不误判 idle。
- 浏览器处于后台后恢复，最终回复自动可见；人工刷新不得成为验收必需步骤。
- 内存、goroutine、FD/句柄和子进程长期无无界增长。
- App-only restart：App PID变更、Daemon PID/epoch不变，托管会话与轮次不变。
- 小内核候选崩溃只在隔离环境注入，记录能够恢复与无法接管的边界，不声称进程透明迁移。

### 11.5 性能预算

以下为设计验收目标，T0 固定硬件、构建模式、磁盘、事件尺寸与负载，保存原始样本；调整目标必须经评审并在文档说明，不能测完后悄悄放宽。

- 基准负载：活跃会话持续产生合成原生事件，逐档增加并发；必须覆盖用户当前全部连接同时活跃。合成测试不伪装真实模型耗时。
- 主要指标：Go 收到完整 native 事件至事件日志提交并进入健康订阅者发送队列，p95 ≤ 20 ms、p99 ≤ 100 ms。
- 空闲 `daemon.status` p95 ≤ 100 ms；在并发输出和慢消费者下仍可响应并给出真实状态。
- 单一慢 SSH/历史查询不能使无关会话 p99 超出预定预算；与 Python 基线比较队头阻塞，而非只比较无负载平均值。
- 单独报告热挂接与冷启动，报告每环境就绪时间；不把 LLM 首 token 或 SSH 网络等待算成语言执行收益。
- 端到端健康前台页面：已收到 native 最终回复至显示的目标 p95 ≤ 300 ms。若前端渲染超预算，标明边界并处理关键阻塞，不伪称 Daemon 已保证 UI 性能。
- 固定负载长稳测试至少 8 小时；预热后 heap/goroutine/句柄无持续增长，日志和队列不突破配置上限。

### 11.6 验证命令约定

以下是实施后要执行的命令，不是本次文档编写已运行的结果。新增测试目录/脚本由对应任务创建。

```bash
# 在 session-daemon-go/ 下
go test ./...
go vet ./...
go test -race ./...
go test ./tests/contract/... -count=1
go test ./tests/fault/... -count=1
go test ./tests/benchmark/... -run '^$' -bench . -benchmem

# 在仓库根目录；Windows 使用现有 backend 虚拟环境
backend/.venv/Scripts/python.exe -m pytest backend/tests -q

# 在 frontend/ 下；按仓库实际 package scripts 执行测试、类型检查和构建
npm run
```

`-race` 需平台工具链支持，Windows 与 Linux 原生 CI提供相应环境；不能因本机缺编译器而跳过整个 race 门禁。真实环境测试通过明确 opt-in 启动，不包含在默认无凭据单测中。

最终输出 `docs/reports/session-daemon-go-acceptance.md`（待创建）：版本、能力矩阵、真实环境、命令及退出码、测试失败/跳过、性能样本路径、安装/回滚证据、未验证项。未验证的必测项意味着不得上线。

## 12. 一次性发布与整版回滚

### 12.1 发布前

1. 冻结匹配的 Go、App bridge、脚本、helper 和插件兼容版本，生成完整包与校验清单。
2. 候选在隔离端口和独立数据目录验收；生产仍完整运行 Python 小内核。
3. 在维护窗口前读取权威状态，告知将发生一次小内核切换，默认等待活动工作结束。
4. 关闭新建/发送入口并停止自动编排触发，继续允许已有轮次完成、停止与审批。
5. 读取 Daemon 状态、运行时状态和未决操作，再确认没有新工作进入。旧版本缺少 admission gate 时，先断绝外部命令入口，再利用其受锁保护的 shutdown 检查；不能仅依赖一次页面快照。
6. 让 App checkpoint 追到旧 Daemon 高水位；保存真实 native ID、连接配置、工作区和恢复信息，不导出密钥到报告。
7. 对实际平台数据目录做一致性备份：SQLite 使用备份 API或一致快照，不能只复制正在写的 `.db` 忽略 WAL。保留旧可执行环境、App/脚本包和配置。

### 12.2 切换步骤

1. 经认证请求旧小内核安全 shutdown；有活动工作则拒绝，除非用户针对中断明确批准。
2. 验证旧监听者退出、所有权清理与日志完整，不杀未知进程。
3. 原子选择完整 Go 发布目录，由升级脚本启动；不覆盖仍被占用的 Windows exe。
4. 验证可执行身份、认证、epoch、runtime 注册完整性、配置和数据目录。
5. 启动匹配 App，按 native ID恢复绑定；只读核对全部原有连接与会话。
6. 执行授权的最小验收，检查事件/历史/审批/终端，不向现有用户会话偷偷发测试消息。
7. 全部检查通过才开放新建、发送与自动化入口；发布记录声明 Go 完整版接管。

### 12.3 回滚

- 验收失败且尚无新增活动工作：停止候选，恢复匹配旧 App/脚本/配置，启动旧 Python Daemon，依据 native ID重新挂接。
- 已开放用户工作后出现故障：先冻结新工作，再查 Go 权威状态；有活动工作仍需等待或用户明确批准中断，不能因“回滚紧急”绕过规则。
- Go 新日志不要求旧 Python 理解；回滚前排空投影并记录 epoch，原生历史仍保留。禁止把故障前备份覆盖到已经包含用户新会话/消息的原生数据库。
- App 数据 schema采用可兼容扩展，若不兼容则在发布前具备已验证的降级/转换路径；不能盲目恢复旧数据库丢掉发布后数据。
- 状态不可读时按有活动工作处理；应急强制终止必须有明确用户授权、影响范围和数据恢复说明。
- 回滚必须恢复完整旧版，不按某个 runtime 临时分流。

### 12.4 常规升级规则

Go 正式上线后，大内核安装/热更新不得默认替换正在运行的小内核。候选小内核可下载到版本化目录，只有通过相同活动检查和授权流程才切换。修改 `dev_hot_sync.py` 或安装流程时必须验证这一隔离边界。

## 13. 完成定义与禁止缩水

只有以下全部成立才称为“小内核 Go 重构完成”：

- [ ] parity manifest 覆盖实际入口和传递依赖，每个已支持行为有通过证据。
- [ ] Go 完整拥有本机/远端运行时、SSH、PTY，不回调旧 Python 小内核补功能。
- [ ] 模型配置、原生控制、附件、历史、外部会话、Connector 和桌面特殊路径全部保留。
- [ ] action/事件/error/epoch/checkpoint 兼容或有同步交付且验证的扩展。
- [ ] 慢订阅者、跨会话锁、原子回放、overflow 重建和命令不确定性均有故障测试。
- [ ] 本机、Debian、WSL 实测通过；缺环境不是通过。
- [ ] App-only restart 保持小内核及其子进程运行。
- [ ] 安装包完整、启动身份可信、凭据不泄漏、无越权路径/命令执行。
- [ ] 性能/长稳测试通过，明确区分小内核耗时和端到端 UI耗时。
- [ ] 一次性切换和整版回滚在隔离环境演练通过。
- [ ] 无未解释的跳过、占位实现、隐藏降级或静默丢帧。
- [ ] 发布审批再次核对权威活动状态，不借本规划获得重启授权。

## 14. 风险与实施前必须关闭的问题

| 风险/待定项 | 处理方式与截止门禁 |
|---|---|
| 目录外 Python 依赖造成低估 | T0 生成传递依赖与能力映射；核心代理不得留到上线后 |
| native 协议/插件实际版本不同 | 固定验收环境版本，保留原始 fixture；T0/T3 确认 |
| SQLite 持久写成本影响延迟 | T2 基准测试小批次事务、公平性与同步策略；不以静默放松持久性达标 |
| Windows ConPTY/Job 和 SSH 句柄无法迁移 | T3/T4 原生测试；维护窗口切换，不承诺热移交 |
| 远端 helper 可执行环境/架构差异 | T0 清点，T4 固定构建、签名或散列校验和回滚 |
| 旧 App 严格解析新增协议字段 | 差分验证；必要更新随完整版本一起发布 |
| 小内核优化后页面仍慢 | 单独指标定位；关键实时可见性作为全栈验收，不把所有页面重构塞入范围 |
| 工作区并行开发造成基线漂移 | 不覆盖他人修改；T0 固定清单，发布前重新扫描入口差异 |
| 原生 runtime 崩溃后的不可接管状态 | 显式恢复状态和用户提示，不自动重发命令 |

**最终原则：开发可以分步骤，生产能力不能分批缺失。全部实现与验收完成后，一次性切换；不满足条件就继续使用完整旧小内核。**
