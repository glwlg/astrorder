# 全 Go 迁移进度

当前目标仍为 `docs/design/astrorder-full-go-migration.md` 的完整最终状态。以下记录实际完成范围。用户报告已安装客户端无法启动后，已在备份基础上修复本机安装资产、升级配置引用并恢复 App；整体迁移及最新安装包交付仍未完成。后文基线和阶段表保留为历史快照。

## 最新验收：旧 App 源码退役及 gRPC-only 入口

- 旧 `backend/src`、测试、Python 依赖清单、Grok Python 桥和旧运维脚本已原样移至仓库外 `P:/workspace/glwlg/ai/astrorder-retired-python-20261012`，归档 manifest 记录 27 个顶层移动项，包含原有未提交修改。未删除或读取生产数据库和凭据。
- README、AGENTS 代码归属及 Session Daemon 维护文档已改为 Go 入口。旧后端移走后大小内核全量测试、桌面测试、冻结契约校验通过。
- 小内核可执行入口删除内部 WebSocket listener，gRPC 配置必填。入口及 gRPC 专项 153 测试、vet 通过。protocol 包中旧 Handler/兼容测试仍需清退，不将本批称为内部协议全部删除。
- 从清退后的工作树完成正式 `npm run dist`；包内程序 HTTP/静态前端/鉴权/退出隔离验收通过，结果 scratch `closure-native-http-QkYFIB/result.json`。新 daemon 真实隔离生命周期重启测试通过。
- 最新安装包 199940835 bytes，SHA256 `995b3866d99ba80e23c305e996a0ef2acc21ec71e5f6b1454bd479ea89b7e19c`，12 个程序哈希一致，无 ZIP，替代旧候选。
- 生产实例未更新或重启。实际 NSIS 安装升级、完整三环境三 Agent 行为和桌面交互、protocol 旧实现清退仍未完成。

## 最新验收：迁移代理清退与包内程序验证

- HTTP handler 的迁移代理注入字段、转发分支及专用鉴权函数已删除；无调用者的 migrationproxy 包及 P4 proxy-only 验收脚本已移至工作树外 scratch。未知旧路由回归返回 404；原生连接器测试保留。
- 清退后大内核全量测试通过，HTTP/connector/productregistry 专项 31 测试通过，Go vet 通过；桌面全量测试和新增“不打包旧 Python App 运行时”检查通过。
- 新增显式 opt-in 的真实小内核生命周期测试，独立 DPAPI 配置、状态目录和端口。真实重启后进程创建时间改变，持久 daemon ID 保留，权威会话数为零，结束时隔离实例清理通过。此测试未操作生产 daemon。
- 新增 `scripts/migration/isolated-http-verify.mjs`，替代临时 Python 验收探针。直接运行 unpacked 包内程序，8 个 HTTP 状态/部分正文断言、未认证 preferences 拒绝及 App shutdown 保留 daemon 均通过。结果位于 scratch `closure-native-http-inv4vO/result.json`。
- 正式 `npm run dist` exit 0；最新安装包 200936969 bytes，SHA256 `7f7b0f0427fddcbeaec9dea0a49dd88f21e114721474ad0713667a8bcf92f16e`。12 个暂存与包内程序哈希一致，无 ZIP。替代下文旧候选哈希。
- 仍未完成：旧 Python App、历史运维脚本及内部 IPC 全部清退，NSIS 实际安装升级，完整三环境三 Agent 与桌面交互验收，生产部署。未提交或推送。

## 最新验收：2026-10-12 收尾候选

- 前端只读模型绑定已接入控件与 mutation 保护；此前完整前端回归 98 文件 / 414 测试及生产构建通过。
- 补齐 Jev、runtime launch、daemon restart 和内嵌 API 文档入口；冻结路由分发测试通过。分发覆盖不等于所有操作行为已验收。
- 大内核入口不再建立 Python migration upstream；残留配置明确报错。迁移代理包、旧 Python App 和内部 IPC 尚未全部删除。
- 重启任务保持旧成功状态 `success`，失败包含 `error` 和 `detail`。最新设置及入口回归 13 测试通过，Go vet 通过；此前入口清退后的大内核全量测试通过。
- 最新隔离进程验收：health、OpenAPI、docs、Jev 短输出正文、runtime launch 拒绝、未确认重启拒绝、未知重启任务及 App shutdown 保留 daemon 均通过。脚本已修正两个内核同时启动导致的连接拒绝竞态，先等待 daemon 监听。未执行生产 daemon 重启。
- NSIS 已重新打包；暂存与 unpacked 中 12 个 Go 程序哈希一致，无 ZIP。安装包 `desktop/release/Astrorder Setup 0.1.0.exe`，200977958 bytes，SHA256 `b7a6859e13027cf003c0866755d54ec8b0354520edb75d134fad1882ae511cd6`。这些证明构建与拷贝一致，不代表实际安装/升级已验收。
- 本批尚未生产部署；完整三环境三 Agent 生命周期、实际安装及桌面交互、旧实现清退仍未闭环。整体迁移保持未完成。

## 最新增量：真实 Hermes 基本会话与控制接入

