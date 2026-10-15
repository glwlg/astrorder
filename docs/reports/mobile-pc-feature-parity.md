# 移动端与 PC 操作功能对齐验收

验证日期：2026-10-14。分支：`master`。本报告记录当前源码及 `frontend/dist/` 的隔离验收；随后已按用户“改好了部署一下吧”的授权完成本机前端与桌面客户端部署，现场证据见文末。

## 已交付功能

| 范围 | 当前行为 | 验证证据 |
| --- | --- | --- |
| 会话输入锁 | 复用 `SessionComposerGate`；锁定时隐藏输入操作并保留队列，可重试原生同步或创建聊天分叉；只读时禁止快捷发送、目标及模型等修改 | 组件测试；手机浏览器锁定与重试场景 |
| 目标 | 通过 Agent 原有命令创建；无目标时不显示常驻创建／管理入口；已有目标可编辑、删除、暂停和恢复，沿用 PC `GoalManager` 和会话归属接口；显示 Hermes 恢复重置轮次的原生语义 | 目标组件测试；手机生命周期和桌面／手机空目标浏览器场景；[后续空目标修复验收](native-goal-empty-state.md) |
| 任务与子代理 | 展示待办、计划、工具与子代理；可打开详情、查看结果和日志、跳转子会话；确认后按子会话身份停止，缺少子会话时不退回父会话 | 组件测试；手机浏览器子代理导航与停止请求身份断言 |
| 模型与用量 | 显示原生用量来源、上下文及缓存命中信息；模型、思考强度和审批模式沿用会话控制接口 | 组件测试；隔离手机浏览器请求及状态验证 |
| 待发队列 | 支持立即引导、编辑、精确失败重发、删除和结果核对；即时发送与离线发送均先持久化文字、原始文件、引用及 command ID，再上传／提交；回执不确定时保留原 ID，刷新核对 | 队列与移动组件测试；浏览器离线刷新、单次上传／提交和回执丢失后不重发断言 |
| 存储失败恢复 | 队列存储失败时恢复原会话草稿、附件、会话引用和引用内容；在线发送同时移除未提交的乐观消息，未调用上传／提交 | 先失败后通过的在线与离线组件回归，检查草稿和传输调用次数 |
| 队列自动发送 | 成功后刷新精确 agent/session 的 commands 与 messages，缺少即时事件时聊天仍及时更新 | 组件 query key 断言；断网带图引用刷新并重连的浏览器场景 |
| 唤醒与重连 | CONNECTING／OPEN 期间保留唯一事件连接；重连延续游标，替换或停止后旧回调不再影响状态和游标 | 旧回调、重叠唤醒组件回归；浏览器断网、重连和游标断言 |
| 消息触摸操作 | 长按打开锚定菜单，可复制 Markdown 原文、选择原消息渲染文字、引用；选中消息放开原生文字选择，点外部清理；触摸后焦点回到消息也可用 Escape 关闭菜单 | 先失败后通过的组件与浏览器回归；CDP touch 输入和原消息选区截图 |
| 队列触摸与布局 | 减少动态效果时更新卡片不重叠；长队列独立纵向滚动，保留输入框；触摸取消仅清理拖拽状态 | 先失败后通过的组件与浏览器回归；320×640 浏览器触摸滚动及零发送断言 |
| 文件工作区 | 聊天文件链接进入完整工作区，可搜索、查看、编辑、保存，使用 Git 分支操作与终端；关闭工作区和跨会话切换保留未保存草稿 | 工作区组件测试；手机浏览器编辑、关闭重开、跨会话与保存场景 |
| 隐藏查看器 | 隐藏时停止全局保存快捷键、终端聚焦与尺寸同步；重新显示后恢复活动与终端后端尺寸 | 图表、Monaco、终端组件测试；浏览器隐藏与重显验证 |
| 文件保存身份 | 保存始终采用工件原会话与连接；本地工件缺少连接时保持本地，不借用当前选中的 SSH 连接 | 新增本地草稿切换远程会话的回归，先失败后通过 |
| 文件读取鉴权 | 本站请求携带应用鉴权；外部绝对地址和协议相对地址不自动附加应用凭据或 cookies | 新增同源／跨源回归，先失败后通过 |
| 终端 | WebSocket 按 agent/session 定位，接收二进制输出；清理旧连接回调，隐藏时不调整尺寸，重显同步后端 | 终端组件测试；隔离 WebSocket 手机浏览器场景 |

