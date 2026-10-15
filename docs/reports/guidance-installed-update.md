# 目标与活动引导修复的安装版交付

日期：2026-10-14。用户授权更新、重启前端壳和大内核；小内核按 AGENTS.md 只暂存，由用户通过托盘手动激活。本次未提交或推送。

此前 `native-goal-empty-state.md` 和 `codex-active-guidance.md` 中的“未部署”描述是各自验收时点的记录。此次已将最新工作区构建的 Wails 客户端、App Server、控制器及浏览器/PWA 静态资源更新到 `P:/DevApp/Astrorder`。

- 无目标会话误报取消、无目标常驻创建入口和前端引导队列修复已包含在已部署前端/App 产物中。
- 活动引导原生 `turn/steer` 路径属于 Session Daemon。本次只暂存新小内核，完整引导修复要等用户手动激活后生效。
- 流光调查本身只修过 scratch 测试脚本，没有新增产品动画修复。用户先前已确认流光恢复，本次不重新开展视觉排查。
- 本次安装版更新并重启完成后，用户现场确认：“现在打开的版本是正常有动画的”。当前新版动画视觉验收完成，不再追加截图或隔离矩阵。

## 当前安装结果

- 客户端 `P:/DevApp/Astrorder/Astrorder.exe`：新 PID 26752，SHA256 `6571696655046a00363ddc9ae9e8506a10828437b8205fca118f72ccb3fe8010`。
- 大内核 `server/bin/astrorder-app.exe`：新 PID 32444，SHA256 `5163a90a20ca67ba6aefdace77de658317c5c7d7f2344b9366a0053099b4c1d7`。
- 控制器 `server/bin/astrorderctl.exe`：SHA256 `c3ecac65ef3b808a8a2c0d2fff85d2cd1ca3e598df96f27688a6032c2e19d0ed`。
- 小内核 `server/bin/astrorder-sessiond.exe`：PID 24508、创建时间、监听端口 56572 和运行文件哈希均保持不变。部署前的 33 个直属子进程全部核对可执行文件与创建时间一致。
- 待更新文件 `server/bin/astrorder-sessiond.exe.new`：SHA256 `3d86130f03fe96f8d9b61aaf2210c72378f461a73a6d34a19cf806c4189394dd`；已核对安装路径文件哈希，`update-check` 返回 `update_available=true`，尚未激活。
- 浏览器/PWA 前端 113 个文件均与构建哈希一致，并逐项读取线上字节比对。桌面嵌入资源不含 PWA worker，浏览器版包含 `sw-astrorder.js`。
- 安装路径客户端 `--check` 返回 `kernelManagement=true`。大内核健康、精确进程身份和监听归属通过，独立客户端启动后多次观察身份稳定；最后再次读取安装状态、可执行文件哈希、健康接口、在线首页及全部原有小内核子进程身份通过。

## 验证范围

- 最新前端 Vitest 523/523，0 失败；TypeScript 与桌面/浏览器构建通过。
- 最新 dist 的隔离桌面/手机 HTTP/WebSocket 场景 17/17，0 失败。包含无目标静默、Agent 命令入口、活动引导、失败重试及未知结果核对；未向用户工作会话发送测试消息。
- App Server 全模块测试通过，vet/build 通过。
- 桌面全模块测试、vet 和正式配置构建通过。
- 小内核 Codex、gRPC、protocol 包测试及全模块 vet/build 通过；此次未宣称小内核全模块测试通过，先前独立进程存活测试失败记录仍保留。
- 初轮在工具进程中运行时，桌面两项独立子进程测试遇到 Access is denied；通过系统独立进程重跑桌面全量测试通过。小内核慢读者测试初轮启动超时，单独重跑和顺序执行相关包均通过，保留原失败日志。
- 首次发布在停止大内核命令返回错误时终止，产品文件尚未替换，恢复旧版客户端/大内核。第二次停止成功，完成替换与全部发布读回。没有将首次尝试计为成功。

本次没有读取生产聊天、数据库或手动提取生产凭据；生命周期控制由原生控制器执行。没有执行小内核停止、重启或更新，没有生成新 NSIS 安装器，也没有重新截图验收流光。

## 证据和回滚

验证及部署证据目录（临时缓存，可能被清理）：
`C:/Users/luwei/AppData/Local/hermes/cache/scratch/guidance-release-20261014-153407`

主要文件：`independent-validation.json`、`frontend-tests.json`、`browser/results.json`、`deployment-attempt-1.json`、`deployment.json`、`final-readback.json`。安装 `build-manifest.json` 和 `SHA256SUMS` 已按实际部署文件更新，明确小内核激活仍待用户操作。

已验证的产品文件回滚目录：
`C:/Users/luwei/AppData/Local/Astrorder/backups/guidance-release-20261014-154347`
