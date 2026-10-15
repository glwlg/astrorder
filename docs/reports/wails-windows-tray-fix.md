# Wails Windows 图标与托盘状态修复

用户反馈：任务栏为默认空白文件图标，托盘标识模糊，大小内核菜单无状态。

## 修复

- 开发和安装包构建均调用 Wails `generate syso`，将多尺寸 `assets/app.ico` 与 Windows DPI manifest 编入 EXE。设置 `application.Options.Icon` 作为运行时回退。
- 基于现有星序 logo 生成透明 ICO，应用含 16/20/24/32/40/48/64/128/256 帧；托盘含 16/20/24/32/40/48 帧，并提供浅色、深色任务栏两种版本。避免把 192px PNG 在 Windows 图标 API 中直接缩小。
- 将托盘管理提取到 `tray.go`。每五秒读取 service CLI 状态，在主线程更新父菜单标签、子菜单状态和操作按钮；悬停、手动刷新、操作完成后触发更新。
- 查询失败显示状态未知，服务操作失败显示原生错误对话框。操作在后台执行，防止阻塞 UI。客户端退出时取消状态查询。
- 活动会话明细缺失时，小内核停止/重启必须请求原生确认，不能把字段缺失当作空集合。

## 验证

- desktop Go 全量测试、vet 通过；回归覆盖运行/停止/冲突/未知状态和托盘 ICO 尺寸、透明背景。
- Scratch Go overlay 启动真实隔离 packaged Wails 与空 runtimes 双内核，通过 Win32 `GetMenuStringW` 读回两项“运行中”；停止隔离 App 后读回“大内核 · 已停止”“小内核 · 运行中”。随后仅清理本次隔离进程。
- 等待真实窗口创建后，`WM_GETICON` 返回非零 HICON，导出窗口图标和 light/dark 小尺寸 HICON。窗口 32px、托盘 16px，均为星序标识。
- 第一轮探针错误地以开发 root 查询隔离 packaged 服务，已修正为独立安装布局。ApplicationStarted 可早于窗口句柄建立，探针现等待图标句柄就绪；探针和反射代码只存在 scratch overlay。
- 后续已在用户授权下完成客户端与双内核完整安装；之后 App 修补保持小内核 PID 44840 不变。当前交付结论以 `astrorder-wails3-acceptance.md` 第 8 节为准。

