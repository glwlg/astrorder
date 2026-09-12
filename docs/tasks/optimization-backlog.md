# 星序 · 待优化清单

> 状态：living backlog。做完一项把状态改成 `done`，不要另开平行文档。
> 更新：2026-09-11（S1–S3 / M1–M3 / M6 已落地；M2 补原生长按屏蔽）

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
| S1 | 过滤 ephemeral / smoke / 测试会话 | done | `isHiddenRailSession`：`native_kind` 为 `smoke`/`subagent`、`ephemeral: true`、标题含 `native connector smoke` / `冒烟测试` 永不进轨。本机 Hermes 冒烟创建路径仍可能写出「Astrorder 本机联调」，后续再标 ephemeral。 |
| S2 | 过滤子代理与审批评估会话 | done | 额外按标题藏 `whose request action you are assessing`、`独立复审`、`subagent`。普通 `The following is the Codex agent history` 仍保留。 |
| S3 | 折叠重复占位标题 | done | `displaySessionTitle`：空标题 / Untitled / UUID / Hermes 时间戳 / `Astrorder 远程会话` 显示工作区名，否则「未命名」。桌面轨与移动抽屉共用。相对时间仍在行上。 |
| S4 | 定时任务会话默认折叠 | done | 仍由 `buildProjectGroups` 过滤；移动抽屉不再另写一份列表。 |
| S5 | 批量清理入口 | open | 桌面/移动都要能「清理测试会话」「清理未命名」；走现有 `ConfirmPopover`，禁止 `window.confirm`。 |

相关文件：`frontend/src/components/sessionRailModel.ts`、`MobileSessionDrawer.tsx`、`SessionRail.tsx`

---

## 2. P0 — 移动端侧栏 / 抽屉重构

目标：左滑抽屉 = 选会话。其它全部滚出抽屉。

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| M1 | 抽屉信息架构砍掉一半 | done | 默认只剩搜索 + 项目分组 + 会话行。Agent 过滤和全部/未读/开放中/置顶/24h 收到顶栏「筛选」。新建会话在 header；删除项目进项目 ⋯ 菜单。 |
| M2 | 会话行只保留主操作 | done | 行上只留标题+相对时间+运行弧。置顶/删除改为长按或右键菜单。已屏蔽浏览器原生长按：CSS `-webkit-touch-callout`/`user-select:none`，`suppressNativeHold` 捕获 `contextmenu` 且不 stopPropagation。 |
| M3 | 默认只展开当前项目 | done | 含当前会话的项目展开，其余折叠。超过 5 条仍可「展开显示」。 |
| M4 | 工作台从抽屉拆出去 | open | 连接管理、运行状态、模型、队列已经是独立 sheet。header 仍有筛选/新建，可再收。 |
| M5 | 移动端工件查看（Sidecar 的手机形态） | done | `MobileArtifactSheet.tsx`：全屏底部 Sheet，点附件/markdown 文件链接打开。按扩展名分发：图片直显、Markdown 渲染、代码/文本等宽字体、不支持类型给下载链接。`MobileMarkdown` 加 `onFileClick`/`onImageClick`，`MobileTranscript` 附件链接改调 `onFile`。 |
| M6 | 输入条减肥 | done | 输入条只留附件 · 输入 · 语音 · 发送。模型芯片在会话头，点开 `models` sheet；批准模式以 `ApprovalModeControl variant="panel"` 并进该 sheet。会话操作菜单在会话头 ⋯。 |
| M7 | 手势契约保持 | done | 本轮未改手势实现；`mobileGestures` / `MobileWorkspace` 抽屉开合、竖滑不关、删除确认仍走 `ConfirmPopover`。 |

相关文件：`MobileWorkspace.tsx`、`MobileSessionDrawer.tsx`、`mobile.css`、`mobilePolish.css`、`mobileGestures.ts`

---

## 3. P0 — 把已上线的桌面 Sidecar 做完整

| ID | 项 | 状态 | 说明 |
|---|---|---|---|
| D1 | 侧边聊天改为真 fork | doing | UI 复用 `ChatComposer`、`Transcript`，父历史不显示、草稿/发送隔离。列表泄漏修复：补齐后端 ephemeral 字段、两条入库路径及 Codex 通知投影；临时性不被原生同步清除。前端共享列表选择器排除临时会话，兼容旧版精确 `[侧边聊天]` 标记；已静态发布。222 项前端测试、43 项后端相关测试、浅/深色 Chromium 桌面/移动隔离用例通过（含自动改名、关闭、刷新及计数）。后端升级暂未发布：线上当前会话运行中，未强停；详情见 `docs/reports/sidechat-visibility.md`。真实原生上下文继承、Hermes fork、关闭/TTL 清理与 Tab 切换保留仍待验收，D1 不标完成。 |
| D2 | Ctrl+P 做成 Quick Open | done | `QuickOpen.tsx`：Ctrl+P 打开模糊搜索，回车用注册表匹配的 Viewer（默认 Monaco）打开。文件树改绑 Ctrl+Shift+E。后端新增 `/api/v1/files/search`（本机+SSH 文件名搜索，跳过缓存目录）。 |
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
| M8 | 移动端文件预览 | done | 并入 M5 `MobileArtifactSheet`：Markdown 用 `MobileMarkdown` 渲染、图片直显、文本/代码等宽展示、不支持类型给下载链接。不新开桌面 Sidecar。 |
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
- 侧边聊天独立 Tab，共用主会话输入框和消息渲染；原生 fork 生命周期待验收，见 D1
- 决策状态机插件注册与快捷键 Ctrl+Alt+G（展示仍是时间线，见 D3）
- 全局快捷键 capture 拦截 Ctrl+P / Ctrl+T / Ctrl+\` / Ctrl+Alt+S
- 本机 Agent 启动恢复优先于 SSH discover
- 冒烟测试会话一次性清理（37 条）
- 移动端独立壳、左缘开抽屉、斜滑切会话、禁止原生 confirm
- 会话轨卫生：隐藏 smoke/ephemeral/评估 subagent；占位标题显示工作区名
- 移动抽屉瘦身：筛选折叠、会话行长按菜单、默认只展开当前项目
- 移动端屏蔽浏览器原生长按（callout / 系统菜单），自定义长按菜单可稳定打开
- 移动输入条减肥：附件 · 输入 · 语音/发送；模型+审批进会话头/模型 sheet
- 全局屏蔽浏览器原生长按（触摸触发的 contextmenu 统一拦截，桌面右键不受影响）
- Ctrl+P Quick Open：模糊搜工作区文件回车打开；文件树改 Ctrl+Shift+E

---

## 8. 建议下一轮开工顺序

1. **D3 / D4**：决策图 DAG 化、Git 事件化。
2. **S5**：批量清理未命名 / 测试会话入口。
