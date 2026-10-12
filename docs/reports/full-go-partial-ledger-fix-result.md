# 分片台账审查缺陷修复

状态：REVIEWED（限定 PARTIAL-001–004）。独立报告 `full-go-partial-ledger-rereview.md` PASS，机器索引注明协调者提取来源。整体迁移未完成，生产未变更。

首个 `antigravity/gemini-3.8-flash` 修复会话 `proc_c28e8e4d2f68` 退出 1，只完成调查和复现。源码哈希未改变，也没有结构化修复结果，不能计为修复。

收窄后，Flash 工作者完成严格 JSON 和父功能分组唯一性；协调者完成历史执行记录、分片重叠和路径规范修复。原 FAIL 报告保留为 `full-go-partial-ledger-review-fail.json/md`。

- PARTIAL-001：拒绝未知/拼错字段及多 JSON 对象，合法元数据显式建模，原生 contract JSON 保持 opaque。
- PARTIAL-002：台账不再携带未绑定的 commands/exit_codes/assertions；完整严格解析哈希绑定的原始进程结果，校验版本、阶段、类型、夹具范围、清理状态和显式非生产/非完成声明。原始结果逐字节保留。
- PARTIAL-003：feature 展开为 entry 前检查父 ID/feature ID 唯一性；单一父功能内的分片 ID 和 entry claim 不得重复。
- PARTIAL-004：证据必须采用规范 slash-relative 路径，并继续执行原有 symlink/root/regular-file 检查。

实际验证：新增缺陷测试先 RED 后 GREEN；完整 App `go test ./... -count=1 -timeout=120s`、`go vet ./...` exit 0；Linux parity/ctl race exit 0。真实 CLI 对私有台账副本执行 27 项错误变异全部拒绝，未改变副本通过。正式源码台账仅修改 F03 partial 子块，结构核验 472/472 exit 0；最终核验仍 exit 1，458 个入口未注册，无 invalid_partial。

修复 CLI：Hermes scratch `astrorderctl-partial-repaired.exe`，SHA256 `8a7c85e3758c3b8584009c9ff2c2d9d7853fb555f78e86fc4ac684fb8e63441c`。CLI 变异结果位于 scratch `full-go-partial-ledger-rereview-after/result.json`；源码哈希位于 scratch `full-go-partial-ledger-repaired-source-hashes.json`。历史原结果 `full-go-p2-operation-process-result.json` SHA256 `14cf665d21201663b988feb2fe5ed091aeab8f8ff1235069845147f6d8615c7e` 不变。

本修复验证声明仅覆盖台账规则；不满足父功能最终 Evidence，不代表实际产品注册、P0/P2/F03 完成或生产交付。独立审查者已实际执行专项 tests/vet exit 0，报告内 10 项源码/台账/历史結果哈希与当前一致；原 FAIL 报告保留。