原生验收证据目录：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/tray-probe/`，`running.json`、`native-ui.json`、`loaded-icons-enlarged.png`。

此前安装包的资源和 HICON 验证没有覆盖用户实际任务栏显示；用户重装重启后仍反馈默认文件图标，不能据此认定此前修复成功。

## 2026-10-13 任务栏小图标补充修复

- 实装旧窗口 `WM_GETICON(ICON_SMALL)` 为零，窗口类大小图标均为空。Wails beta.28 的 `setIcon` 只设置 `ICON_BIG`，Windows `application.Options.Icon` setter 为空实现。
- 新增 `taskbar_windows.go`，通过公开 `WndProcInterceptor` 在框架设置大图标时绑定进程持有的小图标，使用现有多帧 ICO。没有修改依赖缓存、清空全局图标缓存或重启 Explorer。
- 真实 Win32 隐藏窗口回归测试先失败，再验证 `ICON_BIG` 保留且 `ICON_SMALL` / `ICON_SMALL2` 有效；桌面 16 项测试与 vet 通过。
- 完整 canonical build 成功，隔离新装、覆盖安装、12 构件哈希、大小内核生命周期与保留数据卸载通过。
- 从新包重新安装并启动客户端 PID 67072；窗口大图标 49030943、小图标及 small2 均为 84611343。`DrawIconEx` 实际绘制出星序标志，`SHGetFileInfoW` 解析实装 EXE 和开始菜单快捷方式均返回星序图案。
- 小内核权威 process 对象保持不变，PID 44840；App 健康检查 HTTP 200。备份目录：`C:/Users/luwei/AppData/Local/Astrorder/backups/taskbar-reinstall-20261013-185547`。
- **显示验收限制**：普通屏幕捕获只返回壁纸；任务栏 `PrintWindow` 虽返回成功，画面时间仍停在 18:44，不能作为 18:55 重启后的新画面。已确认窗口图标绑定与 Shell 解析修复，尚不能宣称当前任务栏可见显示已验证。

该阶段安装包 126613647 字节（120.75 MB），SHA256 `77c9aa0d674da0230fbdc9d545d95f395b369fb80391ede673d2265c85b12bd1`；桌面 EXE SHA256 `d261a2697011f0b5a0a7e5bfe950678e3c7d0d1b7fe511e4e491037f16d5a36c`，已被下述最终安装包取代。

证据位于 Hermes scratch：`taskbar-fix-build.log`、`taskbar-fix-isolation.log`、`taskbar-reinstall-current.json`、`live-window-icons-current.json`、`shell-icon-probe.json`，以及 `shell-exe.bmp`、`shell-shortcut.bmp`、`window-small-white.bmp`。截图缓存不能替代真实显示验收。

## 2026-10-13 实际任务栏默认图标定位

用户新截图确认小图标绑定后仍为白纸。此次实时 `PrintWindow` 画面时钟正常更新，按以下对照定位：

- 窗口 AppID、relaunch 元数据、窗口类图标、独立 ICO、隐藏后重建按钮均未恢复。
- 同字节 EXE 复制到新文件名后立即正常；原路径 `SHChangeNotify(UPDATEITEM)`、`SHUpdateImage`、`ie4uinit -show`、关联缓存通知均未恢复。
- 显式进程 AppID 和快捷方式 AppID 匹配仍无效，已撤掉这项源码试改并恢复原快捷方式。
- 删除旧 EXE 后在原路径创建新文件，图标恢复。随后恢复此前已交付的 EXE 字节（SHA256 `d261a2697011f0b5a0a7e5bfe950678e3c7d0d1b7fe511e4e491037f16d5a36c`）与原快捷方式，仅保留新文件身份，任务栏继续正确显示；再次重启 PID 44988 仍正确。
- 因而本次复现与旧文件身份关联的 Shell 图标缓存有关；不能再将小图标缺失视作全部根因。未重启 Explorer，未删除用户图标缓存数据库。
- NSIS 安装器改为写入客户端前删除旧 `Astrorder.exe`；文件被运行进程锁定则明确中止。仅针对桌面文件，不改大小内核生命周期。
- 隔离升级回归保留旧 EXE 的硬链接，并比较新旧 NTFS identity，避免文件号立即复用。旧安装包测试确实失败：`taskbar-file-identity-red.log`，不以字节哈希相同替代文件身份更新。

实时显示证据：`taskbar-file-replacement-green.bmp`、`taskbar-second-restart.json`；小内核 PID 44840、创建时间 134363487167922564 保持不变，App health 200。前节的显示验收限制在此轮已通过实时截图对照解除。

最终 canonical build 与隔离验收均 exit 0；新的覆盖升级测试确认客户端文件身份已更新。最终包再次实装客户端 PID 33340、App PID 46644，小内核 process 对象不变；19:54 实时任务栏截图显示星序图案，证据 `taskbar-final-visible.png`。已删除临时独立 ICO 和探针 EXE，保留原应用路径与快捷方式。

最终安装包 126613716 字节（120.75 MB），SHA256 `460d2fcd71f6a842a1f9d260c4fc2bfba2ee78bfab364e2796b620a0740d85fc`；实装桌面 EXE SHA256 `4228ccc7aba38f79b4f1e5e146e5152fb51bf78208a263a83f6cf860cbb40c81`，与 manifest 一致。日志：`taskbar-final-build.log`、`taskbar-final-isolation.log`；无 ZIP，未 commit/push。