- 当前同步并发覆盖缺陷已闭合：原子会话明确完成后，主会话审查事务内 exact binding 守卫及仅写 `status/updated_at` 的实现，sessions/store 整包 exit 0（3.488s/3.564s），相关 vet exit 0。原回归及新增删除/重绑定在飞回归通过；不再用 RPC 前旧 row 全量 Upsert，不复活已删除行。该结果不包含外部原生历史 activity，两个并发 status 快照的时序仍为 last-write-wins。
- 本连接 live ownership 首批明确完成，主会话正式 TCP GetStatus/runtime/protocol 专项 exit 0（0.226s/0.633s/0.888s）。远端 executable resolver 和 HTTP 接线完成后，主会话为两个旧合成 fixture 注入独立 resolver（Store 不保存任意 settings 字段，不能靠给保存 payload 添加 executable 起效）；connections 整包 exit 0（8.078s）及 vet exit 0。真实远端登录和 daemon 工厂链路未计入该结果，后者另派原 session 集成验证。
- live ownership 最终复核另发现确定性重绑定 RED：`TestAcceptanceLiveSnapshotDoesNotPairOldStateWithReboundIdentity` 在 Snapshot 屏障期间通过正式 close/spawn 将同 ID、同 kind 重新绑定到新 connection、running，返回却为新 identity + 旧 idle state（exit 1）。`session_owner_rebind_acceptance_test.go` 保留；交原 owner session 修复原 binding 相等守卫，首批 GREEN 不覆盖该缺陷。
- 使用已安装 Go 二进制的独立副本，在随机 loopback 端口、私有 App/daemon 数据和新 Hermes home 中实际验证；未修改用户 Hermes 默认配置、生产数据库或已运行大小内核。真实 Hermes 通过 Go HTTP → gRPC 创建会话，原生模型读回为 `magpie / group/luozhi / max`。
- 实际发送一次工具验证任务：Agent 调用 terminal，读取私有工作区内随机 challenge 并写入 answer，精确文件读回匹配；产品 `/commands` 返回 completed，`/messages` 返回含验证串的最终助手回复。没有合成模型回复。该结果覆盖基本发送、工具执行、完成及历史读回，不覆盖全部工具 transcript 展示或前端实时交互。
- 隔离 App-only stop/start 后 daemon PID `41752` 与 epoch/进程身份保持一致，原助手消息 ID 和内容可再次读回。测试会话 `20261010_071015_3fecd4` 通过正式删除接口清理并核对不存在；App 和 daemon 均确认 stopped，权威活动会话为 0，两监听端口可重新绑定。
- 实际 runner exit 0：Hermes scratch `full-go-real-hermes-flow.js`；完整结果 `astrorder-real-hermes-Jvcylb/result.json`。测试 App SHA256 为 `9148a02e20f054b014ebaedb8d179a43cb6fa6a38abdd45539e264c2666dfb05`，daemon SHA256 为 `8d0b82f79804a41e889068df0735de85597bda151e4d5f39bec90fd299472c4e`；这是已安装版本基线，不包含正在开发的最新 ownership/Codex/Grok 改动。
- 进一步真实 headless Chromium 走查同版本编译 SPA：在实际聊天输入框点击发送，真实 Agent 读取/写入第二份随机 challenge，最终命令 completed；浏览器接收 `message.upsert` 与 `command.upsert(completed)`，DOM 命中确切助手消息 ID 且只有一个。刷新和隔离 App-only restart 后均保留同一条最终回复，pageerror 为 0。runner exit 0，9 项基本流程断言通过，结果 `astrorder-real-hermes-aWtuD6/result.json`，截图 `real-chat-live.png` / `real-chat-restarted.png`。测试会话 `20261010_071744_c358ed` 已删除并核对不存在，测试大小内核 stopped、端口可重新绑定。
- UI 走查同时保留未覆盖缺口：`/api/v1/presence`、`/api/v1/sessions/{id}/sync`、`/api/v1/review-relay/runs` 返回 404；一次模型读取 502，重启截图的模型控件仍显示读取中。当前源码的 sessions 路由表虽声明 presence/sync，但 Handler 未派发它们，presence/user-activity handler 仍返回固定空数组；review relay 尚无完整 Go handler。基本流程断言成功不将 T10/T13 或全 UI 标为通过。私有工作区不是 Git 仓库，`git/status` 422 单列为该 fixture 环境限制。
- `group/luozhi --reasoning max` Codex 开发子会话已明确完成纯 initialize 控制通道首批，主会话复跑 `TestControlOnly*`、`TestCodexRuntimeControlAttachOverOfficialGRPC` 与 Codex vet 均 exit 0。新增正式 `ListModels` 验收却 exit 1：控制 ID 被 `requireSession` 当作原生 thread 身份检查并拒绝，`TestAcceptanceCodexControlSupportsOfficialModelCatalog` 保留，已交原子会话继续修复；首批 GREEN 不等同完整控制通道可用。
- 本机真实 `codex-cli 0.161.0` 在新 `CODEX_HOME`、隐藏 stdio 子进程中执行 initialize → initialized → model/list → thread/list，实际模型数 8、线程数 0，未调用 thread/start/resume 或 turn/start。关闭 stdin 后原生进程 exit 0，reader 已退出。结果 `astrorder-native-codex-control-2oofkqzh/result.json`，runner `astrorder-native-codex-control.py` exit 0；这证明原生只读初始化行为，不替代仍在修复的 Go ListModels 消费链路。
- Codex catalog 修复子会话已明确开发完成：控制绑定在 `Adapter.Command` 中单独允许 `session.models`，通过现有 live transport 执行 `model/list`，其他 native-session actions 明确拒绝。主会话复跑 control/official catalog/拒绝 actions 专项与 vet 均 exit 0；真正的 Codex CLI → 当前 Go adapter → 正式 TCP gRPC 完成 attach、重复复用、返回 8 模型、空 thread/list、DisconnectRuntime、重新接入和读模型。结果 `astrorder-real-codex-grpc-run-821133857/result.json`，runner exit 0、daemon closed、native bindings 0、监听端口重新绑定成功；精确测试工作区内无残留 Codex 进程。未改现存服务或提交推理任务。
- 整包验收保留新失败：`TestControlOnlyCommandRejectsNativeSessionActions` 偶发把延迟消费的合法 `initialized` 通知计入 rejected actions 新增请求；codex 全包 exit 1，定向 count=40 复现 4 次同失败。原测试在 Spawn 返回后立即记录 baseline，fixture 尚未消费通知，已交原 `group/luozhi max` 子会话增加明确初始化消费同步、保留零 native mutation 断言；尚未将整批标为验收完成。该失败是测试同步边界，不能用已有真实 gRPC GREEN 抹去。
- 上述测试同步缺陷已闭合，Codex 控制接入/模型目录批次限定验收通过：原子会话明确 `DEVELOPMENT_COMPLETE`，只改 `runtime_control_test.go`，在 fixture 记录 `initialized` 后关闭 channel barrier，测试取请求 baseline 前按 context 等待；原 len 比较、拒绝 action、零 thread/turn 和 idle 断言完整保留，没有固定 sleep。主会话独立复跑原失败用例 count=100 exit 0（0.238s），Codex 整包 exit 0（2.156s），三项正式 gRPC 专项 exit 0（0.998s），vet/gofmt 通过。旧 RED 记录仍保留，Windows race 未覆盖；此前真实 Codex CLI → Go → TCP gRPC 证据仍属于纯控制/只读范围，不代表实际 Codex 对话、所有 Agent 环境或全迁移完成。结果 `luozhi-codex-control-test-synchronization-result.json/md`，主会话限定验收 `luozhi-codex-control-catalog-main-acceptance.json`。
- 本连接 live ownership 合同与 Grok 纯控制接入由互不双写的 `group/luozhi max` 子会话开发，主会话等待明确完成确认后验收。最新 NSIS、全环境原生矩阵、统一产品验收及旧 Python App/内部 WS 清退仍未完成。
- 新增独立 `group/luozhi max` 子会话 `20261010_072938_b455a6` 开发 sessions 的 sync/presence/user-activity 正式派发和可信元数据，不写 owner/Codex/Grok 范围。连接归属与 Grok 原子会话因大量重复源码调查、无实现，已确认旧进程退出后保留原 session 续跑收窄实施任务；恢复进程分别为 `proc_ae5d56aad6d9`、`proc_9cfd8d5bd4cf`，不是开发完成证据。
- 本机真实 Grok 1.0.50 在独立 Grok home、工作区和 leader socket 上仅执行 `initialize`；返回 protocolVersion=1，未调用 session/new/load/prompt。runner exit 0，关闭 stdin 后原生进程 exit 0，reader 退出，精确测试工作区内无残留 Grok 进程。结果 `astrorder-native-grok-control-gt8ltrz5/result.json`。这是原生协议证据，Go Grok 控制接入子会话尚未完成，不能替代正式 Go 消费链路验收。
- 会话刷新首轮 94 次工具调用、无源码修改，已停止并核对无残留；同 session 续跑收窄实施任务 `proc_cb8f57b26992`，开始写独立回归和 Store 元数据读取。另派独立审查转发后端迁移子会话 `20261010_080526_ee60a3`（`proc_534e34cd1f59`），仅正式 HTTP/持久状态和对应 Go 文件/Git 快照，均使用 `group/luozhi max`；没有完成报告前不验收。
- 审查转发首轮亦持续 62 次调查无源码修改，停止并核无残留后同 session 续跑 `proc_d442b7e6246f`；现已开始正式路由、bindings/runs CRUD 和 Go 快照实现，尚无最终完成报告。远端 executable 首批仅三字段透传 GREEN，后续 66 次工具调用仍未实现路径发现，已停止并收窄原 session 的实施任务；保持未完成，不将 constructor 单测视为真实远端接入。
- 远端 executable 目标接线有新 RED：`TestAcceptanceRemoteNativeTargetPreservesExecutablePaths` exit 1，现有 `runtimeTarget` 丢失三个契约已有 native executable 字段，Codex/Grok 工厂却要求绝对 POSIX 路径。独立续跑 Codex 原 session（`proc_0a1a85a9812b`）修复只读 SSH 路径发现、App HTTP → typed gRPC 目标接线；不写 owner 的 environments、Grok adapter 或 review relay 范围。真实用户远端登录仍未验证，alias/options/custom SSH 当前拒绝边界保留。
- 此前模型读取 502 的隔离探针已执行，暂未复现：精确已安装基线副本，新 Hermes home/工作区/loopback 端口，实际创建空原生会话但未提交推理；真实编译 SPA 同时读取，App-only restart 前后各 12 组并发 model/models/messages，共 72 个读请求全部成功。runner exit 0，结果 `astrorder-model-read-probe-l8GMaX/result.json`，daemon 身份保持，测试会话删除、测试大小内核 stopped、端口释放。既有 404 和非 Git fixture 的 422 仍保留；空会话探针未覆盖已完成真实 turn 的模型竞态，不将历史 502 标为已修复或完整 UI 通过。
- 模型身份差异保留：上述 HTTP 模型读回首次 provider=`magpie`，之后 23 次为 `custom`。绕过 Go 的原生 Hermes gateway 对照，增加跨异步 agent build 的 0.5s 间隔后同样复现：create/前两次 resume 为 magpie，之后八次 resume 为 custom，模型仍 group/luozhi、reasoning=max。结果 `astrorder-native-hermes-model-7fjnzrzj/result.json`，原生 session.close 确认、进程 exit 0；本轮没有修改用户 Hermes 或通过 App 保存值掩盖原生差异。该差异属于原生行为证据，App model setter/readback 的兼容处理仍需后续闭合。
- sync/presence 子会话已明确开发完成，主会话初步复跑 sessions/store 包及 vet exit 0（3.680s/3.429s）。新增确定性并发验收仍 RED：`TestAcceptanceSyncPreservesConcurrentSessionMetadata` exit 1，GetStatus 在飞时用户修改 title/workspace 后，旧 row 的 UpsertSession 覆盖新值；已交原 session 限定修复只更新状态的事务边界（`proc_2872f607696e`）。App 投影元数据范围仍不覆盖外部原生历史，整批暂未验收。
- Grok 原子会话明确开发完成，主会话 control/官方 gRPC 专项、Grok 整包和 vet exit 0；真实 Grok CLI → 当前 Go adapter → TCP gRPC 完成接入、复用、实际 2 模型目录、exact live owner、断开和重连。结果 `astrorder-real-grok-grpc-run-2543602267/result.json` exit 0，端口释放、精确工作区无残留 native 进程；关闭后的 Adapter.Snapshot 仍有一条封闭记录，不将其称为 bindings 已清空。新增正式 `TestAcceptanceGrokDisconnectRejectsUnconfirmedNativeExit` exit 1：模拟 SSH transport 明确报告退出未确认，Shutdown 吞 close 错误仍返回 disconnected/COMPLETED。已交回原 session 修复清理错误传播和不确定归属保留，Grok 整批暂未验收。
- 审查转发后端八条路由已由子会话明确开发完成，主会话初步复跑正式 httpapi/targetfs 包与 vet exit 0（3.622s/3.728s）。新增真实临时 Git 路径验收三条均 exit 1：合法 `version..notes.txt` 被当成遍历路径、工作区位于仓库子目录时未跟踪文件拼错根路径、未跟踪符号链接目标改变未影响指纹。`snapshot_path_acceptance_test.go` 保留 RED，交原 session 修复，尚未验收该批；快照输出仅事后检查长度也需改为接收期间有界。
- 独立开发版入口 `desktop/start-go-dev.cmd` 和 `.runtime/dev-bin` 三个二进制仍存在，当前 dev App/daemon 权威状态均 stopped。其摘要与已安装真实 Hermes 基线不同，未将它们宣称为本轮最新已验收交付。owner 子会话整包记录另有 lifecycle 偶发退出身份错误（`A device attached to the system is not functioning`），主会话定向原用例 count=20 exit 0，暂未复现，原失败保留。

