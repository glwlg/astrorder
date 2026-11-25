# Wails 3 桌面客户端

本目录是星序当前的 Windows 桌面实现，复用 `frontend/`。Go 桌面壳负责原生主窗口、独立设置窗口、悬浮会议录音胶囊、托盘、通知、剪贴板、文件对话框、图片预览和大小内核管理，不维护第二套业务 UI。

## 工具链

版本以 `toolchain.json`、`go.mod/go.sum` 和 `frontend/package.json/package-lock.json` 为准。目标平台为 Windows x64，构建主机支持 Windows 与 WSL。Windows 运行需要 WebView2；安装器检测缺失的运行时并明确报错。

WSL 需要 Go、Node/npm、Wails CLI；安装包构建还需要 Buf、protoc-gen-go、protoc-gen-go-grpc 和 NSIS。Windows 工具准备：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/setup-desktop-wails.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/install-nsis.ps1
```

Windows 专用工具安装在 `.tools/wails/`，WSL 使用 PATH 中的工具。不会启动或更新已安装的内核。

## 构建与验证

以下命令从仓库根目录执行。Windows 使用 `.tools/wails/` 中与 `toolchain.json` 一致的 Node 执行脚本。

```sh
node scripts/desktop-wails.mjs install
node scripts/desktop-wails.mjs build
node scripts/desktop-wails.mjs check
node scripts/build-installer.mjs
```

- `install`：安装锁定的 Go 和前端依赖，不是安装或发布桌面程序。
- `build`：生成图标与 manifest、Wails bindings、桌面版前端，然后交叉编译 Windows EXE。
- `check`：校验 Go module、桌面测试/vet、Node 桥接测试与前端 lint。WSL 只能编译 Windows 测试，测试执行须在 Windows 上完成。
- `test`：运行前端 Vitest。
- `run`：在 Windows 运行已构建的开发 EXE。
- `smoke`：在 Windows 启动真实 WebView，验证前端绑定后退出；使用隔离开发配置。

开发产物为 `desktop-wails/bin/astrorder-desktop-dev.exe`。前端复制到 `assets/app/` 后嵌入 EXE；`bin/`、`assets/app/` 和工具目录不入 Git。正式安装器输出到 `release/`，Go 构件由 `scripts/stage-go-artifacts.js` 校验并暂存到 `.runtime/go-stage/`。

`assets/image-preview.html` 是独立原生图片窗口，`assets/meeting-capsule.html` 是悬浮会议录音胶囊窗口。HTML 与前端资源一起嵌入桌面程序，修改后需要重建 EXE。

## 中文输入法

当前 Wails 版本在窗口激活时的 WebView 焦点处理会影响部分中文输入法候选框。项目脚本通过 `scripts/wails-ime-fix.mjs` 校验并修补临时源码副本，再构建桌面程序。直接 `go build` 不带此修复；升级 Wails 时须重新验证，源码不匹配时构建明确失败。

## 开发隔离

开发程序默认使用 `%LOCALAPPDATA%/Astrorder-Wails-Dev`，不允许正式状态目录及其子目录。连接已独立启动的大内核时，显式传入回环地址和非生产端口：

```powershell
.\desktop-wails\bin\astrorder-desktop-dev.exe --backend http://127.0.0.1:30102 --state-dir "$env:LOCALAPPDATA\Astrorder-Wails-Dev"
```

`30102` 上的服务须独立准备。无 `--backend` 时业务 API 明确返回不可用。不要通过路径别名、重解析点或复用配置绕过隔离。

## 安装版与生命周期

正式程序位于安装目录的 `Astrorder.exe`，服务和插件位于其 `server/` 子目录，用户状态位于 `%LOCALAPPDATA%/Astrorder`。窗口关闭隐藏至托盘；显式退出与大内核管理使用受控生命周期入口，小内核保持独立。

小内核候选仅暂存为 `astrorder-sessiond.exe.new`。用户通过托盘「小内核 → 更新小内核」激活，由独立控制器完成替换；Agent 不停止、重启或更新小内核。构建、暂存和上线是不同步骤。

构建成功、`--check` 通过与真实窗口交互分别验收，不引用历史测试数量或进程号作为当前状态。发布步骤见[构建、安装与更新](../docs/DEPLOYMENT.md)，操作约束见 [AGENTS.md](../AGENTS.md)。
