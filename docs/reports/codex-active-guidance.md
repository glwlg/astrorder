# 星序持有的 Codex 活动会话引导发送修复

日期：2026-10-14。分支：master。本轮修改源码、构建候选程序并隔离验收，未部署，未重启或替换运行中的大小内核、客户端，未提交或推送。未读取生产数据库、聊天、凭据，也未向用户的工作会话发送测试内容。

## 问题与修复

用户截图显示 `Native result is unconfirmed: native command outcome is uncertain: codex session is active`，队列呈“未确认/失败”。定位到 Go Codex 适配器的活动状态判断，并在 running、waiting_approval 两种状态复现同一错误；该错误不代表会话归属丢失。

- PC 的“立即引导”与普通发送都进入已有 Send RPC，Codex 适配器此前一律拒绝活动状态。现在由小内核的真实活动 turn ID 决定：活动回合调用原生 `turn/steer`，空闲时调用 `turn/start`；必须核对 steer 响应中的 turnId，缺失或不匹配不报告成功，也不盲目换成 start 重发。保留所有权和精确回合校验。
- 引导等待尚未返回的 start/steer 操作释放，遵守请求取消；上一条输入回执不明时不启动另一回合。后续原生回合事件可解除该不明状态的发送门禁。
- 引导携带 `clientUserMessageId`，原生 userMessage.clientId 仅用于该条回显的 command ID。原任务的 command ID、运行和完成事件归属保持独立；App 投影优先使用精确 command_id。
- 原生确认后，将这条引导的投递标为 completed，同时原任务仍可 running。确认写入 `codex.command_complete` 持久事件，HTTP 回执丢失后可从 WAL 和命令状态核对为已送达，不自动重发。App 不通过引导回执恢复已经结束的活动回合。
- PC/移动共享队列允许用户显式选择的引导越过已确认 accepted/running 回执；自动队列仍等待回合结束。submitting/received/unknown 边界继续阻止后续提交。PC 对确认失败重新生成 ID 后发送；未知结果显示“核对结果”，只读取命令回执。原来失败/未知条目按钮重复调用仅支持 queued 的 flush，实际没有发送。

## 执行证据

证据根目录：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/`，是可能被清理的缓存。所有下列通过结论均有实际运行输出。

- 前端最终全量 **502/502**、0 失败，success=true，退出码 0：`steer-active-final-frontend.json/.log`。包含并行工作新增的测试，数量不应归因于本修复。最早名为 baseline 的运行采集到本轮新增队列 RED 测试，493/494，不能声称它是未改代码的干净基线。
- `tsc -b` 与 PWA `vite build` 退出码 0：`steer-active-types.log`、`steer-active-build.log`；收尾类型复查 `steer-active-delivery-types.log` 退出码 0。
- 当前 dist 加独立 HTTP/WebSocket 夹具的桌面/手机浏览器 **17/17**、0 失败、退出码 0：`steer-active-browser-final-green/results.json/.log`。验证原任务保持 accepted/running、显式引导、确认失败重试使用新 ID、HTTP 丢失回执后核对且不重复提交，兼容先前目标和移动操作场景。截图 `owned-guidance-desktop.png`、`owned-guidance-mobile.png`；手机截图仍包含刻意断开 HTTP 产生的 `Failed to fetch` 提示，不能视为无错误环境截图。
- 定向 Go 缺陷均先 RED 后 GREEN：`steer-active-red-codex.log`、`steer-active-red-queue.json`、`steer-active-red-pc.json`、`steer-active-red-app.log`、`steer-active-red-start-race.log`、`steer-active-red-identity.log`、`steer-active-red-projection.log`、`steer-active-red-uncertainty.log`、`steer-active-red-delivery-{app,native}.log`。对应最终回归见下列通过日志。
- App projection/sessions/store 包通过；加入持久投递事件后 projection/sessions 再次通过：`steer-active-final-app-tests.log`、`steer-active-delivery-app-green.log`。App 全模块 vet 和候选构建通过：`steer-active-delivery-app-vet.log`、`steer-active-delivery-app-build.log`。
- 小内核 Codex/gRPC/protocol 包通过：`steer-active-delivery-daemon-green.log`。全模块 vet、候选构建通过：`steer-active-delivery-daemon-vet.log`、`steer-active-delivery-daemon-build.log`。全模块测试另有 `TestOwnedProcessSurvivesOwnerExit` 失败，输出 `owner did not start the detached process`；单独重跑仍失败：`steer-active-final-daemon-tests.log`、`steer-active-process-isolated.log`。未改独立进程保护来规避测试，也未宣称小内核全模块测试全部通过。
- Codex 与 codex-ssh 真实 Registry、TCP gRPC、持久去重和 WAL 订阅回放通过：`steer-active-grpc-wal.log`。原生 stdio 对端和 SSH 会话 transport 是合成夹具，不是真实 SSH 主机验收。
- 实际安装的 Codex **0.161.0** 进程，在独立 CODEX_HOME/工作区和本机 Responses 夹具中运行：原生 start 后 steer 确认同一 turn、原回合仍运行，模型 HTTP 请求次数保持一次，并完成隔离进程及文件清理。最终 `steer-active-real-codex-acceptance.log` 退出码 0。首次已确认引导但 Windows 临时文件清理失败；清理等待补齐后通过。模型响应是明确的本地测试数据，未调用真实 LLM、生产 API 或用户凭据。
- Windows race 首次 C 编译器路径解析失败，未获得 Windows race 通过证据：`steer-active-windows-race.log`。WSL Linux 私有源码副本中，Codex/gRPC/protocol 全包 `go test -race` 通过：`steer-active-linux-race-final.log`；加入 WAL 断言后再跑该定向竞态测试通过：`steer-active-linux-wal-race.log`。
- 限定 oxlint 0 错误，1 条已有 ChatComposer 导出警告：`steer-active-scoped-lint.log`。gofmt 无差异，CRLF 感知的限定 `git diff --check` 通过。默认空白检查把 CRLF 视为行尾空白的输出不作为产品错误。
- 最终限定只读 Codex 审查 `steer-active-review-final.json`：passed=true、安全/逻辑问题为空、五字段 schema 校验通过。前一次审查未返回 verdict，被精确停止，不计通过。

## 浏览器夹具修正与边界

保留全部早期失败结果。夹具补齐并行新增的 background-processes 接口；未知结果测试暂时隐藏服务端回执，再显式核对，避免轮询提前成功造成按钮消失。普通工具已按并行产品修改离开运行任务列表，断言与当前组件契约对齐；未改该产品逻辑。通知自然关闭前的晚期断言改为新失败事件对照；事件标题同时更新服务端快照；文件快开 Escape 定位到输入，避免无焦点时关闭整个工作区。手机失败提示可能覆盖操作按钮，验收实际关闭提示后重试，没有使用 force click。

本轮没有现场生产 UI 验收、真实 SSH 模型执行或真机验收。候选程序位于 scratch：`steer-active-session-daemon.exe`、`steer-active-astrorder-app.exe`；网页 dist 已构建，Wails 嵌入前端和安装器未重建。安装版本尚未包含此修复。激活小内核修改须遵守 AGENTS.md 的权威状态和活动会话授权规则。