## 最新增量（2026-10-10 下午）：三个 RED 修复复核与开发版交付

- 审查快照三 RED 已由原 session（`20261010_080526_ee60a3`）明确完成修复：合法双点名文件名按段校验、`ls-files --full-name` 仓库根相对路径 + within 门禁、符号链接哈希 link text 且不打开目标、输出硬上限取消子进程、WSL 连接不再误查 SSH 行。主会话复跑 `go test ./internal/targetfs ./internal/reviewrelay ./internal/httpapi -count=1` exit 0（10.009s/0.676s/4.435s），vet exit 0。报告 `luozhi-review-relay-snapshot-fix-result.json`（DEVELOPMENT_COMPLETE）。快照三 RED 与本批不验收的边界随之解除；审查转发整批仍待最终验收记录。
- owner 重绑定 RED 修复已落盘且主会话复跑通过：`TestAcceptanceLiveSnapshotDoesNotPairOldStateWithReboundIdentity` exit 0（0.199s）。`session_owner_snapshot.go` 现在在 closing 锁内以值比较（`sameReadIdentity`，nil 与值均需精确一致）丢弃被 release/re-bind 竞态的整行，不再把旧 generation 的 state 拼新 identity。原 session 被外部中断于 generation 证明与补测试收尾阶段，已续跑 `proc_3274112f491a` 完成报告；主会话尚未收到其最终报告。
- Grok 退出未确认的错误成功 RED 修复已落盘且主会话复跑通过：`TestAcceptanceGrokDisconnectRejectsUnconfirmedNativeExit` exit 0（0.802s）；`go test ./internal/runtime/grok` exit 0（6.022s）。Shutdown 现在仅在确认 close 后清 map、失败保留 binding 并 `errors.Join` 传播；SSH Wait 的 nil error + `!ConfirmedRemoteExit` 合成失败。原 session 被外部中断于补测试收尾，已续跑 `proc_1562cb299e3d`；主会话尚未收到其最终报告。
- 全树一致性：`app-server-go` 全包 `go test ./... -count=1` exit 0（含 lifecycle 45.3s、targetfs 17.5s、store 6.9s）；`session-daemon-go` 全包 exit 0（含 grok 14.6s、hermes 17.4s、protocol 14.1s）。前端 `vitest run` 97 files / 407 tests 全过（111.45s），`tsc -b` + `vite build` exit 0，`frontend/dist` 已更新。
- remote 集成验证（正式 TCP gRPC → multiplex → factory → native adapter）原 worker 被中断于写测试前（36 次调用全为只读 grounding、零落盘、无残留），已续跑 `proc_1cba75186d30`；未完成，不得把 remote 目标接线 GREEN 等同 daemon 消费者集成验收。
- 独立 Go 开发版已重建并启动：`.runtime/dev-bin` 三件套由最新源码构建（10-10 修复全部包含），daemon PID 69852 / daemon_id `063f20f8d2aee113dfc4423257d8c79a` / 端口 62367 / 3 个持久会话，App PID 71140 / 端口 62366 / healthy。`/health`、`/chat`、bootstrap 均 200；electron 窗口 `ready:true` 打开 `http://127.0.0.1:62366`。已安装实例（App 47484 / daemon 20500 / daemon_id `86ca8e5f112af6041f05bf2d6ef78c6a`）未受影响，未重启、未替换；本批未触碰任何生产数据。
- 仍未完成：remote 消费者集成、完整 NSIS 最新候选、统一回归与集中独立审查、旧 Python App/内部 WS 清退、provider 身份漂移兼容、历史 502 定位、外部原生 activity、真实远端登录。

