# Codex 访问权限切换回归修复

## 故障与修复

用户截图提示 `ReadApprovalMode verification failed: rpc error: code = Unimplemented desc = runtime does not support this session read`。

App 的 PUT 链路先调用 SetApprovalMode，确认完成回执，再调用 ReadApprovalMode。Codex 适配器已有两项实现，但 runtime/session_read.go 的读白名单仅允许 Hermes 和 SSH Hermes。Codex 和 Codex SSH 的写入可以成功，随后验证被 Registry 拒绝，App 返回错误且不会保存新的 UI 绑定。

修复只为 session.approval.read 加入 codex 和 codex-ssh。保留认证、会话归属校验与读回确认，不绕过验证。

## 已验证

- 新增 codex_approval_mode_test.go，真实 loopback TCP gRPC、Registry 与 Codex Adapter，原生 stdio 对端为测试夹具。本机/SSH 两类 runtime × manual/auto/full_access 在修复前全部复现用户相同 Unimplemented 错误，修复后全部通过。
- 同一回归检查未认证和错误归属被拒、读不改变 journal/activity，以及 Send 的 turn/start 实际参数 approvalPolicy、approvalsReviewer、sandboxPolicy。
- 新增可选 codex_approval_real_test.go。已安装 Codex CLI 0.161.0，空 CODEX_HOME、独立 workspace 和 daemon DB；真实原生创建，auto → full_access → manual 写入与 gRPC 读回全部成功。此检查不调用模型生成，不建立真实 SSH 网络连接，不读用户聊天和凭据。
- session-daemon-go 全量 go test ./... -count=1、go vet ./...、候选 daemon 编译成功。
- git -c core.whitespace=cr-at-eol diff --check 成功。

验证日志位于 Hermes scratch：approval-mode-green.log、approval-real-codex.log、approval-daemon-tests.log、approval-daemon-vet.log。修复前失败保存在 RTK recall bfd48cbf804d。

## 实装状态

排查时 daemon status 返回 stopped，旧 PID 44840 的身份未验证、监听端口 56572 无进程占用；端口连接失败，进程查询也未见该 daemon。App PID 46644 的 health 返回 200。此时无法读取 daemon 权威会话状态，不能据此认定没有活动会话。

上述为授权前状态。用户随后明确回复“同意”，授权本次备份、替换小内核并恢复启动。

## 候选交付

- Canonical 构建成功，包含 contracts、Session Daemon、App Server 全量测试与 vet、12 个基础构件、前端 TypeScript/Vite 及 Wails 客户端。
- 新装、覆盖升级（客户端文件身份更新）、12 构件 SHA 校验、独立双内核生命周期、保留数据卸载隔离验收全部通过，exit 0。
- 安装包：`release/Astrorder-Setup-0.1.0.exe`，126613194 字节。
- 安装包 SHA256：`e86a9d3aaa6ada63852b64e6dfbd0f5c177b6b41878cf838762f2dec85b66d6a`。
- 包内小内核 SHA256：`de083a3a55662ada9b8fda067defc5bdd15dc57e848836e5833877273b542df7`。
- manifest 与实际包/构件逐一核验成功；release 无 ZIP。未 commit/push。
- 构建日志：Hermes scratch `approval-installer-final-build.log`；隔离日志 `approval-installer-isolation.log`。前两次构建因 PATH 未找到 buf 提前失败，随后使用显式工具路径完成构建，失败日志保留。

## 授权后实装验收

- 已备份到 `C:/Users/luwei/AppData/Local/Astrorder/backups/approval-daemon-20261013-204822`，替换小内核并启动，实装 SHA256 与候选一致。
- 小内核 PID 43524、创建时间 134363693037232626，身份与监听归属均通过验证；权威状态保留 142 条会话记录，验收结束时运行中/等待审批会话数为 0。
- 大内核重连并重启验收后最终 PID 15968，health HTTP 200。桌面客户端无需重装。
- 通过实装 MCP 创建临时真实 Codex 会话 `local-codex::01a13550-aa4a-7960-9d24-ce4dc2939227`，走客户端使用的 `/api/v1/sessions/{id}/approval-mode` POST/GET，依次 manual → auto → full_access → auto → manual，五次写入与读回均 HTTP 200，返回 mode 精确一致。
- 再次重启大内核后 GET 仍返回 manual，前后小内核 PID 和创建时间未变化。
- 临时会话经原生删除接口清理成功，之后权限 GET 返回 404。未修改既有用户会话权限，未发送模型提示词或读取用户聊天。
- 同步三个模型 helper 构件及校验清单，最终实装全部 12 个基础构件 SHA256 与本次包一致；同步期间小内核未再次重启。
- 现场验证是实装 HTTP → gRPC → 真实 Codex 链路，没有自动点击桌面权限菜单；不将接口验证表述为 UI 点击验收。
- 实装回执：Hermes scratch `approval-daemon-deployment.json`；五次切换、重启读回与精确清理回执：`approval-live-http.json`。
