# 文字与工具顺序修复

2026-10-15 已修复并更新安装版 Wails 及浏览器前端。未提交、推送或重打安装器。

## 原因与改动

- 原生历史本身提供条目顺序；工具完成时间不能代表条目位置。之前 `visibleTranscript` 对合并结果按 `created_at` 排序，工具完成时间较晚时移到后续文字之后，原本分隔的工具合并成一组。Codex 缺少条目时间时统一为 epoch，排序也无法恢复原生顺序。
- 现在直接按原生条目顺序渲染，以消息 ID 保留实时更新内容。新实时条目保持到达顺序；临时用户输入仍沿用原来的时间覆盖退出规则。
- React Query 的页数组最新页在前，现在先反转页顺序，再保留每页的原生顺序。
- 移动端会生成新的会话对象。原历史合并 effect 依赖该对象，每次重渲染都可能把缓存快照重新写入 store、覆盖实时工具输出。现改依赖 agent/session ID 及历史数据。
- 渲染器继续只折叠相邻工具，不跨文字分组。

## 验证

- 修改前定向测试实际失败：工具被排序至答复之后；分页顺序颠倒；会话对象变化后实时输出被缓存覆盖。
- 前端全量 109 文件、546 项通过；类型检查、浏览器及 Wails canonical build、packaged GUI EXE 构建通过。
- 定向 oxlint exit 0，保留既有 `useAstrorderData.ts` ref warning；diff whitespace check 通过。JSDOM 全量日志有既有 scrollTo 未实现提示。
- 候选和安装版均通过 Edge headless 桌面 1200×900、移动 390×844 验收：两组工具分别位于三段文字之间，工具完成更新不移动，翻页及重连原生历史刷新保持顺序，无页面异常。
- 浏览器验收使用隔离 HTTP/WS fixture，实际加载构建/安装服务的 UI；不代表真实模型推理或用户会话验收，未读取生产聊天或提交真实 prompt。

## 部署

- `P:/DevApp/Astrorder/Astrorder.exe` 已更新，安装路径 `--check` 的 kernelManagement 通过，新桌面进程已启动；SHA256 为 `348521DCD47FE8795E1A32593674B278F977BFF9998431C5C820DD0C2C19CC99`。
- 安装服务 113 个静态文件与本次构建磁盘、HTTP SHA256 全部一致。Wails 内嵌资源不含 PWA worker，浏览器构建保留 worker。
- 本次仅前端改动：App Server、小内核、控制器二进制未替换，两套后端没有重启。部署前后的 App、小内核 PID/创建时间和 19 个直属 daemon 子进程身份保持不变，App healthy。
- 首轮 EXE 文件访问拒绝导致回滚；重试为停止后的文件解锁增加有界等待，第二轮成功。
- 备份：`C:/Users/luwei/AppData/Local/hermes/cache/scratch/transcript-order-backup-20261015-104417`。
- 回执：scratch 的 `transcript-order-deployment.json`；验收：`transcript-order-installed-browser.json`；截图：`transcript-order-installed-desktop.png`、`transcript-order-installed-mobile.png`。