## 最新增量（2026-10-10 傍晚）：presence 时间戳缺陷修复与两个收尾批次验收

- presence 503 真实缺陷已修复：`messages.created_at` 重复投影（无 `authoritative_created_at` 标志）时，旧实现用 `fmt.Sprint(existing["created_at"])` 把驱动扫出的 `time.Time` 以 Go 默认布局（`2026-10-09 10:14:34.027938 +0000 UTC`）回写 DATETIME 列；presence 解析 `MAX(created_at)` 失败导致整个端点 503（dev 库实测 4 行污染，仅 `messages.created_at`）。修复：`store/messages.go` 保留分支改用 `databaseTime()` 规范化（不可解析时保留入参，绝不回写垃圾）；`store/codec.go` `parseTime` 容忍遗留 `-0700 MST` 布局。TDD：新增 `store/presence_time_format_acceptance_test.go` 两用例（重投影保真 / 遗留格式读容忍），修复前 RED、修复后 GREEN；store 整包 ok、vet 通过。
- 开发版已带修复重新部署：重建 app 后旧进程因 exe 被原地替换（Windows 把运行中镜像改名为 `astrorder-app.exe~`）进入 `conflict`（identity 不匹配、按设计拒绝操作）；经 FILETIME+端口确证归属后按生命周期同款认证协议优雅关闭旧进程、清除 `~` 镜像、启动新构建。新 App PID `41904`、`identity_verified:true`、端口 62366 owned；daemon PID `69852` 未动。
- 开发版端点复验（正确方法与参数）：`/api/v1/presence` 200（遗留行被容忍）、`/api/v1/user-activity?agent_id=…` 200、`POST /api/v1/sessions/{id}/sync?agent_id=…` 200（sync 为 POST 且必须带 agent_id；此前记录的 404/422 为探测方法错误）、`GET /api/v1/sessions/{id}/model?agent_id=…` 200（`group/luozhi`/`max`）、`GET /api/v1/review-relay/runs?source_agent_id=…&source_session_id=…` 200。
- owner 重绑定收尾批次验收：`proc_3274112f491a` exit 0、报告 DEVELOPMENT_COMPLETE；实现为 generation 计数（bind 分配递增 token、close/unbind 删除）+ 指针/值比较纵深防御，新增同值 rebind 与 all-nil identity rebind 两个 RED 并转 GREEN。主会话独立复跑 3 个验收用例全 PASS（0.203s）。
- Grok 清理收尾批次验收：`proc_1562cb299e3d` exit 0、报告 DEVELOPMENT_COMPLETE；Shutdown 传播未确认清理错误、保留 binding、拒绝替换；SSH Wait nil-error+未确认合成失败；新增 nil-error 未确认用例。主会话独立复跑 2 个验收用例全 PASS（0.893s）。
- 全树一致性复跑：`app-server-go` 全包 `go test ./... -count=1` exit 0（store 9.088s、targetfs 20.453s、lifecycle 47.114s、httpapi 11.714s）；`session-daemon-go` 全包 exit 0（grpcapi 18.122s、grok 9.323s、hermes 11.823s）。
- remote 消费者集成 worker（`proc_1cba75186d30`）仍在运行，尚未落盘测试或完成报告，不计入本批结果。

## 最新增量（2026-10-10 夜）：网关差异对比白屏修复与热替换

