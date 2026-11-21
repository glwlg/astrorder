# 星序 · Astrorder

<div align="center">
  <img src="assets/brand/astrorder-mark.png" alt="星序 Astrorder Logo" width="160" />
  <p><strong>群星各有所长，协作自有秩序。</strong></p>
</div>

星序是本机优先的多 Agent 控制台，支持原生会话、跨机器协作和文件工作台。桌面客户端采用 Wails 3，浏览器和移动端共享 React 前端；Go 大内核与 Go 小内核独立运行，通过 gRPC 通信。

## 主要功能

- **原生会话**：接入 Codex、Hermes、Grok、Antigravity，以及 WSL、SSH 远程环境；保留原生会话身份、历史、模型与审批控制。
- **项目与工作台**：按项目组织会话，支持代码、图片、Markdown、终端、浏览器、Git Diff 和图形查看器。
- **群聊协作**：同一 Agent 可添加多次，成员可使用不同工作空间或派生已有会话。System One 协调消息投递与轮次，成员支持禁言和恢复。
- **黑板与产物**：跨 Agent 共享信息，自动识别文件产物并保留首次来源，可从原服务器读取内容。
- **运行观察**：展示会话运行、审批、工具调用、后台任务、通知与使用统计。
- **移动端**：独立的触屏布局、会话抽屉、附件、语音及工件预览。

## 代码结构

| 目录 | 职责 |
| --- | --- |
| `frontend/` | React、TypeScript、Vite、Mantine 前端 |
| `desktop-wails/` | Wails 3 Windows 客户端、原生窗口、托盘、通知与预览 |
| `app-server-go/` | HTTP API、事件投影、控制工具、群聊和文件服务 |
| `session-daemon-go/` | Agent 与终端进程托管、WAL、原生控制和模型配置执行 |
| `contracts/` | 大小内核之间的 protobuf/gRPC 契约与 Go 生成代码 |
| `connectors/` | Agent 宿主插件源码与安装资产 |
| `scripts/` | 构建、构件校验、安装器与隔离验证 |
| `assets/brand/` | 当前透明品牌母版与导出说明 |

## 开发与构建

Node 版本见 `.node-version`，Go 版本见各模块的 `go.mod`，桌面工具链见 `desktop-wails/toolchain.json`。协议生成需要 Buf、protoc-gen-go 和 protoc-gen-go-grpc；桌面构建还需要 Wails CLI，安装器需要 NSIS。详细准备步骤见[桌面开发说明](desktop-wails/README.md)。

安装前端依赖：

```sh
npm --prefix frontend ci
```

开发前端默认使用 `127.0.0.1:30001`，并代理到独立开发大内核 `127.0.0.1:30002`。不要与正在运行的安装版共用端口或状态目录。

```sh
npm --prefix frontend run dev
```

在 Windows 或 WSL 构建 Windows 桌面客户端：

```sh
node scripts/desktop-wails.mjs build
```

产物位于 `desktop-wails/bin/astrorder-desktop-dev.exe`。构建正式 NSIS 安装器：

```sh
node scripts/build-installer.mjs
```

安装器输出到 `release/`，公共 Go 构件暂存到 `.runtime/go-stage/`。构建本身不激活已安装的小内核。

## 验证

```sh
go -C contracts test ./...
go -C app-server-go test ./...
go -C session-daemon-go test ./...
npm --prefix frontend test
npm --prefix frontend run build
node --test scripts/stage-go-artifacts.test.js scripts/wails-ime-fix.test.mjs desktop-wails/assets/*.test.mjs
```

协议生成一致性、Go 测试、vet 和构件构建可通过 `bash scripts/build-go.sh`（WSL）或 `scripts/build-go.ps1`（Windows）执行。Windows 桌面运行检查须在 Windows 上执行；交叉编译成功不代表原生交互验收通过。

## 服务维护

大内核、桌面和小内核分别维护。大内核重启不得终止小内核及其托管的 Agent、SSH 和终端进程。小内核候选仅暂存为 `astrorder-sessiond.exe.new`，由用户通过托盘「小内核 → 更新小内核」激活，具体约束见 [AGENTS.md](AGENTS.md)。

运行状态、数据和凭据不属于源码清理范围。历史实现与一次性迁移记录通过 Git 历史追溯。

## 文档

- [文档索引](docs/README.md)
- [产品与技术架构](docs/PRODUCT.md)
- [通信契约](docs/CONTRACT.md)
- [大小内核职责](docs/design/dual-kernel-architecture.md)
- [安装与更新](docs/production-deployment.md)
- [小内核维护](docs/session-daemon.md)
- [连接器说明](connectors/README.md)
