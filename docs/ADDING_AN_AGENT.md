# 接入新 Agent 引擎标准模板与清单

星序（Astrorder）已将原生 Agent 抽象为 **可插拔引擎插件**。接入一个新的 CLI / Coding Agent（例如 `claude`、`gemini`、`opencode` 等）时，按本模板自底向上完成 5 个标准槽位即可自动获得：

- 插件中心「Agent 引擎」开关与按需隐藏
- 启用时的自动体检（三态就绪：`missing` / `needs_setup` / `ready`）、PATH 提示、手动选路径与官方辅助安装（后台日志 + 交互终端）
- 停用保护与关联插件联动提示
- 跨本机 / WSL / SSH 的统一连接与会话托管

---

## 一、快速声明清单（最小接入骨架）

以接入假想新 Agent `claude`（Claude Code CLI）为例：

### 1. 前端引擎注册表（单一入口）

在 [`frontend/src/features/plugins/agentEngineRegistry.ts`](../frontend/src/features/plugins/agentEngineRegistry.ts) 的 `AGENT_ENGINE_REGISTRY` 中追加一条声明：

```ts
{
  id: 'agent-claude',
  kind: 'claude',
  aliases: ['claude-ssh'],
  title: 'Claude Code',
  shortLabel: 'Claude',
  description: '接入 Anthropic Claude Code 原生命令行编程 Agent',
  searchKeywords: ['claude', 'anthropic', 'agent', 'npm'],
  relatedPlugins: [], // 若有专属配套插件（如配额池），填其 pluginId
}
```

完成后，以下模块会**自动生效**（无需逐处硬编码）：
- 插件中心「Agent 引擎」分类卡片与开关
- `usePluginSettingsStore().isAgentEnabled(kind)` 全局门控（新建会话、欢迎页、群聊、转交、连接管理、RTK、历史会话只读提示）
- 停用确认弹窗的关联插件联动选项

> 品牌图标：在 [`frontend/src/components/AgentBrandIcon.tsx`](../frontend/src/components/AgentBrandIcon.tsx) 补充 `kind === 'claude'` 的图标和显示名；在 [`frontend/src/domain/types.ts`](../frontend/src/domain/types.ts) 将 `'claude'` 加入 `AgentKind`。

---

### 2. 大内核工具链与就绪体检注册表（单一入口）

在 [`app-server-go/internal/toolchain/agent_registry.go`](../app-server-go/internal/toolchain/agent_registry.go) 的 `registeredAgentEngines` 中追加一条 `AgentEngineSpec`：

```go
{
	Kind:         "claude",
	PluginID:     "agent-claude",
	Name:         "Claude Code",
	ShortLabel:   "Claude",
	Description:  "Anthropic Claude Code 原生编程 Agent（依赖 Node.js / npm）",
	Aliases:      []string{"claude-ssh"},
	Binaries:     []string{"claude"},
	VersionArgs:  []string{"--version"},
	Dependencies: []string{"node"}, // 缺失时辅助安装自动按 node -> claude 串联
	HomeField:    "claude",         // 在 envtarget.Paths 补齐对应配置目录
	AuthFiles:    []string{"settings.json", ".credentials.json"},
	AuthEnvVars:  []string{"ANTHROPIC_API_KEY"},
	AuthCommand:  "claude login",
	AuthHint:     "安装后可在交互终端执行 claude login 或配置 ANTHROPIC_API_KEY",
	DocsURL:      "https://docs.anthropic.com/",
	Capabilities: AgentEngineCapabilities{
		SupportsSSH:        true,
		SupportsRTK:        false,
		SupportsSessionCLI: true,
	},
	RecipesBuilder: func(targetOS string) []InstallRecipe {
		if strings.EqualFold(targetOS, "windows") {
			return []InstallRecipe{{
				ID:      "claude-npm-win",
				Label:   "npm 全局安装 @anthropic-ai/claude-code",
				Mode:    InstallModeBackground,
				Shell:   "powershell",
				Command: `npm install -g @anthropic-ai/claude-code@latest`,
			}}
		}
		return []InstallRecipe{{
			ID:      "claude-npm-unix",
			Label:   "npm 全局安装 @anthropic-ai/claude-code",
			Mode:    InstallModeBackground,
			Shell:   "sh",
			Command: `npm install -g @anthropic-ai/claude-code@latest`,
		}}
	},
},
```

