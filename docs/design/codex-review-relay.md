# Codex Review Relay

## 目标

在星序控制面实现一条低上下文污染的固定流程：开发会话 A 完成修改后，星序触发独立审查会话 B 的 Codex 原生 Review；星序只提取 B 本轮新增的原生 `::code-comment{...}`，原样回填到 A 的输入框草稿，等待用户确认发送。

A、B 不通过普通消息互相协作，也不把对方会话历史、记忆、项目说明或审查模板注入给对方。

## 非目标

- 不重新设计 Codex Review 提示词或审查结果协议。
- 不把 `code-comment` 转换成自定义审查模板。
- 不默认自动发送回 A；第一版只写入 A 的输入框草稿。
- 不让审查会话自动修改、提交或推送代码。
- 不使用群聊作为审查流水线的消息中继。
- 不因本功能重启 Session Daemon；部署时遵守 AGENTS.md 的小内核重启约束。

## 用户流程

1. 用户在开发会话 A 的菜单中选择“请求 Codex 审查”。
2. 用户选择或使用已绑定的审查会话 B。
3. 星序在控制面校验 A、B 的 agent、工作区、仓库分支和代码快照是否一致。
4. 星序记录 B 当前消息游标，复用星序现有 `/review` 菜单「审查未提交的更改」选项的提交内容，不附加 A 的上下文。
5. 星序等待 B 的审查轮次结束，读取从游标之后产生的消息。
6. 星序提取这些新增消息中的 `::code-comment{...}`，保留原始字符串和顺序。
7. 星序把提取结果合并到 A 的 draft 文本；A 当前输入框已有文本时追加一个空行，不覆盖用户内容。
8. UI 展示“审查结果已回填”，用户确认后按普通发送流程提交给 A。

## 控制面数据

控制面绑定现在持久化到 App Server 的 `review_relay_bindings` 表；浏览器只保留自动触发的本轮标记。会话内容中不写入对端 key。

```text
review_run_id       唯一审查轮次
source_agent_id     A 的 agent
source_session_id   A 的 session
review_agent_id     B 的 agent
review_session_id   B 的 session
workspace           触发时的工作区
branch              触发时的分支（可选）
snapshot            触发时的 HEAD/工作区变更指纹（可选）
message_cursor      B 触发前的最后消息游标
status              validating/reviewing/forwarding/draft_ready/empty/failed
comment_count       本轮转移的 code-comment 数量
created_at          创建时间
completed_at        完成时间（可选）
error               面向用户的失败原因（可选）
```

旧轮次结果不得覆盖新轮次草稿。回填前必须确认目标 A 仍是同一会话，且 review run 尚未完成；重复事件通过 `review_run_id` 和 comment 原文去重。

## 事件和结果识别

- 触发命令与星序现有 `/review` →「审查未提交的更改」选择一致：提交 `请检查我未提交的更改`，不追加自定义审查模板；不要与直接向后端发送 `/review` 的 `review/start` 路径混淆。
- 只接收触发游标之后的 B 消息。
- 只识别 `::code-comment{...}` 原生标记；普通文本、思考、工具日志和摘要不转移。
- 复用现有 `extractCodeComments` 的语法规则，但 relay 必须保留原始标记文本，不能使用 UI 的展示 Markdown 替代。
- Review 轮次失败、取消、超时或没有评论时，不能伪造结果；状态分别为 failed、empty 或待重试。
- 不能只以 B 的 idle 状态判断完成，必须结合本轮提交的 command/turn 完成状态。

## 工作区校验

校验只发生在星序控制面，不注入 A/B：

- A、B 必须仍然存在且可控；
- A、B 工作区路径必须一致；
- 如果可取得，仓库根目录、分支和 HEAD 必须一致；
- 工作区校验失败时不触发 Review；
- 审查开始后快照仅用于防止旧结果回填新修改，不阻止 Codex 自己处理工作区变化。

## 实现分层

### 后端

1. 提供 review relay 的创建、状态读取和完成回填所需的最小 API/服务层。
2. 复用现有聊天命令提交接口和 `/review` 菜单所选「审查未提交的更改」动作，不新增后端协议或审查模板。
3. 在 App Server 控制面关联 A/B 和游标；不通过 `sessions_send` 向 A 发送内容。
4. 复用消息事件投影，保存 B 的增量消息和完成状态。
5. 明确审查会话只读：relay 不生成写文件、提交或推送命令。

### 前端

1. 在 A 的会话菜单提供“请求 Codex 审查”。
2. 提供 B 的绑定/选择入口和运行状态。
3. 显示 validating、reviewing、forwarding、draft-ready、empty、failed。
4. 使用现有 draft store 回填 A；不直接调用发送接口。
5. 继续复用现有 code-comment 渲染器。

## 失败与回滚

- B 不存在或工作区不一致：不创建 Review，保留 A 草稿不变。
- 审查请求提交失败：状态 failed，可重新发起新轮次。
- B 在完成前断线：保留 run 状态和游标，不重复自动发送；用户可重试。
- 没有 code-comment：状态 empty，不向 A 写入空文本。
- A 草稿在回填前发生变化：追加结果，不覆盖现有文本。
- 回填失败：保留 relay 结果，允许用户再次执行“回填到输入框”。
- 不删除 B 的审查消息，不修改 A/B 的历史。

## 第一阶段开发任务

- [x] 固化设计文档和边界。
- [x] 抽取可复用的原生 code-comment 增量提取函数及测试。
- [x] 将 A/B 绑定从浏览器 localStorage 迁移到 App Server 持久化 API。
- [x] 将 review run、基线消息 ID、状态、错误和原始 code-comment 持久化到 App Server。
- [x] 在发送审查请求前校验 repository root、branch、HEAD 和工作区变更指纹。
- [x] 页面重新打开会话时恢复服务端仍处于进行中的 review run。
- [x] 增加 review relay 的前端状态模型和持久化绑定。
- [x] 在 A 的会话操作区增加显式“请求 Codex 审查”入口。
- [x] 调用已有 `/review` 菜单的「审查未提交的更改」提交内容，记录 B 的消息游标。
- [x] 监听 B 的完成结果并把原始 code-comment 回填 A draft。
- [x] 增加重复回填、空结果、工作区不一致、草稿合并测试。
- [ ] 运行定向测试、构建和完整验收；未通过完整验收前不部署生产前端。

## 验收标准

- B 收到的内容与星序已有「审查未提交的更改」选项完全相同，没有额外提示或 A 的会话历史。
- A、B 的 transcript 中不出现对方会话标识。
- 多条 `code-comment` 原文、字段、顺序均保留。
- 普通审查文本不会被回填。
- A 已有草稿不会被覆盖。
- 同一 review run 重复事件不会重复追加。
- Review 失败和空结果可见且不会污染 A 草稿。
- 相关测试通过，且没有未经授权的 Session Daemon 重启。