- 用户报告点击"网关差异对比"后星序白屏（主界面正常）。日志证据：`POST /api/v1/model-sync/preview` 返回 200/1065B，请求成功；拉取真实响应体对比前端代码定位根因。
- 根因：Go 迁移 parity 回归——预览响应 `model_diff.removed` 为空时序列化为 JSON `null`（Go nil slice），而退役 Python 实现（`sorted(set 差集)`）恒为 `[]`；前端 `UsageGatewaySettingsCard.tsx` 对 `diff.removed.length` 取 length 抛 TypeError，React 整树崩溃白屏。同批修复同类 nil→null 风险（`jobs[].targets`、analytics 的 providers/accounts/reports、sync 的 targets/results/changed/reloaded）。
- 修复（TDD）：`modelgateway` 包内全部空集合改为 `[]T{}` 初始化；新增 `diff.go` 统一 diff 渲染（difflib 语义、`--- 当前/`+`+++ 同步后/` 头、@@ 范围、api_key 值脱敏、400 行上限），补上 `changes[].diff` 字段（此前缺失导致"查看底层配置文件 Diff 详情"展开为空）；前端 `UsageGatewaySettingsCard.tsx`/`client.ts` 加 `|| []` 纵深防御与可空类型。新增 Go 回归 `TestModelGatewayPreviewSerializesEmptyDiffAsArrays`、`TestUnifiedDiffText*`（修复前 RED、修复后 GREEN）与前端回归 `UsageGatewaySettingsCard.test.tsx` 两用例（null 数组不崩 / 正常渲染移除与 diff 折叠）。
- 验证：`go test ./internal/modelgateway/ -count=1` exit 0；前端 `vitest run` 98 files / 409 tests 全过（`--no-experimental-webstorage`，Node 26 原生 webstorage 遮蔽 jsdom localStorage 属环境问题，与本修复无关）；`tsc -b` + `vite build` exit 0；全量 `build-go.sh` exit 0（53 包 ok、0 FAIL）。
- 热替换：候选 `candidate-20261010T033756Z-IkzUrU`，仅 app/ctl 变化（sessiond 哈希一致，小内核未动）。按生命周期：备份旧 app/ctl → `service app stop`（仅大内核）→ 替换二进制 + `ctl sync` 同步前端 dist → 刷新 SHA256SUMS/build-manifest → `service app start`（PID 65816、identity_verified:true）。
- 运行验证：`"removed":[]`（修复前为 null）、`changes[0]` 含 `diff`（13,650 字符）、`/health`、SPA index 与新 bundle 均 200、presence 200（小内核 26 会话未受影响）。
- 追加修复（同批第二缺陷）：headless E2E 截图暴露 `最近同步` 摘要显示字面量 `· <nil>`——`jobs.go` 用 `fmt.Sprint(m["error"])` 读持久化 job 缺失键。已改为 `jobString()` 安全读取（缺失/null → 空串），新增回归 `TestListJobsDoesNotLeakNilLiteralForMissingError`（先 RED 后 GREEN）。DB 原始值未污染，仅读路径泄漏。候选 `candidate-20261010T035023Z-01UUot` 二次热替换（仍仅 app/ctl），运行验证 jobs API 无 `<nil>`。
- E2E 全流程验证：真实 headless Chromium 走用户完整点击路径（设置 → 模型与网关 → 勾选 Codex → 预览选中），9/9 检查通过、0 页面错误、预览弹窗正常渲染 11 模型 + 新增模型列表；用户窗口（曾最小化于 -32000）已恢复前台，截图确认界面正常，白屏消除。
- remote 消费者集成批次验收：`proc_1cba75186d30` exit 0、DEVELOPMENT_COMPLETE（此前"仍在运行、不计入"的遗留项闭合）。交付 `session-daemon-go/internal/grpcapi/remote_executable_control_test.go`（766 行新测试，零生产改动）。主会话独立复跑 5 个验收用例 ×2 全 PASS（1.229s）、grpcapi 整包 142 测试 ok、vet/gofmt 通过。链路证据：真实 loopback TCP gRPC → AttachSession(RuntimeControl, typed CODEX_SSH/GROK_SSH) → controlTarget 字符串字段透传 params.ssh_settings → Registry.Spawn → 真实 MultiplexRegistry → 真实 NewRemoteCodex/GrokFactory 准入（绝对 POSIX 路径校验）→ 合成 SSH seam 内初始化一次 → session.models 只读目录 → GetStatus live ownership（exact connection+agent identity）→ DisconnectRuntime scoped release + 重连 fresh initialize + 不释放其他连接 binding。边界保留：SSH transport 为合成 seam（无真实 OpenSSH dial），真实远端主机端到端（`ASTRORDER_TEST_REAL_SSH=1`）未覆盖；alias/options/custom-ssh Unimplemented 边界由测试钉住。

## 已安装客户端启动恢复（此前增量）

- 用户安装位置为 `P:/DevApp/Astrorder`。损坏的 `app.asar` 中 `main.js` 长度元数据比实际 payload 多 2 字节，导致后续条目错位，Electron 建窗前退出；具体造成损坏的写入者尚未证实。已备份并以匹配的完整 EXE/ASAR 恢复窗口。
- 已安装旧配置缺少 Go App/gRPC 字段，并指向旧 WS-only daemon 与旧 Hermes 启动参数。先在权威状态为 stopped 时升级引用与启动参数，保留工作区和数据路径；新 daemon 随后成功启动。通用安装升级识别入口尚未实现，不能将本机修复等同所有旧安装升级兼容。
- 新 App 仍因退役流 checkpoint 被 daemon 返回 `UNKNOWN_SESSION` 而退出。`group/luozhi --reasoning max` 开发子会话 `20261009_223216_d3db16` 已明确确认开发完成：只对当前订阅确切匹配、无保留历史且 epoch 一致的未知流省略下一次只读订阅 cursor，保留数据库 checkpoint 和其他流；其他缺口继续失败，退役 ID 在 snapshot/live 中重现也明确失败。
- 主会话在完成确认后复跑 projection/lifecycle/astrorderctl 测试及 vet，均通过；desktop 全部 18 项测试通过。真实 electron-builder `--win --dir` 构建成功，afterPack 实际打印 `desktopArchiveVerified=true`、9 files、main.js。该构建只验证桌面归档 hook，所带旧 sidecar 不作为最新完整安装候选交付。
- App 权威状态为 stopped 后，备份并替换安装目录内 `astrorder-app.exe`，更新其安装清单摘要，执行 App-only start 成功。App PID `47484`，`/health` 与静态首页均 HTTP 200；既有窗口已进入 `http://127.0.0.1:30001/chat`。真实 renderer 的 auth/session 为 200/authenticated，bootstrap 为 200，工作台 DOM 已挂载。
- 本次 App-only 恢复前后，小内核 PID `20500`、creation_time、daemon_id `86ca8e5f112af6041f05bf2d6ef78c6a`、持久流数量 23 均保持一致。未在此次 App 修复中停止、重启或替换小内核，未删除用户 checkpoint。
- App 修复与权威状态读回结果：Hermes scratch `astrorder-app-startup-repair-_feumoyq/result.json`。安装清单的全部 10 项产物 SHA256/bytes 均匹配，App 当前 SHA256 为 `9148a02e20f054b014ebaedb8d179a43cb6fa6a38abdd45539e264c2666dfb05`。
- 子会话整包测试 exit 1 的 6 个 connections 失败保留，不能计作全 App 通过；该模块由独立开发子会话继续修复，主会话等待开发完成确认后验收。此前环境模块验收已复现失败 rediscovery 保留旧缓存、远端 Codex 已接入仍显示 disconnected，两项均未被单包 GREEN 覆盖。
- 限制保留：真实 Agent send/history/completion 尚未在本次安装恢复中验证；Windows race 缺 gcc 未验证；子会话 stderr 中有 GBK 解码异常。最新 NSIS 整包尚未重建/验收，旧安装包不得作为含全部修复的最新交付。

开发方式已按用户最新要求调整：开发子会话使用精确 `group/luozhi` 与 `--reasoning max`，大型任务 500 轮；已运行会话经停止确认后保留 session_id 续跑。主会话只在开发子会话明确完成后介入其范围的验收。

## 当前增量：F08 远程模型配置与 helper 交付

主会话直接实现；以下为当前工作树的限定验证，后文阶段表和原台账属于较早快照，不代表当前所有模块状态。整体迁移仍未完成。