移动端保留独立组件树、底部抽屉和会话切换手势。操作复用 PC 的共享组件及原生接口。

## 最新验证

以下最终结果覆盖本轮即时发送持久化、存储失败恢复、队列自动发送刷新、事件连接保护、消息文字选择和 Escape 修正：

- 全量 Vitest：**473/473 通过，0 失败，success=true，退出码 0**；已从 JSON 结果解析核对。
- TypeScript：`tsc -b`，退出码 **0**。
- 生产构建：`vite build`，退出码 **0**，已更新 `frontend/dist/`，包含 PWA 构建产物。
- 手机浏览器：**13/13 场景通过，0 失败，退出码 0**；运行当前生产构建，使用独立 HTTP/WebSocket 夹具，覆盖 390px 与 320px 宽度及 320×640 长队列触摸滚动。脚本声明场景数与采集结果数均为 13，名称无重复且完全匹配。
- 浏览器执行了 CDP `touchStart/touchMove/touchEnd`，验证长按复制／原消息文字选择／引用、Escape／外部点击关闭、斜滑切会话和边缘抽屉开关；跨会话草稿及引用保留，触摸导航产生的提交和上传均为 0。
- 离线图片和引用刷新后仍存在于 IndexedDB；重连后仅上传、提交各一次。即时纯图片发送丢失回执后保留原 command ID，刷新核对没有再次上传或提交；用户随后主动发送同名图片仍产生独立命令和第二条消息。
- `git -c core.whitespace=cr-at-eol diff --check -- frontend` 与浏览器脚本语法检查：退出码 **0**。
- 本轮 10 个文件范围 lint：**11 条 React/Hook 等警告、0 错误，退出码 0**；警告均位于 `MobileWorkspace.tsx`。检查范围包含事件流、移动工作区、队列、消息／手势及相应测试和浏览器脚本。上轮 27 条警告来自不同检查范围，不据此声称警告已减少或清零。
- Vitest 日志保留一条 JSDOM `Window.scrollTo()` 未实现提示；测试结果仍为全部通过。

实际命令使用仓库工具链：`.tools/wails/node-v26.11.1-win-x64/node.exe`。主要执行入口为 `node_modules/vitest/vitest.mjs run`、`node_modules/typescript/bin/tsc -b`、`node_modules/vite/bin/vite.js build` 和 `e2e/mobile-feature-parity.mjs`。

