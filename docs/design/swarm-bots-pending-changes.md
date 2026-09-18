# 群星现有代码待修改记录（Bots 群聊专项）

本文档用于记录在 Bots 群聊（多服务器多 Agent 协同协作）开发过程中，发现的需要对群星（Swarm）现有共享模块进行的改动。
为了不与另外正在修改群星会话的工作产生代码冲突，此处先行整理变动点与设计接口，后续统一合并合入。

---

## 1. 待合入改动清单

### 改动点 1：`agent_gateway.py` - `swarm.lock.acquire` 增加群聊麦克风专有资源域
- **目标文件**：`backend/src/astrorder/agent_gateway.py`
- **背景/原因**：群聊同一时刻只允许一个 Agent 持有发言权。
- **拟改动方案**：
  - 在 `swarm.lock` 校验中允许 `group:mic:<group_id>` 模式的命名空间。
  - 支持群会话一键释放：当群内被中断（Kill Switch）时批量释放该群关联的所有资源锁。

### 改动点 2：`agent_gateway.py` - `swarm.milestone.resolve` 增加群聊上下文透传
- **目标文件**：`backend/src/astrorder/agent_gateway.py`
- **背景/原因**：Agent A 完成阶段性任务并移交 `@Agent B` 时，需要在唤醒 `Agent B` 的提示词中带入群名称与本轮接力链序号。
- **拟改动方案**：
  - 在 `_milestone_resolve` 中支持 `group_id` 与 `hop_count` 字段。

---

*（后续在此持续追加）*
