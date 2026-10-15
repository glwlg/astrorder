# 最新源码重新打包及覆盖安装

用户授权：重新打包最新版并重新安装，保留用户数据。前端壳和大内核可重启；小内核遵循 AGENTS.md，仅暂存候选。

## 已完成

- 正式安装包：`P:/workspace/glwlg/ai/astrorder/release/Astrorder-Setup-0.1.0.exe`。
- 安装包 SHA256：`6542b869dbb1d7a6ae472d005e5456fe005f307811b16b8bb3d93348dd7df5cd`。
- 覆盖安装到 `P:/DevApp/Astrorder`，客户端 PID 3504，大内核 PID 33948，健康及监听归属检查通过。
- 小内核 PID 29524、创建时间和端口 56572 保持，安装前记录的 20 个直属托管进程身份保持。
- 小内核候选 SHA256：`a70e85823d131175a3dbf9afd9e00f8e6a36dc5806ce9b49b123a86fb00096c4`；`update-check` 已识别，未激活。
- 113 个安装静态文件逐个核对通过，线上 index、主 JS/CSS 及 service worker 字节核对通过。
- 回滚文件：`C:/Users/luwei/AppData/Local/Astrorder/backups/reinstall-20261014-172734`。未读取生产 DB、聊天或凭据文件。

## 构建修正与验证

- 安装脚本分别构建 Wails 内嵌前端和浏览器 PWA，避免桌面构建覆盖浏览器 worker。
- NSIS 覆盖安装保留现有小内核文件，仅暂存 `.exe.new`；全新安装正常写入小内核。隔离验收验证了全新安装、升级文件身份及候选、独立双内核生命周期、卸载保留用户数据。
- 前端全量 525/525 通过，桌面全量测试通过，最终正式构建中的 contracts、小内核、大内核全量测试及 vet 均通过。
- 首轮缺少协议生成工具，补齐脚本锁定版本。后续一次标题异步测试提前检查，调整等待条件，原断言保留，定向连续 10 次通过。另一次音频生命周期测试遇到退出/文件锁失败；Windows 发布构建改为逐包运行大内核测试，最后全量通过。此前失败日志保留。
- 安装后第一次回执因 PowerShell 5 解码中文 JSON 失败而触发恢复路径；该路径又遇到 EXE 文件锁。随后独立 Python 控制器调用按 UTF-8 字节解析，恢复 App/客户端并重新核对全部安装产物、服务和托管进程，最终 `ok: true`。

## 动画验收边界

重新构建的客户端 SHA256 为 `4a1474c99314b09067c75d8f2fb9063c6f4a1d403f5cc2b58f842bfdb7832625`，与重装前相同。此次未改动画代码，重装完成不代表动画根因已解决，需用户直接确认当前实际窗口。

本轮未 commit/push。详细构建、隔离验收、首次安装错误和最终核验回执位于 `C:/Users/luwei/AppData/Local/hermes/cache/scratch/repack-latest/`，最终证据为 `final-verification.json`。
