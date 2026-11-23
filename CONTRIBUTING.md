# 贡献指南

欢迎参与 **星序 · Astrorder** 的开发与维护。提交改动前请先阅读本文档与[架构说明](docs/ARCHITECTURE.md)。

## 开发环境准备

- **Node.js**：与根目录 `.node-version` 保持一致。
- **Go**：与 `contracts/go.mod`、`app-server-go/go.mod`、`session-daemon-go/go.mod` 保持一致。
- **前端依赖**：运行 `npm --prefix frontend ci`。
- **桌面与打包工具**（修改桌面壳、protobuf 或安装包时需要）：Wails CLI、Buf、`protoc-gen-go`、`protoc-gen-go-grpc` 与 NSIS，具体版本和准备方式见 [`desktop-wails/README.md`](desktop-wails/README.md) 与 [`contracts/README.md`](contracts/README.md)。

## 本地开发隔离

- 开发前端固定运行在 `127.0.0.1:30001`，默认代理后端 `127.0.0.1:30002`。
- 桌面开发壳默认使用 `%LOCALAPPDATA%\Astrorder-Wails-Dev`，请勿与已安装正式版的端口或 `%LOCALAPPDATA%\Astrorder` 状态目录混用。
- 测试数据与临时环境使用独立目录，避免污染本机真实 Agent 配置或数据库。

## 代码边界约定

1. **保持大小内核职责清晰**：
   - 进程托管、事件 WAL、原生会话控制与模型配置写入归属 `session-daemon-go/`。
   - HTTP/WS API、事件投影、群聊编排、文件/Git 服务与前端静态资源归属 `app-server-go/`。
   - 两者之间的协议变更必须通过 `contracts/proto/` 定义并重新生成 Go 代码，不引入非类型化的通用 JSON RPC 转发入口。
2. **原生运行时事实优先**：会话身份、历史、模型和运行状态以原生 Agent 返回为准，不伪造“已连接”或空闲状态。
3. **安全与凭据隔离**：不在源码、测试夹具或日志中提交密钥、Token 或私钥内容；文件与终端操作必须经过工作区边界校验。
4. **桌面打包注意**：桌面内嵌前端使用 `ASTRORDER_WAILS=1` 构建（不注册 PWA Service Worker），与 Web/PWA 产物分开构建，且须通过 `scripts/desktop-wails.mjs` 构建以保留中文输入法修补。

## 验证清单

按改动范围运行对应的测试与检查：

```sh
# 契约校验（修改 proto 或 action-map 时）
go -C contracts test ./...

# Go 大小内核测试
go -C app-server-go test ./...
go -C session-daemon-go test ./...

# 前端类型检查、Lint、单测与生产构建
npm --prefix frontend run lint
npm --prefix frontend test
npm --prefix frontend run build

# 构建脚本与桌面前端桥接测试
node --test scripts/stage-go-artifacts.test.js scripts/wails-ime-fix.test.mjs desktop-wails/assets/*.test.mjs
```

如需一次性校验协议生成一致性与全部 Go 模块，可执行 `bash scripts/build-go.sh`（WSL / Linux）或 `scripts/build-go.ps1`（Windows）。Windows 桌面交互与安装包验收须在真实 Windows 环境中完成。

## 提交与运维约束

- 保持单次提交职责单一、测试通过，不顺带提交无关的本地运行产物或临时文件。
- 涉及已安装实例维护和小内核更新流程时，请遵守 [`AGENTS.md`](AGENTS.md) 与[构建、安装与更新](docs/DEPLOYMENT.md) 中的边界约束。
