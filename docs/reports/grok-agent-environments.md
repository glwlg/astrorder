# Grok Agent 环境归属修复

用户现象：WSL、Debian 新建会话列表缺少 Grok，本机显示三个 Grok。

## 原因与实现

Grok 控制会话结果缺少 connection_id/name/runtime_id/source_id，protocol 生成的 connector.hello 因而没有远程归属。App 投影将 connection_id 写为 NULL，NewSessionDialog 按 local 筛选，两个远程 Agent 出现在本机列表。

- Grok Config 增加 ConnectionID，远程工厂显式填写。创建、重复接入、模型目录读取与快照共享完整控制元数据。
- App 对旧小内核的 Grok hello，只在 Agent ID 与已保存 SSH 连接的规范 ID 精确对应时补齐归属；数据库和 agent.upsert 使用同一修正结果。
- App 启动纠正已经写错的规范远程注册记录，保留 Agent ID、状态、自定义名称与会话关联。无对应保存连接的记录不猜测归属。

## 验证

- 实现前回归复现远程 hello 被当作本机，以及控制结果丢失配置名称；修复后通过。
- App 全量（清理继承的 ASTRORDER_* 环境）1120 通过，10 跳过。首次继承生产配置时有三项配置/生命周期测试失败，清理环境重跑全部通过。
- 小内核全量 634 通过，25 跳过；vet/build 通过。
- 前端 NewSessionDialog 3 项通过，TypeScript 检查通过。
- 安装版真实页面 + 线上 API 验收：astrorder、OpsCore、xiuxian 三个项目弹窗均有三个 Agent，且各自仅一个正确环境的 Grok。仅打开弹窗，未创建会话或发送模型消息。

## 交付

App 已发布，线上 agents 接口本机/WSL/Debian 各一个 Grok，现有三个 Agent ID 和 43 条 Grok 会话关联保持一致。小内核进程身份不变，46 个递归子进程全部保留。

- App SHA-256: `0be7bdac51cb0d23206fca4d4bf8c60b58563a10b6c118d99fb32a89167d1638`
- 小内核候选 SHA-256: `08a18185585ba645eed8613e83115f84188d33ad0445e8f56cf7f27a18cb9b69`

小内核候选已暂存到安装目录 astrorder-sessiond.exe.new，旧候选已备份，哈希与 update-check 通过；未激活。App 的旧事件兼容已使本次列表问题在线生效。小内核源头修复按 AGENTS.md 由用户通过托盘手动激活。

本次验收只证明 Agent 环境归属与新建弹窗列表，未验收 Grok 模型发送、历史等其他业务能力。没有 commit/push。
