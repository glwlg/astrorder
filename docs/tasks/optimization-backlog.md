# 星序 · 待优化清单

> 状态：living backlog。做完一项把状态改成 `done`，不要另开平行文档。
> 更新：2026-09-11（桌面 Sidecar 首轮落地后 + 移动端侧栏梳理）

优先级：`P0` 立刻做 · `P1` 下一迭代 · `P2` 有空再做  
状态：`open` / `doing` / `done`

---

## 0. 当前产品断层（先看这个）

桌面已经长出一套 **Sidecar 工作台**（文件树 / 终端 / 浏览器 / Git Diff / 侧边聊天 / 决策状态机）。移动端是 **完全独立的壳**（`MobileWorkspace`），**没有接入 Sidecar**。

所以用户在手机上感觉「侧边栏乱七八糟」，本质不是桌面 Sidecar 被缩放到了手机上，而是：

1. 移动端把 **会话导航抽屉** 当成了唯一侧栏，里面塞了项目折叠、筛选、搜索、置顶、新建、删项目、删会话、Agent 过滤。
2. 抽屉列表仍会露出大量低质量标题（UUID、`Astrorder 远程会话`、Codex 审批评估原文、空会话）。
3. 桌面工作台能力（看文件、看 Diff、看终端、侧边聊天、决策图）在手机上 **全部缺失**；附件/代码只能在对话气泡里将就看。
4. 输入条把批准模式、模型、更多菜单、附件、语音、发送挤在一行，和抽屉一样密度失控。

移动端优化原则：**抽屉只负责选会话；工作台用底部 Sheet / 全屏卡片，一次只做一件事。不要把桌面 Sidecar 的多 Tab 原样塞进 390px。**

---

## 1. P0 — 会话列表卫生（桌面轨 + 移动抽屉共用）

这些规则必须在 `sessionRailModel` / `buildProjectGroups` 一层做完，桌面左侧轨和 `MobileSessionDrawer` 一起干净。

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| S1 | 过滤 ephemeral / smoke / 测试会话 | open | 冒烟测试已手工删 37 条。代码侧：`native_kind`、标题含 `native connector smoke` / `冒烟测试`、创建时 `ephemeral: true` 的会话 **永不进轨**。脚本 `scripts/verify_native_hermes_local.py` 不得再写持久会话。 |
| S2 | 过滤子代理与审批评估会话 | open | `native_kind === 'subagent'` 已过滤。仍会露出 `The following is the Codex agent history whose request action you are assessing.`、`你是独立复审 subagent` 等。按标题/来源再藏一层。 |
| S3 | 折叠重复占位标题 | open | 多条 `Astrorder 远程会话`、UUID 当标题（`01a08f…`）、`Untitled session`、Hermes 时间戳 id（`20260911_143914_a75faf`）。空标题显示「未命名」，UUID 标题显示工作区名+相对时间。 |
| S4 | 定时任务会话默认折叠 | open | `OV 记忆+知识库 LLM 每晚整理` 已从轨过滤。确认移动抽屉同样走 `buildProjectGroups`，不要另写一份列表。 |
| S5 | 批量清理入口 | open | 桌面/移动都要能「清理测试会话」「清理未命名」；走现有 `ConfirmPopover`，禁止 `window.confirm`。 |

相关文件：`frontend/src/components/sessionRailModel.ts`、`MobileSessionDrawer.tsx`、`SessionRail.tsx`

---

## 2. P0 — 移动端侧栏 / 抽屉重构

目标：左滑抽屉 = 选会话。其它全部滚出抽屉。

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| M1 | 抽屉信息架构砍掉一半 | open | 现状一层里同时有：Agent 过滤、搜索、全部/未读/开放中/置顶/24h、项目折叠、每项目「新建+删除项目」、会话行（标题+相对时间+置顶+删除）。改为：**搜索 + 项目分组 + 会话行**。筛选收到抽屉顶一个「筛选」小按钮，默认「全部」。删除项目不要和「新建会话」并排常驻。 |
| M2 | 会话行只保留主操作 | open | 一行三个按钮（进会话 / 置顶 / 删除）在拇指区会误触。默认只显示标题+时间+运行点；置顶/删除放长按菜单（已有 `MobileMessageMenu` 模式可复用）。 |
| M3 | 默认只展开当前项目 | open | 现在每个项目默认展开前 5 条，astrorder 一个项目就能把屏占满。默认折叠非当前项目；当前项目展开选中会话附近。 |
| M4 | 工作台从抽屉拆出去 | open | 连接管理、运行状态、模型、队列已经是独立 sheet，不要再从抽屉里绕。抽屉 header 只留标题+关闭。 |
| M5 | 移动端工件查看（Sidecar 的手机形态） | open | **不要**把桌面多 Tab Sidecar 塞进抽屉。方案：对话里点文件/Diff/图 → 底部全屏 Sheet，一次一个工件；顶部小条可切「文件 / Diff / 浏览器」。终端、决策图作为第二期。入口可以是会话头「⋯」里的「工作区」。 |
| M6 | 输入条减肥 | open | `.m-composer-tools` 当前：附件、批准模式、模型胶囊、会话操作菜单、清空、语音、发送。改为：附件 · 输入 · 语音/发送。模型+批准收到「⋯」或点模型胶囊弹出的 sheet（已有 `models` sheet，把批准模式并进去）。 |
| M7 | 手势契约保持 | open | 改 UI 不得破坏 `astrorder-mobile-workspace` skill：左缘开抽屉、抽屉内竖滑不关、斜滑切开放会话、`navigate replace`、禁止 `window.confirm`。改完跑 `mobileGestures` / `MobileWorkspace` 测试 + `e2e/mobile-isolated.mjs`。 |

