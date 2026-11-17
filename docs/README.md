# 文档索引

## 开发与维护

- [产品与技术架构](PRODUCT.md)
- [通信契约](CONTRACT.md)
- [大小内核职责](design/dual-kernel-architecture.md)
- [安装与更新](production-deployment.md)
- [小内核维护](session-daemon.md)
- [桌面构建与调试](../desktop-wails/README.md)
- [连接器与插件](../connectors/README.md)
- [gRPC 契约](../contracts/README.md)
- [品牌母版与图标导出](../assets/brand/README.md)

## 功能规范

设计规范描述功能边界，具体字段、路由与实现状态以当前代码和当次验证为准。

- [群聊协作](design/bots-group-orchestration.md)
- [工件查看器与 Sidecar](design/artifact-viewers-and-drawio-sidecar.md)
- [Codex 审查接力](design/codex-review-relay.md)
- [状态视觉规范](design/status-visuals.md)
- [Agent 升级管理](features/agent-upgrade-management.md)
- [模型目录同步](llm-gateway-model-sync.md)

Swarm、OpenViking 记忆和项目导航以当前模块为维护入口：

- `app-server-go/internal/swarm/` 与 `frontend/src/features/monitor/`
- `app-server-go/internal/memory/`、`frontend/src/features/services/OpenVikingSettingsCard.tsx` 与 `features/sidecar/viewers/memory/`
- `frontend/src/components/SessionRail.tsx` 与 `sessionRailModel.ts`

已取代的原型任务和 Python 实施蓝图不再作为开发规范。

## 兼容性基线

`reports/` 保留校验器和回归测试仍读取的冻结基线、迁移映射及其引用证据。这些文件记录历史兼容性，不代表当前程序、安装版或部署状态；不要修改其冻结哈希来适配新功能。

旧架构方案、一次性开发任务、阶段验收与设计候选已从当前目录清理，历史通过 Git 追溯。当前维护命令以本索引链接的开发说明为准。
