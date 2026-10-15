# Agent 批量升级与历史 WSL 目标修复

日期：2026-10-15（UTC+08:00）。

## 根因与修改

- 前端要求 NDJSON 升级执行事件，但 Go POST `/api/v1/agents/{id}/upgrade` 只调用版本查询并返回 `status: verified` JSON；没有执行更新。前端未收到 done 帧时默认 `{ok:false, exit_code:0}`，产生截图的“失败但退出码 0”。
- 新增 `internal/connections/upgrade.go` 原生执行器，执行 Codex 包管理器更新、Hermes `update --yes --no-gateway-restart` 或 Grok `update`，逐块输出 init/chunk/error/done，实际退出码决定成功。更新不受版本查询失败阻断，缺失连接明确拒绝。
- 前端要求真实完成帧、处理无换行末帧、保留 HTTP 与执行错误，不再吞掉 error 或把截断流当执行结果；批量界面显示成功/失败数，单个目标失败后继续其他目标。
- 远端版本查询与更新共用非交互安装目录解析；补 Hermes venv、Grok、Codex 安装路径与目标 PATH，抑制路径探测输出，避免安装路径数字被误识别为版本。本机动态解析真实 exe；npm cmd 使用相邻 JS 入口和 node。
- 升级列表原直接读取全部 Agent 历史注册。改为 `ListCurrentAgents` 且远端必须存在对应 SSH connection，已删除连接的旧 WSL 不再作为可升级安装目标；保留历史注册与会话。

## 验证

- 前端全量 112 文件、558 项测试通过，TypeScript 构建检查通过。新增流式协议与批量部分失败回归。
- Go connections/envtarget/store 三包回归及 vet 通过。测试覆盖真实子进程 stdout/stderr、非零退出、取消、Windows launcher、非交互 PATH、真实 HTTP handler 帧与已删除连接目标过滤。
- 桌面全量测试和正式桌面、浏览器/PWA、App、控制程序构建通过。桌面测试在独立 CIM 进程中运行。
- Hermes/Grok updater 参数由已安装 CLI 的 `update --help`核验；Hermes 官方安装文档同步查阅。
- 原目标过滤测试先复现旧 WSL 误纳入；前端先复现版本 JSON、无换行末帧、error 被吞与截断流问题；Hermes 非交互路径测试先复现失败，再修复通过。

## 正式交付

- 最终发布核验时间：2026-10-15 13:07:57 UTC+08:00。
- 安装目录 `P:/DevApp/Astrorder` 的前端壳、浏览器/PWA前端、大内核及控制程序已替换；桌面 PID25740，App PID25008。113个静态文件的完整集合、安装哈希与线上HTTP字节一致。
- 小内核 PID39136、端口56572、创建时间、运行文件哈希和最终发布前20个直属子进程身份保持；助手未停止、重启或更新小内核。当前 `.exe.new`已不存在，本次发布未创建候选。
- 发布worker退出后，再次验证服务 running、identity_verified、port_owned、文件哈希和health通过。
- 最终发布备份：`C:/Users/luwei/AppData/Local/Astrorder/backups/batch-upgrade-20261015-130701`；更早且包含本轮修复前版本的备份：`C:/Users/luwei/AppData/Local/Astrorder/backups/batch-upgrade-20261015-130458`。
- 回执：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/publish-batch-upgrade/deployment.json`与`final-verification.json`。
- 验证浏览器访问生产升级列表返回401，未读取生产凭据、数据库或聊天以绕过。生产Agent列表读取未出现截图的裸WSL；具体升级列表过滤已通过隔离实际handler回归。没有执行生产全部Agent更新，不能将隔离执行测试或资源核验称为各节点真实更新成功。
- 未重建安装包，未提交或推送。相关操作规则已保存至 `astrorder-agent-upgrade-debugging` 技能。
