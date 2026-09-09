# 移动端五项问题修复与验收

## 改动

1. 模型：使用移动面板内选择/确认，替代原生 window.confirm；选中态和确认按钮固定可见。后端区分原生 deferred 切换；agent 状态尚未更新时，另行读取原生 session.resume.info 精确核对 provider/model，不将旧 agent 的模型文本作为唯一依据。未确认仍失败，未降低断言。
2. 图标：主要移动端操作、麦克风、发送/停止、置顶、关闭和返回底部使用统一 Tabler SVG；复用现有星序 favicon。
3. 滑动：共享 isSessionOpen/adjacentOpenSession 规则，只跨开放会话，包含 idle-open，不包含 closed。新增独立鉴权 GET /api/v1/open-sessions，使用 session.active_list 的 session_key，不返回私有句柄；不让原生查询阻塞 bootstrap。读取失败的来源不伪造开放状态。
4. 置顶：桌面和移动端均增加全局置顶区，位于所有项目上方；项目内不重复显示置顶会话。项目原有排序保持不变，项目计数仍表示包含置顶的项目总数。
5. 桌面：增加 24小时筛选；测试近期空闲会话保留、超过 24 小时会话排除。

## 已执行

- `npm test`：75 passed。
- `npm run build`：通过；既有 bundle size warning 仍存在。
- 后端 `tests/test_native_mobile_controls.py tests/test_native_mutations.py`：16 passed，2 项依赖弃用 warning。
- `node e2e/mobile-isolated.mjs`：9 passed；真实 Chromium + 独立 FastAPI + 惰性协议测试连接器，不能冒充原生执行。
- 隔离浏览器已实际执行 CDP 触摸滑动，验证跳过关闭会话；验证两端置顶区在项目上方且无重复；点击桌面 24小时筛选并检查选中状态。
- `node e2e/mobile-comparison.mjs`：12 passed。
- 原生临时模型测试：本机与 SSH 曾返回 200；复测 SSH 捕获“模型切换尚未通过原生状态读回确认。”（502）。这是运行中的旧后端结果，不能把单次 200 当作完整修复证据。
- 全部临时模型会话已清理。最后一个临时会话 `20260908_213443_d7743c` 删除后已从 bootstrap 验证不存在；未发送测试聊天消息。
- `git diff --check`：通过。

## 部署边界

前端 30001 已通过 Vite 热更新，生产构建已生成。运行中的 30002 未重启，尚未加载新的模型确认逻辑与 open-sessions 接口。需授权重启后再验证原生开放列表与远程模型失败分支，不应宣称线上五项全部完成。

截图与 JSON 位于 `frontend/test-results/mobile-parity/`，全局置顶截图在 `isolated/pinned-mobile.png` 和 `isolated/pinned-desktop.png`。没有 commit/push，没有改 Hermes 核心。
