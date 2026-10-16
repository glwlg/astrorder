# 新建 Codex 会话空历史误报

## 原因和修复

用户截图同时显示 `Native history read failed` 和后台任务 `native session read failed`。原会话 `01a143d4-7f36-7ca2-8510-9c06e1794543` 的创建成功，但创建初期 history 503、background-processes 502；稍后只读回查均返回 200 空列表。

真实本地和 SSH Codex 可复现：`thread/start` 成功，`thread/read(includeTurns:false)` 成功，但尚未有输入时 `thread/read(includeTurns:true)` 返回原生 -32601 `list_turns is not supported yet`，`thread/items/list` 返回原生 -32601 `thread/items/list is not supported yet`。独立 catalog 对已经写入原生存储的空线程可读取真实空回合；对未写入的线程则可能 `thread not loaded`。因此 App fallback 单独不能覆盖全部新建会话。

App：history 和 background-processes 失败后，通过独立 catalog 使用 `includeTurns:true` 读取，只有身份匹配、确切 turns 数组为空时才返回空列表。缺失/null turns、身份不符、读取失败和非空回合仍保留错误。旧分页不使用空历史替代。

Daemon：在本 transport 的原生 `thread/start` 返回身份匹配的空 turns 时维护内存状态；只在上述精确 -32601 code/message 且当前原生线程元数据确认 idle、身份匹配、transport 正常时返回空结果。不适用于 fork/resume，不存任何消息副本。发送、引导、review、compact、rollback、goal 写入之前撤销状态；原生 turn/item 通知也撤销。核验期间 revision 改变则拒绝空结果，发送回执丢失也不会恢复状态。

## 验证

- App 会话包：142 pass，vet 通过。
- Daemon 全量：632 pass / 25 skip，vet / build 通过。
- 真实隔离验证：本地 Codex、SSH Codex、TCP gRPC + Codex 共 3 pass。每项创建后立即重复读取历史和后台任务，没有 turn/start 或真实模型请求。
- 回归覆盖精确原生错误码、缺失字段、非空和外来身份、分页，以及可能已提交但返回失败的发送之后不复用空状态。
- 本次文件 `git diff --check` 通过；全工作区检查存在无关并发前端文件 CRLF/空白告警，未修改这些文件。

## 发布状态

- App 已独立发布并核对 hash、health，小内核身份及当时的 29 个后代进程保留。单独这一步未修复所有情况，安装后的新建 local-codex 测试仍读到 503；不声称生产完整验收通过。
- 完整 Daemon 候选已暂存至 `P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new`。
- SHA256：`48400aff5e750495005ef04da676cb3921ce089f6a55d0f24e0504fbf4a5ae15`。
- `update-check` 返回 `update_available:true` / `updated:false`。
- 暂存前后运行 daemon PID 3384、creation_time 134366127889661707 相同；当前 live hash 保持 `b317f75298776bfee1519f825eb0a9567b1578c07ab500f56aca58dd6db5959e`；23 个直接子进程全部保留。
- 未停止、重启或更新小内核。根据 AGENTS.md，由用户通过托盘手动激活。激活前完整修复尚未生效，激活后仍需验证安装版新建交互。
- 安装验证留下的无输入测试会话 `local-codex::01a143e0-b6e8-79f0-8ec9-1e63b0387b80` 删除请求超时/502，后续只读确认仍存在；未绕过原生删除修改 SQLite，未自动重发删除。
- 本次没有 commit / push。

回执：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/empty-session-fix/{delivery,staged}.json`，测试日志位于同目录。
