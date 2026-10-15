# 旧轮次消息错位与最终正文回退修复

2026-10-15 16:30 已发布 Wails 前端、浏览器前端和 App Server；未提交、推送或重建安装器。小内核无需改动，未停止或更新。

## 已复现的缺陷与修复

- 历史响应直接写入消息 store，会覆盖请求期间已经收到的完整实时正文。现在每个历史请求保存本地 messageRevision，仅接受未在请求后更新的消息字段；不使用 durable cursor 判断实时新旧，因为 transient 消息的 cursor 为 0。新发起的原生历史查询仍能更新正文，不通过文本长短猜测新旧。
- Codex 原生 history envelope 和实时帧的 turnId 曾在投影时丢失。现在保留为 turn_id，迟到条目按精确轮次插入下一轮前，原生页内顺序继续保持。没有轮次身份的旧客户端内存条目不按正文猜测合并。
- 完成时刷新历史原先依赖 command.upsert 或 native observation Stop。增加 Codex turn/completed 及 session running→idle/error 的刷新，覆盖没有命令回执的原生轮次。

## 验证范围

- 首轮两项前端回归确实失败：完整正文被旧快照覆盖、旧轮次消息追加至新回复后。修复后通过。
- 原生完成刷新与 running→idle 的新增测试先失败后通过。
- Go history/live 投影保留 turn_id 的测试先失败后通过。
- 前端全量 113 文件、562 项通过；TypeScript 通过。定向 oxlint exit 0，仍有既有 ref/render 与 navigate dependency warnings，未宣称零 warning。
- App Go projection、sessions、application 三包完整测试及 vet 通过；Wails 全包测试、canonical desktop build、release GUI build、browser build、App/ctl 构建通过。
- 隔离 Codex 真实 app-server + 本地 Responses SSE fixture 验证 history/live 的 turnId、item.id 字段，不使用真实 LLM，不读取原生用户存储。
- Edge headless 在候选与安装服务上均通过 desktop 1200×900、mobile 390×844：连续两轮、迟到旧条目、持有历史响应期间实时完成、native completion 自动查询、刷新后顺序与单条显示一致，无 pageerror。API/WS 使用隔离 fixture；安装服务仅提供真实静态资源。这不等同截图会话或真实模型的逐条事件验收。

## 正式发布

- 回执：C:/Users/luwei/AppData/Local/hermes/cache/scratch/publish-stream-order/deployment.json。
- worker 退出后 2026-10-15T16:30:47+0800 再验：App PID 7840、desktop PID 20908；小内核 PID 8788 与发布前身份/端口/文件相同，发布前 15 个直属托管进程身份保持。
- 113 个安装静态文件完整文件集、SHA256 及 HTTP 返回字节与候选一致，安装二进制哈希一致，服务 healthy。
- 回滚备份：C:/Users/luwei/AppData/Local/Astrorder/backups/stream-order-20261015-163012。
- 本轮没有读取生产 DB、聊天或凭据，没有向生产 Agent 提交测试消息。截图原轮次的完整事件序列未取得，因此上述为确认的代码缺陷及受控复现，不把截图归结为已证明的单一根因。