相关文件：`MobileWorkspace.tsx`、`MobileSessionDrawer.tsx`、`mobile.css`、`mobilePolish.css`、`mobileGestures.ts`

---

## 3. P0 — 把已上线的桌面 Sidecar 做完整

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| D1 | 侧边聊天改为真 fork | open | 现状：本地 snapshot + 不存在的 `/api/v1/system/agent-query` 兜底。应对齐 Codex：`thread/start` + `ephemeral`，打开瞬间 fork 已完成历史，**不进会话列表**，空闲数小时 TTL 删除。禁止订阅主会话 live 流。 |
| D2 | Ctrl+P 做成 Quick Open | open | 现状绑定的是打开文件树。应对齐 Codex：模糊搜工作区文件，回车用 Monaco 打开。文件树改绑 Ctrl+Shift+E 或欢迎卡片「文件」。 |
| D3 | 决策状态机做成真 DAG | open | 现状是消息时间线。应消费 `tasks` + tool `call_id` 画父子边（规划→工具→观察→重试），节点可跳到文件/终端。 |
| D4 | Git 栏事件化 | open | 15s 轮询改成保存/终端/焦点时刷新。点文件直接 split diff。 |
| D5 | Sidecar 插件崩溃隔离 | open | ChatPage 已给 SidecarHost 套 Error Boundary。补「重开这个 Tab」、欢迎菜单不被错误页挡住。 |

相关文件：`SideChatViewer.tsx`、`ChatPage.tsx`、`GitStatusBar.tsx`、`AgentGraphViewer.tsx`、`sidecarStore.ts`

---

## 4. P1 — 桌面工作台与稳定性

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| D6 | 分屏预设 | open | 聊天\|文件、聊天\|Diff、聊天\|终端 一键布局，写入 `sessionMemories`。 |
| D7 | 内置浏览器鉴权页 | open | 代理剥 CSP 对登录/验证码仍弱。需要 cookie 透传或「系统浏览器打开并回传」。 |
| D8 | 热重启不断连 | open | `restore()` 已改本机优先。SSH 发现仍可能拖慢；顶栏应显示「重连中」而不是整页 `Connector is not connected`。 |
| D9 | 模型选择器密度 | open | Codex 胶囊已有，弹层搜索/思考强度滑条再压一档，避免「模型暂不可读」时空列表。 |
| D10 | 查看器按需加载 | open | 主包 ~2.1MB。mermaid / xterm / monaco / three / drawio 改动态 `import()`。 |

---

## 5. P1 — 移动端能力补齐（在 M1–M7 之后）

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| M8 | 移动端文件预览 | open | Markdown/附件路径点开 → Sheet 里复用 Monaco/图片/PDF，不新开桌面 Sidecar。 |
| M9 | 移动端 Git 摘要 | open | 输入条上方一条「master · +n −m」，点开 Sheet 文件列表；不做完整 IDE diff。 |
| M10 | 真机手势与键盘 | open | 桌面 Chromium 不等于 iPhone。麦克风、PWA 安装、键盘顶起遮挡输入条、刘海 safe-area 必须真机过。见 `docs/tasks/mobile-parity.md`。 |
| M11 | 队列/outbox 文案 | open | 「排队待发」在 lease/steer 场景仍会误伤。与 skill 中 `canDispatch` / `session.steer` 对齐。 |

---

## 6. P2 — 下一档插件（有空再做）

| ID | 项 | 状态 |
|---|---|---|
| P1 | SQLite / DuckDB 表浏览 | open |
| P2 | `.http` 微型 Postman | open |
| P3 | Excel / CSV 交互表 | open |
| P4 | `.env` 打码编辑 | open |
| P5 | 音频波形预览 | open |

不要在 P0/P1 完成前开这些。

---

## 7. 明确已完成（避免重复立项）

- 桌面 Sidecar 宿主、拖拽宽度、多 Tab、会话记忆
- 空白欢迎菜单（文件 / 侧边聊天 / 决策状态机 / 浏览器 / 终端）
- 浏览器地址栏 + 反代去 CSP + 唤起系统浏览器
- Git 分支切换/新建 + Diff 文件树（树/列表）+ 复用 DiffViewer
- 侧边聊天独立 Tab（逻辑仍是快照，见 D1）
- 决策状态机插件注册与快捷键 Ctrl+Alt+G（展示仍是时间线，见 D3）
- 全局快捷键 capture 拦截 Ctrl+P / Ctrl+T / Ctrl+\` / Ctrl+Alt+S
- 本机 Agent 启动恢复优先于 SSH discover
- 冒烟测试会话一次性清理（37 条）
- 移动端独立壳、左缘开抽屉、斜滑切会话、禁止原生 confirm

---

## 8. 建议下一轮开工顺序

1. **S1–S3 + M1–M3**：列表一干净，手机抽屉立刻不乱。  
2. **M4–M6**：抽屉和工作台、输入条分家。  
3. **D1 + D2**：桌面侧边聊天真 fork、Ctrl+P Quick Open。  
4. **M5 / M8**：手机上第一次能正经看文件，而不是把桌面 Sidecar 缩进去。
