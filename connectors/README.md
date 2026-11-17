# Agent 运行时与插件

原生 Agent 进程由 `session-daemon-go/internal/runtime/` 托管。`connectors/` 保存宿主插件和安装资产，不提供第二套 Python Codex 控制服务。

## 运行时归属

| 对象 | 当前实现 |
| --- | --- |
| Codex 会话、模型、审批与中断 | Go 小内核的 Codex app-server 适配 |
| Hermes 本机与远程会话 | Go 小内核托管及 Hermes 原生插件 |
| Grok、Antigravity | Go 原生运行时与观察适配 |
| SSH、WSL、交互终端 | Go 小内核的远程环境和 PTY 管理 |
| Agent CLI、技能分发、观察钩子 | Go `astrorder-agent`、`astrorder-helper` 与能力中心 |

## 安装资产

- `codex/codex-plugin/`：Codex 插件清单和技能资产。
- `hermes/astrorder_hermes_plugin/`：Hermes 轻量宿主插件，运行于 Hermes 自己的 Python 环境。
- `.hermes/plugins/astrorder-hermes/`：随安装器交付的 Hermes 插件包装目录。

这些目录由当前安装器使用，不能因为包含 Python 文件就删除。Hermes 插件不承载星序 HTTP 服务或应用数据库。

## 连接与身份

通过星序的连接管理和能力中心配置本机、WSL 与 SSH 环境，由小内核按运行时协议托管。Agent ID、connection ID 和原生 session ID 各自保持原样，不通过名称或最近活跃时间推测身份。

浏览器访问凭据与连接器内部凭据分开管理；私钥路径是引用，不在项目源码、技能或日志中写入密钥内容。大内核更新不结束托管 Agent，维护边界见 [AGENTS.md](../AGENTS.md)。
