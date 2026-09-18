# 多服务器跨 Agent 群聊（Bots Group）架构与实现规范

本文档定义星序（Astrorder）中 **多服务器、跨 Agent 群聊协作（Bots Group）** 的端到端技术规范、防死循环机制、数据结构契约与前端交互设计。

---

## 1. 架构目标与设计原则

在多服务器协作环境中，不同宿主机（Windows 本机、本地 WSL、远端 Linux/Debian 等）运行着不同专长或不同品牌的 Agent 进程（如 Codex、Hermes、Grok 等）。
传统的多 Agent 交流若简单采用“全员广播”，各 Agent 会无休止地接话、客套确认、互问互答或死锁，造成巨大的 Token 浪费与逻辑紊乱。

群聊协作的设计原则为：**“复用群星底座、单发言权互斥、定向受控接力、三重闭环熔断”**。

---

## 2. 深度复用：群星（Swarm）现有基础设施

系统完全复用星序现有成熟的分布式协同底座，不重复造轮子：

| 现有群星能力 | 所在模块 | 在 Bots 群聊中的复用方式 |
| :--- | :--- | :--- |
| **机群网关（Machines Dispatch）** | `agent_gateway.py` | 统一基于 `machine_id` 与 `agent_id` 寻址跨机器成员，复用现有的连接、鉴权与进程通信机制。 |
| **分布式资源锁（`swarm.lock`）** | `agent_gateway.py` | 作为**“群麦克风发言权互斥锁”**，同一时刻只允许一个 Agent 处于执行态，杜绝抢话与并行输出。 |
| **里程碑唤醒（`swarm.milestone`）** | `agent_gateway.py` | 作为**“消息接力通道”**，Agent A 交付产物并 `@Agent B` 时，自动通过网关注入提示词并唤醒 Agent B。 |
| **全局共享黑板（`blackboard`）** | `agent_gateway.py` / API | 群内只输出决策与结论，详细构建日志、大段代码及文件产物写入群共享黑板，避免各 Agent 上下文窗口爆炸。 |
| **紧急求援告警（`swarm.sos`）** | `agent_gateway.py` | 遇到死锁、死循环征兆或连续失败时，自动触发求援广播，群聊强制暂停并向人类推送系统通知。 |

---

## 3. 核心防死循环机制（三重熔断锁）

```text
[ 用户输入指令并 @指定Agent 或 @all ]
               │
               ▼
     ┌───────────────────┐
     │  获取群麦克风互斥锁  │
     └─────────┬─────────┘
               ▼
      [ 目标 Agent 思考与执行 ]
               │
       ┌───────┴───────┐
       ▼               ▼
 【未 @ 任何成员】   【明确输出了 @其它Agent】
       │               │
       │         ┌─────┴─────┐
       │         ▼           ▼
       │    [当前步数 ≤ 上限] [当前步数 > 上限]
       │         │           │
       ▼         ▼           ▼
  [立即静默闭嘴] [接力下一位] [强制熔断挂起，提醒人类介入]
  (麦克风交还人类)
```

### 3.1 规则一：默认静默（Silent by Default）
- Agent 回复完成后，调度器自动解析其产出文本。
- **若未显式包含针对群内其它成员的 `@mention`，该 Agent 立即完成本轮次，禁止任何其他 Agent 自动插话、礼貌确认或展开联想**。
- 发言权自动交还给人类用户，群聊进入等待态。

### 3.2 规则二：最大接力跳数硬熔断（Max Hop Limit）
- 人类单次指令所触发的 Agent 间自主接力流转设置最大步数上限（默认 **3 步**，允许用户在 1~5 步之间配置）。
- 例如：`用户 -> @Codex (第1步) -> @Hermes (第2步) -> @Grok (第3步)`。
- 一旦步数达到上限，无论最后一名 Agent 是否输出 `@`，调度器立即截断接力，群聊进入暂停态，并在界面高亮提示“已达单次自动接力上限，请确认是否继续”。

### 3.3 规则三：语义停滞与重复检测（Echo & SOS Guard）
- **重复度指纹**：记录当前接力链上各 Agent 的文本摘要哈希。若检测到不同 Agent 连续给出高度重复、无实质工具动作推进的推诿或客套话，判定为语义停滞。
- **自动调用 SOS 挂起**：调度器自动触发 `swarm.sos.escalate`，将群状态锁定为挂起，并通过 Windows 动作中心 / 系统通知卡片提醒人类指挥官介入仲裁。