- 远程 plan 转发拟议文件与 `expected_digests`；远程 apply 保留预期摘要，目标文件变化时拒绝写入。共享 gRPC 操作账本的 completed 重试返回相同摘要且不再次调用 helper。
- 修复 SSH argv 重复 executable 的实际目标错误；SSH 使用 BatchMode，WSL 使用 `--exec` 保留 argv，避免默认 shell 二次展开。输出有界，JSON 保留精确数字并拒绝尾随数据，取消与进程失败不返回成功。
- 原生 Go helper 使用随包 `helpers/linux-amd64` / `helpers/linux-arm64` 资产：本地 SHA256SUMS 校验、stdin 传输、远端校验后发布到用户目录的内容哈希版本路径。daemon 配置支持 `model_config.helper_assets_dir`，默认从 daemon 二进制旁的 helpers 发现。构建脚本与桌面打包 filter 已接通资产与摘要文件。
- Windows `modelconfig`、`configuration` 测试，ModelConfig gRPC/protocol focused 测试及相关 vet 通过；Windows 原生注册表 credential 用例本轮跳过。App `modelgateway` 整包通过。Linux 相关四包 race 通过。
- 真实 Windows gRPC → WSL → 自动安装的 Linux Go helper 完成预览、写入、旧摘要拒绝、completed 无重复执行及实际文件摘要读回；安装 helper 与 bundle SHA 一致，私有 WSL 目录已清理。结果：Hermes scratch `full-go-model-wsl-ktm5r_7l/result.json`。
- 真实本地隔离 SSH 服务使用新生成的私有测试密钥和随机 loopback 端口；原生 SSH 客户端完成平台探测、helper 上传安装、预览、写入及拒绝旧摘要，Linux race 测试通过；结束后确认 owned SSH PID 消失及端口可重新监听。这不是用户现存 SSH 服务器配置验证。
- Bash 和 PowerShell 的实际新增 helper 构建段均产出 Linux amd64 / arm64 二进制，四份产物与各摘要文件逐项匹配，两个脚本对应架构产物一致。结果：Hermes scratch `full-go-model-build-79wk12bp/result.json`。未执行完整发布构建或 NSIS 安装包。
- 新回归位于 `session-daemon-go/internal/modelconfig/remote_helper_test.go`、`remote_install_test.go`、`remote_ssh_integration_test.go`，以及 `internal/grpcapi/model_config_remote_test.go`、`model_config_wsl_test.go`、`internal/configuration/helper_assets_test.go`。旧固定 PowerShell 返回值与全局测试 hook 已由真实 helper 进程及实例 runner 替换。
- 失败保留：先复现远程预览缺变更、旧摘要被忽略、SSH executable 重复；真实 WSL 安装初次失败由 `--` 二次 shell 展开引起，改 `--exec` 后通过；构建摘要初次格式不一致由 Git Bash binary 标记引起，改 `--text` 后通过。Windows scratch 路径规范化导致两条旧 fixture 断言失败，已按规范路径比较修正。完整仓库 diff check 仍有此前桌面文件 CRLF/空白问题；本轮 Go 与构建脚本 scoped diff check 通过。

剩余交付缺口包括：正式安装升级 staging、全产品集成与统一验收、用户真实远端和原生有效配置读回、模型 reload 的完整跨环境验证、旧 Python App 和内部 WS 清退。该增量不将 F08/P8/P9 或整个迁移标为 DONE，也不变更生产配置或重启现有小内核。

## 基线

- HEAD：`ff32f947024b1840b939ebfb8e58e45886d34b47`，分支 `master`。
- 原有未跟踪设计文档、图片与 pytest 临时目录保留。
- 本轮真实基线验证：Go daemon `go test ./... -count=1 -timeout=120s`，275 项通过；Python App `backend/.venv/Scripts/python.exe -m pytest backend/tests -q --tb=short`，476 passed / 9 skipped；frontend Vitest 406 passed（97 files）；frontend build、desktop check 通过。
- Python 集成跳过项不计作真实 Agent/gRPC 验收。当前尚无 gRPC 生产传输。

## 工作状态

| 阶段 | 状态 | 当前范围与退出缺口 |
| --- | --- | --- |
| P0 | IN_PROGRESS，派发保留修复 REVIEWED | 472 个冻结项；typed 默认值、native baseline、证据 v2 和隔离 runner 已落地。每个冻结派发来源的保留检查经 20 项完整测试和独立复审 PASS；命令/候选绑定及 P0/P1 总退出最终审查仍缺 |
| P1 | ISOLATED_VERIFIED，待最终审查 | typed protobuf、35 个 action 集合对照、实际 Go→Python→Go 互操作与确定性生成通过；EventEnvelope 增 native_payload 保留未知字段，P0 冻结 hash 保持原样，P1 最终审查仍缺 |
| P2 | IN_PROGRESS，QueryNative 切片 REVIEWED | DaemonControl/EventStream/OperationQuery/RuntimeQuery 已注册，RuntimeQuery 仅实现 QueryNative。操作查询原范围 PASS、QueryNative 当前范围 PASS；旧 WS 账本/真实 TCP/无重发和查询路径已验证；其余 SessionControl/RuntimeQuery 方法/终端/模型/Connector RPC 与 T01–T08 总门槛仍缺 |
| P3 | NOT_STARTED | Python App 全部跨内核调用尚未迁至 gRPC |
| P4 | REVIEWED（基础过渡形态） | 五个原生 HTTP 方法、完整 170 条显式过渡代理、static/SPA/CORS/auth/WS/流取消；真实旧 App 路由、隔离 Store、编译页面和 App-only 关闭已验证，独立审查 PASS；业务仍由 Python 单写 |
| P5 | IN_PROGRESS | versioned Store、旧库夹具、结构 schema 检查、跨进程迁移锁、opaque Projector 及事务 checkpoint 已实现；生产投影与持久待发布事件、完整单写者交接未完成 |
| P6 | 基础实现，未切归属 | F07 文件/Git/附件/本地与远端目标、偏好领域、记忆客户端基础已落地；偏好 HTTP handler 仅隔离契约测试，尚未挂入 App；路由与表/键域迁移、写后事件读回、Python 清退未完成 |
| P7–P8 | NOT_STARTED（分析/局部基础） | 会话/命令/事件完整接管、MCP、高级协作、浏览器、语音和多环境真实验收未完成 |
| P9 | 基础 REVIEWED，最终依赖未满足 | DPAPI、幂等 provision、独立 App start/status/stop/restart 实际测试通过；六项安全缺陷独立复审 PASS；两个 Go-only 构建入口真实产出五个二进制和 SHA256SUMS；桌面、安装升级、daemon CLI 与 P8 门槛未完成 |
| P10–P11 | NOT_STARTED | 完整产品/安装验收、Python 清退、正式切换和生产读回未进行 |

## 当前核验器

