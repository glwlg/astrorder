# 会话原生模型绑定显示

## 修复

- 原问题：移动端 `modelNames` 仅记录手动切换结果，进入已有会话不会读模型；桌面未渲染模型控件。
- 新增鉴权 `GET /api/v1/sessions/{session_id}/model?agent_id=...`。检查精确会话范围后读取原生 `session.resume.info`；兼容缺失 info 的原生状态文本。只返回 model/provider，不泄露其他运行时字段，不用 Astrorder 自选全局模型填充未知值。
- `useSessionModel` 以来源和原生会话 ID 为缓存键。两端进入会话会自动读取。切换前后取消旧读取，防止迟到响应覆盖原生已确认的新模型。
- 移动端按钮展示绑定名，不再只显示“选择模型”。桌面在输入框上方展示模型和选择按钮，支持搜索、确认切换。
- 缺失原生信息时保留明确的未读取状态，不禁用聊天，不将别的会话的模型显示过来。

## 验证

- 前端 `npm test`：79 passed。
- 构建通过；既有 bundle size warning 未修改。
- 后端模型与原生变更相关测试：18 passed，2 项依赖弃用 warning。
- `node e2e/mobile-isolated.mjs`：10 passed。真实 Chromium 与隔离 FastAPI，模型 RPC 使用明确标记的 inert fixture，不冒充真实模型执行。
- 浏览器验证：不同会话分别显示不同模型；桌面切换；刷新后重新读取绑定；切换不污染另一会话；返回移动端显示同一绑定。
- 检查截图 `frontend/test-results/mobile-parity/isolated/bound-model-mobile.png` 与 `bound-model-desktop.png`，模型名和入口可见，没有遮挡。
- `git diff --check` 通过。

## 线上状态

本轮未重启 30002。新模型读取接口需获得重启授权并加载后，才能在真实运行环境完成最后验收；不要仅凭前端热更新宣称线上模型绑定显示已生效。未发送用户工作会话消息，未修改用户会话模型，未 commit/push。