同步补齐 3 处极小映射：
1. [`app-server-go/internal/store/plugin_settings.go`](../app-server-go/internal/store/plugin_settings.go)：将 `"claude"` 加入 `AgentKinds`、`CanonicalAgentKind`、`AgentPluginID`（确保首次自动初始化开关与 `Restore()` 门控生效）。
2. [`app-server-go/internal/envtarget/envtarget.go`](../app-server-go/internal/envtarget/envtarget.go)：在 `Paths` 中加入配置目录（如 `Claude: home + "/.claude"`），供三态就绪检测和技能分发使用。
3. [`app-server-go/internal/lifecycle/daemon_config.go`](../app-server-go/internal/lifecycle/daemon_config.go)：在 `EnsureDaemonConfig` 和 `UpsertLocalRuntime` 中补上 `"claude"` 与默认 `AgentID`（`"local-claude"`）。

---

### 3. 大小内核通信契约（`contracts/`）

在 [`contracts/proto/astrorder/sessiond/v1/sessiond.proto`](../contracts/proto/astrorder/sessiond/v1/sessiond.proto) 中：
1. `enum RuntimeKind` 增加 `RUNTIME_KIND_CLAUDE`（及可选 `RUNTIME_KIND_CLAUDE_SSH`）。
2. `message SshConnectConfig` 增加 `optional string claude_executable`（如支持 SSH 远端）。
3. 使用锁定的 `buf` / `protoc-gen-go` / `protoc-gen-go-grpc` 重新生成并通过 `ASTRORDER_VERIFY_GENERATION=1 go test ./...` 校验（详见 [`contracts/README.md`](../contracts/README.md)）。

---

### 4. 小内核原生运行时适配器（`session-daemon-go/`）

1. 新增 `session-daemon-go/internal/runtime/<kind>/` 实现 `core.Adapter`（会话创建、消息发送、中断、审批与事件发射；可选实现 `Snapshotter`、`ConfigReloader`）。
2. 若支持 SSH 远端，在 `session-daemon-go/internal/runtime/remote/` 新增对应工厂并用 `core.NewMultiplexRegistry` 注册。
3. 在 [`session-daemon-go/internal/configuration/config.go`](../session-daemon-go/internal/configuration/config.go) 的 `Config.Register` 中注册本机和远程适配器。
4. 按 [`AGENTS.md`](../AGENTS.md) 约束：构建验证后将新版小内核暂存为 `astrorder-sessiond.exe.new`，由用户通过系统托盘「小内核 → 更新小内核」手动激活。

---

### 5. 大内核连接与升级接线（`app-server-go/internal/connections/`）

1. `environments.go`：`environmentKinds` 增加 `"claude"`；`remoteRuntimeKind` 增加对应枚举映射。
2. `connections.go`：`runtimeTarget`、`changeEnvironmentAgent`、`parseSSHConfig` 增加对应分支。
3. `upgrade.go` & `version_check.go`：接入版本探测与原生升级命令。

---

## 二、验证清单

完成上述 5 步后，按顺序执行以下验证即可上线：

```sh
# 1. 协议与小内核验证（如新增了 RuntimeKind）
go -C contracts test ./...
go -C session-daemon-go test ./...

# 2. 大内核工具链、配置与连接测试
go -C app-server-go test ./internal/toolchain/... ./internal/store/... ./internal/lifecycle/... ./internal/connections/...

# 3. 前端类型、单测与构建
npm --prefix frontend test -- --run src/features/toolchain/ src/features/plugins/
npm --prefix frontend run lint
npm --prefix frontend run build
```
