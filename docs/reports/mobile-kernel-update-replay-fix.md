# 手机更新小内核后大内核退出修复

2026-10-17 用户手机执行 daemon/update，持久操作 `5a05d34d199ad34c0e38f8aed6a9a543` 成功；随后 App 日志报 `stream probe-antigravity-local expected seq 1 but got 131` 并退出。

只读核对生产 WAL：该流 1～131 共 131 条事件完整存在。独立控制器启动 App 后正常回放至 checkpoint 131，恢复健康；没有修改生产数据库或再次重启小内核。

根因：`journal.AppendBatch` 仅在非 runtime.owner 且 status 非空时插入 catalog。Antigravity 的 connector.event/hello 使用空 status，历史先落盘而未进入订阅快照；恢复控制会话时 hello 先于 TrackSession 发出，实时流从 131 开始，App 以为新流应从 1 开始，触发致命序号检查。

修复：所有无状态事件在同一持久事务中 INSERT OR IGNORE catalog，默认 idle，不覆盖已有状态；App 实时跳号时按原 durable checkpoint 重新订阅回放，同流同 checkpoint 仅允许一次恢复，回放跳号或持续跳号仍失败，不跳过事件。

验证：缺陷测试先红后绿；App projection 103 项通过，包括补回 1～131 及真实缺失不推进 checkpoint；小内核 journal/events/grpcapi 专项通过，真实 TCP gRPC 覆盖 130 条无状态历史分页回放、LiveReady、实时 hello 131；完整 App 1147 pass/10 skip、daemon 643 pass/26 skip，vet 和候选构建通过。首轮冻结验证缺少 docs/tools 测试资源造成失败，补齐隔离候选资源后全量通过，未修改相关业务代码。

发布：仅替换并独立重启 App，保持 ctl/frontend 完全一致；小内核候选暂存到安装目录 astrorder-sessiond.exe.new，哈希一致并通过 update-check。生产当前小内核及 14 个直接子进程身份保持，未激活候选。发布回执与独立读回在 Hermes scratch/kernel-update-gap/{deployment,verification}.json。未以生产再次更新小内核作为验收，源码未提交推送。