`app-server-go/internal/parity/` 和 `cmd/astrorderctl/` 读取完整冻结清单。最终核验接 `productregistry.Snapshot()`，当前仅 5 个 HTTP + 9 个 Agent CLI 的实际执行定义，其余领域保持未注册失败；结构成功不代表最终成功。证据 v2 强绑定 candidate_revision、command_id、测试路径/符号、固定命令模板、JSON/二进制/输出 SHA256。Windows `verify --isolated` 已实际执行；候选为未提交工作区，明确记录 `vcs.modified=true` 与 `source_freshness_limited=true`。
已加入固定 `go-daemon-package-test` 模板（先 RED unknown command，后 GREEN），拒绝跨 module、路径逃逸、shell 后缀与错误包命令；F03 台账新增已审查操作查询和隔离控制/事件的 partial_implementations，保留父项未完成状态、Python 投影 owner 与所有退出缺口。最新结构核验 472/472、issues 为空；最终核验 exit 1、ok=false，仍有 458 个未注册入口及全部条目未完成、缺最终证据和 legacy 未清退等问题，输出 Hermes scratch `full-go-final-verifier-after-review.json`。不得用基础 runner 或 partial 记录证明全部产品完成。
daemon 命令模板独立审查 `full-go-daemon-evidence-review.json/md` PASS_WITH_LOW_FINDING，14 个 scoped 文件 SHA 匹配后归档；审查进程 exit 1，结论以已解析报告及实际测试为准。LOW 为分片记录未被 Go adapter 解析，当前已用 RED→GREEN 回归修复：在 feature 展开为 entry 前保留 typed 分片，结构/最终核验均检查父入口归属、重复 ID、阶段/状态、归属/legacy presence、报告 hash/PASS 与历史执行 hash/结果绑定。分片禁止 DONE/PRODUCTION_VERIFIED，不改变父状态或最终 Evidence。操作查询原进程结果逐字节归档为 `full-go-p2-operation-process-result.json`；历史源码新鲜度限制保留。该修复独立复审 FAIL，报告 `full-go-partial-ledger-review-fail.json/md` 原样归档：PARTIAL-001/002（MEDIUM）为未知字段静默忽略、执行声明未绑定原始结果，PARTIAL-003/004（LOW）为 feature/partial 全局唯一性和路径清洁度缺口。原 tests/vet/race 的 GREEN 不能覆盖这些实际反例；修复及新独立复审完成前，不标记该分片核验器为 REVIEWED。

PARTIAL-001–004 后续修复 REVIEWED（限定范围）：首个 Flash 工作者 exit 1，仅调查/复现，没有源码修改，不作修复证据；收窄后的 Flash 工作者 exit 0，严格单对象 JSON/未知字段拒绝及父 feature 分组唯一性已落地，协调者补完整历史结果 schema、清除未绑定命令/exit/assertion、分片入口重叠及规范路径验证。各新增缺陷回归先 RED 后 GREEN；原始历史结果 SHA `14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e` 不变。真实 CLI 私有副本 27/27 变异全部拒绝、原台账结构 472/472 exit 0；最终验收仍 exit 1，无 invalid_partial，458 个入口未注册。完整 App tests/vet exit 0，Linux parity/ctl race exit 0（7.449s/1.290s）。结果 Hermes scratch `full-go-partial-ledger-coordinator-fix-result.json`、`full-go-partial-ledger-rereview-after/result.json`，修复 CLI SHA `8a7c85e3758c3b8584009c9ff2c2d9d7853fb555f78e86fc4ac684fb8e63441c`。仅 F03 partial 子块更新，其他台账项和所有原审查报告不变。独立 Flash 复审 `full-go-partial-ledger-rereview.md` scoped PASS，审查者随后实际执行 parity/ctl tests/vet exit 0，10 个源码/台账/历史结果 SHA 与当前一致。JSON/Markdown 审查报告均原样归档为 `full-go-partial-ledger-rereview.json/md`；较早的 `full-go-partial-ledger-rereview-index.json` 为协调者从原 Markdown 提取的辅助索引，来源标识保留。初次复审到工具上限，仅有原文 PASS，未保存报告且未自行执行专项 tests/vet，不单独作为完整审查证据；后续限定补验与写出 Markdown 后完成上述 scoped gate。此结果不完成 P0/P2/F03 或整体迁移。

## 本次实际证据（均为隔离候选）

- 最新 `RuntimeQuery.QueryNative` REVIEWED：17 个闭合 protobuf selector、三个 SSH runtime 路由接既有 Registry.Query；共享准入/30s timeout/模型 reload wake，严格未知字段、native JSON 和 typed 参数冲突检查。Windows daemon 全量 tests/vet/build exit 0；Linux grpcapi/protocol/runtime race exit 0（19.700s/10.730s/1.071s），新增取消释放权威状态断言再以 Linux race count=3 通过（1.856s）。生成 Python 客户端 -> 真实候选 daemon -> 既有 Codex adapter -> 合成本地 stdio 进程六项断言通过，客户端断开重连复用原 PID，无会话/操作新增，owned daemon exit 0、native stdin EOF、端口释放。详见 `full-go-p2-native-query-isolated.md` 与原样 process result；独立 Flash 审查 JSON/MD PASS，11 项源码 SHA 当前一致。首个实现 Flash exit 1 仅调查；首个审查回合 exit 1 给 prose PASS 但未落盘且把限额误写成16 MiB，后续实际补验/重哈希/精确16000000字节修正并保存报告后才计入 REVIEWED。新增仅读功能不完成 P2/F03/整体目标，SSH transport options/aliases/custom SSH executable 和其他 RuntimeQuery RPC 仍返回 Unimplemented。
- QueryNative 注册新增使原 OperationQuery 报告的 `server.go`/`control.go` 两项 SHA 不再匹配，另外15项保持一致。原报告原样保留，台账操作查询切片改为 ISOLATED_VERIFIED + PASS_SCOPED_PREVIOUS_HASHES；这两项新注册由 QueryNative 当前11文件独立审查覆盖。旧 `full-go-p2-incremental.md` 的三服务描述是历史证据，不再作为当前完整注册状态。
- 本轮 F03 新切片/测试/缺口追加改变了 matrix SHA，旧 partial-ledger 复审的台账哈希保持历史原值；核验器源码和历史 operation result 未修改。追加后的实际结构 verifier exit 0、472/472、issues=[]；最终 verifier exit 1、458 unregistered_entrypoint、无 invalid_partial，最终 gate 的缺失证据/legacy/target/状态问题仍保留。App parity/ctl tests/vet exit 0（1.015s/0.651s），当前 QueryNative review/evidence 哈希及11个源码哈希再次匹配，原跨语言 process-result byte-exact。