最终证据目录：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/`。该目录属于临时缓存，可能按清理策略过期；本报告保留已取得的结果和范围。

- 测试：`mobile-resilience-delivery-suite.json`、`mobile-resilience-delivery-suite.log`。
- 类型与构建：`mobile-resilience-final-types.log`、`mobile-resilience-final-build.log`。
- 浏览器：`mobile-resilience-delivery-browser-green/results.json`、`mobile-resilience-delivery-browser-green.log`，以及同目录截图、队列几何、附件回执与触摸导航 JSON。
- 范围 lint：`mobile-resilience-final-scoped-lint.log`。
- 文字选择与 Escape 失败复现：`mobile-resilience-touch-selection-red.log`、`mobile-resilience-selection-red.json`、`mobile-resilience-escape-red.log`；修正后定向组件结果分别为 `mobile-resilience-selection-green.json`、`mobile-resilience-escape-green.log`。
- 存储与自动发送刷新失败复现：`mobile-resilience-storage-red.log`、`mobile-resilience-bubble-red.log`、`mobile-resilience-drain-red.log`；对应通过日志仍保留。
- 上轮队列、鉴权和保存归属证据：`mobile-parity-delivery-suite.json`、`mobile-parity-delivery-browser/results.json`、`mobile-parity-auth-red.log`、`mobile-parity-auth-green.json`、`mobile-parity-save-scope-red.log`、`mobile-parity-save-scope-green.log`。

此前失败证据仍保留；本次最终通过结果覆盖了对应修正，未以旧通过通知代替当前验证。完整浏览器首次续验中的标题断言存在动画竞态，已改为同时等待唯一标题与正确文本；复制／引用核对 Markdown 原文，文字选择核对渲染文本。图片鉴权夹具补齐服务端已有 session cookie 行为后通过，没有将夹具遗漏认定为生产图片缺陷。

## 验证边界与交付状态

浏览器夹具返回原生协议形状的数据，用于验证真实前端交互、HTTP 请求身份和 WebSocket 行为；它没有运行真实 Agent。真实手机软键盘、麦克风、iOS PWA/Web Push、后台挂起以及真实 Agent 目标和子代理执行链仍需相应设备或隔离原生会话验收。

上述开发与隔离验收阶段没有连接线上会话或读取生产数据库、聊天及凭据，没有部署或重启服务。随后授权的本机部署仅使用生命周期 CLI 的权威状态和静态资源读回，未 commit/push，工作树原有修改保留。

## 本机部署（2026-10-14）

用户授权原文：“改好了部署一下吧”。09:08 完成前端与桌面客户端更新，部署脚本退出码 0。

- 部署前重建 Wails 图标、bindings 和嵌入前端，桌面 Go 全量测试、vet、正式版 `-trimpath` 编译及非交互 `--check` 通过；另行重建启用 PWA 的网页版本，类型检查与构建退出码 0。两种构建分别保留，网页部署包含 `sw-astrorder.js`，桌面嵌入不包含 service worker。
- 重建后完整手机浏览器夹具再次 **13/13 通过，0 失败，退出码 0**。全量组件 **473/473** 复用此前最终结果，本次没有生产源码变更。
- 已安装后端 12 个构件与上次验收 stage 的哈希全部一致，本次未替换后端；网页通过 `astrorderctl sync` 原子同步。部署后安装目录全部 **117 个网页文件**及其 HTTP 实际响应逐一匹配构建哈希，首页缓存策略为 `no-cache`，`/health` 返回 `status=ok, service=astrorder, protocol_version=1`。
- 桌面客户端使用 **114 个嵌入资源文件**重新编译并替换为 `P:/DevApp/Astrorder/Astrorder.exe`，安装后非交互检查确认可找到内核管理 CLI；精确旧客户端 PID 19292 停止，新客户端 PID 20960 已运行。新 EXE SHA-256：`9a3a5e3e8f4909538fe21a21b0bd5310190e747cbfb14a9b5a2b79cb3705de56`。
- 小内核 PID **20120**、创建时间及监听端口 **56572** 保持不变；权威登记数部署前后均为 **146**，仍报告 2 个运行中会话。部署前的 **25 个小内核直属托管进程**逐一核对 PID、创建时间和可执行路径未变。大内核 PID **20380** 及创建时间同样不变，未重启大小内核。
- 旧客户端和网页资源的回滚备份：`C:/Users/luwei/AppData/Local/Astrorder/backups/mobile-parity-20261014-090808/`；备份已逐文件核验，不含生产数据库或凭据。
- 部署收据：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/mobile-deployment-receipt.json`；日志 `mobile-deployment.log`；本次浏览器结果 `mobile-deploy-browser/results.json`；桌面测试／vet／构建证据为 `mobile-deploy-desktop-tests.log`、`mobile-deploy-desktop-vet.log`、`mobile-deploy-desktop-build.log`、`mobile-deploy-release-build.log`；网页类型／构建为 `mobile-deploy-web-types.log`、`mobile-deploy-web-build.log`。

本次直接更新已安装应用，没有重建安装器。未对既有会话发送提示或控制命令，未读取生产聊天；未进行现场 UI 点击或真机／真实 Agent 完整执行链验收，前述设备及原生验收边界继续保留。