---

## 4. 数据模型与协议设计

### 4.1 群实体元数据（`bot_groups`）
群信息存储于数据库与工作区偏好中：
```typescript
interface BotGroup {
  id: string                     // 唯一群标识，如 "group-7f9a12bc"
  name: string                   // 群名称，如 "全栈跨机发布群"
  description?: string           // 群职能说明
  members: BotGroupMember[]      // 绑定的多服务器 Agent 成员
  max_hops: number               // 单次指令最大允许接力步数 (默认 3)
  created_at: string
  updated_at: string
}

interface BotGroupMember {
  machine_id: string             // "local" | "ssh-xxx"
  agent_id: string               // Agent 唯一 ID
  alias?: string                 // 群内别名（如 "架构审查员"、"Linux运维"）
  system_role_prompt?: string    // 针对该群定制的角色 Prompt 指引
}
```

### 4.2 群聊时间线消息（`GroupMessage`）
群内维护一条统一的公开时间线：
```typescript
interface GroupMessage {
  id: string
  group_id: string
  sender_type: 'user' | 'agent'
  sender_id: string              // user 标识或 agent_id
  sender_machine_id?: string
  text: string
  mentions: string[]             // 被显式 @ 的 agent_ids 列表
  hop_count: number              // 当前处于第几跳（人类发起为 0）
  attachments?: string[]
  created_at: string
}
```

### 4.3 提示词与上下文组装协议
当调度器决定将发言权移交给目标 Agent 时，为该 Agent 组装其专属的上下文提示：
```markdown
[群聊协作协议]: 你正在参与星序跨机协作群「全栈跨机发布群」。
群成员列表:
- @Codex (本机 Windows 前端构建)
- @Hermes (远程 Debian Linux 生产部署)
- @Grok (架构与安全审计)

规则约束:
1. 回复需直入主题，汇报结论与执行结果。
2. 若需将后续任务交接给群内其他成员，必须明确输出 @成员名称；若当前任务已完成且无需其他 Agent 动作，请不要 @ 任何人，系统将自动交还给人类指挥官。

[群时间线记录]:
[用户]: 请 @Codex 构建前端产物，完成后交由 @Hermes 部署到线上。
[来自 @Codex]: 构建已完成，产物已打包至 dist/，相关 hash 写入黑板。请 @Hermes 执行拉取。
```

---

## 5. 前端界面与交互体验

### 5.1 侧边栏群聊导航（SessionRail）
- 在侧边栏新增独立的 **【群聊 / Bots】** 分组（与单聊项目并列）。
- 点击组标题旁的 **`+`** 按钮弹出“新建 Agent 群聊”弹窗：
  - 填写群名称与说明。
  - 勾选要拉入群的成员（显示机群来源标签 `[本机]` / `[WSL]` / `[Debian]` 与品牌 Logo）。
  - 设置最大接力步数。

### 5.2 群聊主界面
- **群成员指示栏**：顶部常驻展示所有成员的小头像/Logo，正处于思考/发言中的 Agent 显示脉冲呼吸光环，其余显示静止状态。
- **输入框 `@` 智能补全**：在群聊输入框敲入 `@` 时，下拉展示群内已连接的 Agent 列表（带品牌图标与机器来源），回车即可插入快捷引用。
- **接力状态条与一键终止（Kill Switch）**：
  - 接力进行时，输入框上方显示小胶囊：`流转中：第 2/3 步 [@Hermes 思考中]`。
  - 右侧提供醒目的红色【终止接力】按钮，点击立刻切断接力链并释放群锁。

---

## 6. 实施落地路径

1. **Phase 1：数据模型与群元数据管理**
   - 新增群聊数据持久化（后端路由 `/api/v1/bot-groups`）。
   - 前端增加建群弹窗与成员选择器。
2. **Phase 2：发言权互斥锁与消息中继**
   - 接入 `agent_gateway.py`，实现用户 `@` 触发指定 Agent 并记录群时间线。
   - 实现“无 `@` 立即静默闭嘴”的基础防死循环拦截。
3. **Phase 3：自主接力与环路熔断**
   - 增加 Agent 输出文本的 `@` 解析与自动转发接力。
   - 实现步数上限熔断（Hop-Limit）与重复语义检测。
   - 关联系统通知（Windows Toast / 应用内卡片），接力完毕或熔断时即刻提醒用户。
