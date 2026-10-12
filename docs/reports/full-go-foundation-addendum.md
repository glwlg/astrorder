# 增量验收证据

这是独立基础层审查之后的增量，不改变 `full-go-foundation-final-review.json` 绑定的源码快照，也不声明全迁移完成。

## 附件路径与数据一致性

- `Store.AddAttachment` 插入、读回及 wire 转换处于同一事务。真实 SQLite trigger 将时间戳改成非法值的回归先 RED（error 后 row count=1），修复后 GREEN（error 且 row count=0）。
- JSON 哈希真实 Unicode 分隔符与字面 `\\u2028`/`\\u2029` 分开处理；Python `json.dumps(... ensure_ascii=False, sort_keys=True, separators=(',', ':'))` 生成常量，Go 回归先 RED 后 GREEN。独立审查已复验。
- 附件 `source_path` 的 Windows 单反斜杠在 Linux 无法取 basename，原跨平台测试返回 400。改为每个反斜杠转为斜杠；新增 Windows、POSIX 路径回归又复现 Windows 主机不认可 POSIX absolute。显式识别 POSIX 根路径后两平台均通过；引用路径只作为 metadata，不据此读取其他文件。
- Windows：`go test ./internal/attachments`、`go vet ./internal/attachments` exit 0。
- Linux WSL：`go test -race ./internal/attachments -count=1 -timeout=60s` exit 0，1.223s。
- 最新 Store/preferences Linux race exit 0（22.168s / 4.748s）；与附件第一次同批执行因上述路径问题返回非零，修复后单独复验附件通过。不能把初次失败隐藏成整批通过。

## 偏好 HTTP 领域基础

新增 `internal/preferences/http.go` 与行为测试：真实私有 SQLite 读写、鉴权/Origin、no-store、PATCH 与 import、null tombstone、列表去重、未知/非法字段 422、405、拒绝请求不改变数据。Windows 测试和 vet 通过，Linux race 通过。

独立审查发现存量 JSON 被静默过滤，已按 Python partition/read 语义保留非 bool pin、混合/null/object 列表、空后缀和大整数；GET/PATCH/import 实际 SQLite 回归通过。复审又发现 decoder 未检查尾部，`{} {}`/`{} trailing`/`null false` 回归先 RED（三项修改错误提交），补 EOF 验证后 Apply/Import 全回滚。第三次独立审查 PASS 见 `full-go-foundation-addendum-final-review.json/md`；保留前两份 FAIL，不能抹去发现记录。

尚未挂入 App 公共入口，尚未转移 Python 的 workspace_preferences 键域所有权，不加入最终 productregistry，也未删除 Python handler；不计作 P6 完成。

## 最新实际候选验证

- 新构建 `astrorder-app-foundation-current.exe`，Windows actual `TestAppCLIRealProcessLifecycle` exit 0（0.85s）：初始化、启动、重复启动、状态、重启、停止；DPAPI fixture 保留、listener 释放、独立 sentinel 未收到 lifecycle 请求且持续存活。
- 同一新候选运行 `scripts/migration/p4_contract_verify.mjs` exit 0，日志与结果保存在 Hermes scratch `p4-source-contract-7owkVi/result.json`。六组行为断言全部通过，三个测试进程退出 code 0、listener 释放。
- P4 仍是明确的过渡形态，旧 Python App 是业务/事件单写者；假设事件为明确 fixture，未触发原生推理。

## 范围

未提交或推送、未安装、未切换生产服务、未读取生产凭据/聊天/数据库、未联系或停止生产 Session Daemon。P0 有效默认值/原生能力矩阵、typed gRPC authority/event 流、v2 evidence/isolated runner 正在独立写入范围内实施，需最终稳定快照复验与独立审查。
