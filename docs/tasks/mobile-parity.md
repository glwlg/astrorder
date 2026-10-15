# 星序独立移动端 — Overlook 对照验收

Reference: `P:/workspace/glwlg/ai/Hermes-plugins/overlook/server/mobile.html`, paired live page `http://127.0.0.1:9999/`.

## Non-negotiable scope
- Independent mobile component tree, shell, composer and transcript; do not shrink Desktop AppShell/ChatPage.
- Shared native-ID store/API/command semantics only. Keep fixed project order and tool-notification suppression.
- Never send prompts or destructive controls to existing working sessions during comparison.
- Browser automation is not real-device proof for microphone, mobile keyboards, iOS PWA/Web Push or background suspension.

## Acceptance inventory
Status starts **pending**; implemented does not mean exercised.

| Area | Operations | Status |
|---|---|---|
| Shell | portrait layout, safe areas, top status, theme light/dark/system, refresh | pending |
| Session drawer | bottom dock, backdrop/close, projects, fold, search, all/unread/open/pinned/24h filters | pending |
| Navigation | select session, left/right swipe, route/source isolation | pending |
| Transcript | text/markdown, thinking/tool packs, individual folds, earlier messages, follow/pause/bottom arrow | pending |
| Message actions | long press, copy, select text sheet, quote/cancel quote, code copy/wrap | pending |
| Composer | multiline, clear, draft per session, send/stop, delivered/error state | pending |
| Attachments | choose/remove, image preview/lightbox, text+image, image-only | pending |
| Voice | permission, recording/stop, playback, cancel, transcript/draft | pending |
| Quick actions | continue, real status (not LLM /status), progress summary, stop tasks | pending |
| Activities | active cards, details/logs, log copy/bottom, individual stop | pending |
| Queue | queued text/attachments, persistence, send-now, ambiguous delivery protection | pending |
| Models | native list/search/select/read-back | pending |
| Notifications | explicit permission, no replay storms, background limitations | pending |
| Resilience | offline drafts, reconnect, visibility/online recovery | pending |
| PWA | manifest/icons/start URL, installation | pending |

## Executed first-pass evidence

- `frontend/e2e/mobile-comparison.mjs` operated the paired Overlook page and authenticated Astrorder `/mobile` at 390×844. Latest run: 12 checks passed, no failures. JSON and screenshots: `frontend/test-results/mobile-parity/`.
- Exercised: theme cycle; drawer open/close, filters and search; independent shell/no desktop DOM and no horizontal overflow; multiline draft/clear; status sheet; file choose/remove without sending; voice sheet open/cancel without microphone permission; message selection/quote/cancel; model catalog/search without changing a working session.
- `npm test`: 61 existing frontend tests passed. `npm run build`: passed (existing bundle-size warning).
- `test_native_mobile_controls.py` and `test_native_read_reconnect.py`: 4 backend tests passed; this proves adapter contracts, not live model switching.
- Original and target screenshots were inspected. Main controls are visible without overflow. Pixel parity has NOT been achieved: drawer row treatment, avatar/icons, queue/status details and recording UI still differ.

## Still required before full acceptance

- Dedicated disposable-session send/image-only/stop/queue drain and reconnect tests. Existing work sessions were not used for destructive/send tests.
- Live model switch plus native read-back on a disposable session (catalog/search was tested).
- Actual touch swipe/long-press, microphone recording, async image resizing and mobile keyboard tests, including iOS.
- Notification permission, PWA manifest/installation and background Web Push; none are claimed to be verified by desktop Chromium.
- Complete visual alignment of all sheets, pinned breakout, model metadata and selection states.

No 1:1 completion claim is permitted until every operation has an evidence entry or an explicitly accepted limitation.

## PC 操作功能对齐补充验收（2026-10-14）

输入锁、目标完整生命周期、子代理详情／跳转／精确停止、待办／计划／工具记录、用量／模型／审批、队列完整操作，以及文件编辑保存／Git／终端已补齐移动入口。最后队列触摸取消、长队列滚动、减少动态效果、同源鉴权和跨会话保存归属修正已复验。

最新前端测试 473/473 通过，TypeScript 与生产构建退出码 0，当前构建配合独立 HTTP/WebSocket 夹具的手机浏览器场景 13/13 通过。功能映射、证据路径和限制见 [移动端与 PC 操作功能对齐验收](../reports/mobile-pc-feature-parity.md)。上述结果不改变本文先前列出的真机麦克风、软键盘、iOS PWA/Web Push 和真实 Agent 执行链验收边界；开发验收完成后，已按用户授权部署网页资源与重新编译的桌面客户端，大小内核进程均保持运行，现场核验见链接报告的部署段落。

本次续验增加即时发送先持久化、在线／离线存储失败恢复、带图引用离线刷新重连、丢失回执按原 command ID 核对、队列自动发送后刷新聊天、重叠唤醒与旧连接回调保护。浏览器还实际执行了长按复制／原消息文字选择／引用、Escape／外部点击关闭、斜滑切会话、边缘打开／滑动关闭抽屉，并确认跨会话草稿和引用保留、零误发。文字选择和触摸后 Escape 问题均先复现失败再修复。

## 目标入口与无目标误报修复（2026-10-14，尚未部署）

按用户最新要求，目标由 Agent 原有命令创建，PC 和移动端无目标时不提供常驻创建／管理入口。修正空原生目标被投影为取消任务并触发失败通知的问题：未有目标及重复空快照不产生取消任务，已有目标清除保持正常事件同步，取消目标不弹失败通知，真实失败仍提示。

本次前端全量 481/481、隔离桌面／手机浏览器 15/15、TypeScript／网页构建、App projection/sessions 测试与 App vet/build 均通过。App 全模块测试另有 12 项安装／进程夹具失败，未计全部通过。详情、证据和未部署状态见 [无目标会话修复验收](../reports/native-goal-empty-state.md)。

## Codex 活动回合引导发送修复（2026-10-14，尚未部署）

修正星序已持有的活动 Codex 会话仍因 `codex session is active` 无法引导的问题。小内核以真实活动回合选择原生 steer，核对同一 turn，保留原任务命令身份，并以独立持久事件确认引导投递。共享队列允许显式引导越过已确认的活动回执；PC 确认失败可重试，未知结果仅核对。

最终前端 502/502、隔离桌面／手机浏览器 17/17、相关 Go 包、大小内核 vet/build、Codex/gRPC/protocol Linux race 和实际 Codex 进程＋本机 Responses 夹具验收通过。Windows 小内核独立进程测试仍失败，不计全模块通过。详见 [活动回合引导验收](../reports/codex-active-guidance.md)。未部署或重启线上进程。

本文前面的 pending 清单保留初始验收快照；当前已执行证据及未执行设备／原生项目以链接报告为准，不能从旧 pending 状态推断最新代码未实现，也不能由隔离场景通过推断全部现场验收完成。