- 最新完整 App `go test ./... -count=1 -timeout=120s` 与 `go vet ./...` exit 0，包含全部附件/JSON/偏好修复及新增 daemon 测试命令绑定；`astrorderctl` 候选 build 成功。无测试文件的 helper/acceptance 包不计作行为测试通过。Windows parity/ctl focused tests/vet exit 0，Linux race parity/ctl exit 0（5.005s / 1.300s）。
- P0 原 16 项测试通过后，独立复审 FAIL `FGP0-PRESERVATION-001`：删 Hermes reasoning 派发、保留 Grok 时核验误通过。新增回归先 RED，修复以 action + 派发来源路径保留覆盖，仅忽略行号/hash。新实际源码复制夹具首轮 1 failed / 19 passed（缺 Git 历史，提前报旧脚本缺失）；改成本地 no-checkout clone 后目标测试和完整稳定套件 20 passed / 2 warnings（113.48s）通过。独立复审 `full-go-p0-preservation-final-review.json/md` PASS；自身实际源码删除复现退出 1 且指向丢失的 Hermes 路径，复审完整套件 20 passed（112.88s），四个 scoped SHA 已匹配当前源码。冻结清单未重写：canonical `d8670db3fbf19d4c00505696897f065bf13bb53ea1cabae627e03253cd671acc`，raw SHA `c19a3dda9689fa886a17ba21fc29a0a9f46ee82fc8180235f8b719d6106d4ff0`。
- Contracts 20 项测试通过，明确启用重复生成与 Go/Python 子进程互操作；presence、null、超 JS 安全整数、uint64、未知字段、原生 JSON 和终端 bytes 保留。Go-only 生成不解析 Python。
- `scripts/migration/p4_contract_verify.mjs` 在随机 loopback 端口启动真实 Python App 路由（禁用 lifespan/daemon/自动连接/推理）和新 Go App；bootstrap/auth、任务取消、附件写读与 413、持久 replay/live、headless 编译页面、流响应、上传取消、凭据隔离、App-only 退出与 WS/端口释放均通过。记录：Hermes scratch `p4-source-contract-fU97bn/result.json`，candidate SHA `7462c532b2b7b49731ecbd92615ee70d900aee1abd13882aeec4a9a9ca965b8e`。测试事件为显式夹具，不是真实 Agent 推理证据。
- Windows 生命周期修复已通过三次包测试、vet、恶意继承环境下真实候选 start/status/restart/stop；进程/端口清理，独立 sentinel 存活且未收到 daemon 请求。独立基础层复审 PASS 见 `full-go-foundation-final-review.json`；Linux交叉编译不计为目标平台运行。
- Store/preferences Windows tests/vet 与 Linux race 已通过修复后测试；附件 metadata 插入和读回现在同事务，读回失败 rollback，不会让 handler 清理已提交 row 的 blob。事件发布失败保留 row/blob 并返回 500；持久 outbox 仍未实现。
- Bash Go-only build exit 0：Hermes scratch `full-go-build-bash/candidate-20261008T150955Z-OQ4exr`；五个二进制 SHA256SUMS 已逐字节校验。此轮产物早于最新附件事务修复，不作最新候选交付。
- PowerShell Go-only build exit 0：Hermes scratch `full-go-build-powershell/candidate-20261008T151241Z-230f3e82`；五个二进制 SHA256SUMS 已逐字节校验。此轮含附件事务修复，早于后续偏好 HTTP/JSON 转义改动。
- 上述构建包含 contracts lint/generation/test/vet、daemon test/vet、App test/vet。产物仅为基础候选，无安装/部署/全产品完成声明。
- 最新 Bash Go-only build exit 0：Hermes scratch `full-go-reviewed-build/candidate-20261009T005624Z-0oe0a8`，Buf lint/build、确定性 Go 生成、contracts/daemon/App tests/vet 与五个二进制 build 全通过，五项 SHA256SUMS 已逐字节验证。该产物包含 daemon 命令模板，早于后续 partial verifier 修复。其 App 实际 `verify --isolated` exit 0，九项断言、精确 PID 退出/随机端口释放通过；App SHA `e0c8c74b54321100f6e258070b10ae2c161a873af31d22d4cc0a85d35bcd1a16`，结果 Hermes scratch `full-go-reviewed-isolated-20261009`。最新 transitional Go/Python 重复生成与互操作 contracts tests/vet exit 0（1.936s）。
- 最新候选 `astrorder-app-foundation-current.exe` 的 real process lifecycle 与 P4 同源码 harness 再次通过；结果 Hermes scratch `p4-source-contract-7owkVi/result.json`。审查之后的附件双平台路径修复单独记录在 `full-go-foundation-addendum.md`，不扩大旧报告的哈希范围。
- 验收模块 Windows `HideWindow` 位于通用文件导致 Linux 编译失败，已拆分 build-tag helpers；Windows 全 App test/vet 通过。Linux 全 App 初次因空目录 fixture 向上找到真实 Git 仓库失败，增加 fixture discovery ceiling 后全模块 test/vet exit 0。
- 新候选 `astrorder-app-portability-current.exe` 使用 Go CLI 实际隔离验收，九项断言与精确进程退出/端口释放通过；结果 Hermes scratch `full-go-current-isolated-b6e6692bda`，候选及输出/artifact SHA 已独立读回。
- 偏好存量 JSON 保留与完整文档读回修复：原审查 FAIL 和复审发现 PREF-TX-002 均保留；修复后独立审查 `full-go-foundation-addendum-final-review.json/md` PASS_BOUNDED_PREF_TX_002_FIX，Windows tests/vet 与 Linux race exit 0。handler 仍未挂入 App。
- gRPC 最新 Windows 全模块 tests/vet/build 与 Linux 五包 race exit 0；生成 Python 客户端实际 TCP 验证八项断言、精确 owned PID 退出和端口释放通过，详见 `full-go-p2-incremental.md`。操作查询最终独立复审 PASS，报告 `full-go-p2-operation-final-review.json/md` 的全部源文件 SHA 已比对一致；原 FAIL `full-go-p2-operation-review-fail.md` 保留。控制/事件原范围 PASS 在 `full-go-p2-control-review.json/md`，其旧哈希不扩展到后来的注册变化。App OperationQuery 消费者仍未实现。

## 已确认需要处理的边界

- FastAPI 使用 lazy included router；直接 `len(app.routes)` 不能作为入口总数。
- 既有投影与 daemon checkpoint 分开事务；Go 投影必须按 T03 原子提交。
- 当前无 schema version table；升级需校验实际结构与幂等 backfill，不能仅写版本号。
- 发布 provision 中开发者专属用户名、workspace、LAN 地址不能沿用为新安装默认值。
- 旧 daemon parity manifest 来源早于基线；`operation.read`、`session.event`、运行时查询/别名/调用者差异必须进入冻结清单。
- gRPC 生成工具版本固定：buf v1.66.1 / protoc-gen-go v1.36.12 / protoc-gen-go-grpc v1.6.2；生成 Go/Python 绑定已存在，Go-only 构建路径已验证；实际跨内核传输适配仍在实现。

整体迁移尚未完成。每一阶段必须补实际证据、独立审查和缺口，不能将上述开发或源分析当作全功能通过。
