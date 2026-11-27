# 项目操作约束

## 小内核实现边界

- Session Daemon 唯一实现位于 `session-daemon-go/`。会话进程托管、WAL、原生控制和模型配置执行器均在 Go 中维护。
- App 侧 API、事件投影、控制客户端和终端转发位于 `app-server-go/`，通过 gRPC 连接 Session Daemon。
- Go 可执行文件或配置缺失时必须明确报错。旧 Python 实现可追溯到检查点 `603835a`，不在工作树中保留副本。

## 小内核发布

- Agent 负责构建、验证并暂存 `astrorder-sessiond.exe.new` 到已安装的小内核可执行文件同目录，核对哈希及 `update-check` 后交付；暂存不代表上线。
- 用户通过系统托盘“小内核 → 更新小内核”手动激活，Agent 不执行小内核的停止、重启或更新。
- “停止旧内核 → 替换文件 → 启动新内核”由独立控制器完成；星序进程及更新流程不得依赖 Agent 会话或其工具子进程存活。
- 大内核可以独立发布、重启，必须保持小内核及其托管进程持续运行；启停大内核必须通过已安装的 `astrorderctl.exe service app stop|start|restart|status --root <InstallDir>\server --state-dir %LOCALAPPDATA%\Astrorder`，禁止直接裸杀/裸起 `astrorder-app.exe`。
- 发布已安装桌面客户端 `Astrorder.exe` 必须构建正式打包版（`node scripts/desktop-wails.mjs build-packaged` 或 `-X main.defaultPackaged=true`），禁止把开发版 `astrorder-desktop-dev.exe` 覆盖到安装目录；本机更新与验收优先使用带门禁的一键发布脚本 `node scripts/deploy-local.mjs [--frontend] [--desktop] [--app]`，并必须通过 `node scripts/verify-local-release.mjs` 全部 5 项门禁（Web Dist 完整性、正式版 `Astrorder.exe --check` 的 `packaged: true` 与 `%LOCALAPPDATA%\Astrorder`、桌面主窗口存活、`astrorderctl service app status` 健康态、小内核 PID 未变化）。
- 同步 Web / PWA 前端静态资源到已安装目录必须通过完整性门禁（`astrorderctl sync` 或 `node scripts/verify-frontend-dist.mjs --mode browser <dist>`），核验 `index.html`、全部引用 `assets/*` 与 `sw-astrorder.js` 后原子替换，禁止直接拷贝未校验或桌面构建覆盖过的 `frontend/dist`。
