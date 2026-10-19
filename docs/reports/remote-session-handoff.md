# 远程会话转交交付记录

## 行为

桌面与手机共用同环境目标筛选。源会话支持 Codex/Hermes 原生分叉，目标为同 SSH connection 中不同类型且 ready 的 Codex/Hermes/Grok。转交在原生分叉中生成摘要，不向源会话插入摘要指令；App 只保存原生摘要引用和操作状态。目标首次发送读取并消费摘要。

Go HTTP handler 已接通 handoff/cancel。重复 operation ID 保持幂等；摘要必须由精确原生命令/turn 成功完成事件确认。Hermes interrupted/error/partial、Codex 非 completed、EOF、溢出和失联均不能视为成功。首次投递不确定时保留 claim，并阻止后续发送绕过；不会自动重发或根据 idle 解锁。

Codex/Hermes 增加仅关闭指定空闲分叉的 session.close，保留原生历史。Codex 未确认远端退出时保留绑定。取消携带已确认的 Codex turn ID。摘要读取后再次释放临时绑定。

## 验证

- App 全量：1164 通过，10 跳过；相关 vet、最终构建通过。
- 小内核全量：647 通过，26 跳过；相关 vet、构建通过。
- 前端：660 通过，0 失败；类型检查、Wails 桌面和浏览器构建通过。
- 本机与 SSH mock 链路覆盖原生分叉、摘要引用、首次消费、重复请求、状态重建后重试、取消和清理。
- 隔离真实 Hermes owner 生命周期验证通过：分叉关闭后由新的 viewer 恢复同一原生身份并读取历史，邻接会话与 native 存储保留。历史为隔离测试夹具，未调用模型。
- 已安装浏览器验收：原报告的远程会话“转交”菜单可见且可点击，目标为同 WSL 环境的 Grok/Hermes；无效空请求返回 422，证明进入已实现处理器。
- git diff --check 通过。

Windows 首次复验曾遇到音频插件进程文件占用；独立 CIM 验证又暴露 WindowsApps/bash.exe 被选作测试 shell。验证脚本固定 Git Bash 后，最终全量通过。原生生命周期首次新增检查误用 session.history adapter action，改为实际 session.history_page 后通过；保留失败日志。

## 安装与暂存

App、桌面、浏览器已发布。113 个浏览器资源与构建清单一致，served index/主 JS/service worker 字节一致，health=ok。

- App SHA256：732be91acf8f90ae25c3b05c34524cc283e982cfe9eb67c3e62c2ddbb115fe21
- 桌面 SHA256：35097308d35bbf1923700850716bb313b7fbf02bf256fe1682a335cac9800598
- 小内核候选：P:/DevApp/Astrorder/server/bin/astrorder-sessiond.exe.new
- 候选 SHA256：f14280959d81021999ad32f3e941de7b6f16fcc51ebe93ee3c02178ba9522684
- update-check 已检出候选；未激活。

发布前后小内核 PID 50092、创建时间 134366794096674173 和运行文件哈希保持一致，19 个直属托管进程身份保留。App 独立更新，当前 PID 54492。交付备份及机器验收记录位于 C:/Users/luwei/AppData/Local/hermes/cache/scratch/remote-handoff/。

## 生效边界

按 AGENTS.md，小内核需用户通过系统托盘“小内核 → 更新小内核”手动激活。当前在线小内核未加载本轮分叉 close 改动，因此完整新链路的生效依赖候选激活。

未向真实模型会话提交验收提示或执行生产会话转交；自动化链路与隔离原生生命周期验证不等于真实模型摘要验收。未知首次投递保持锁定，尚无专门的产品核对/解锁入口，不能宣称自动恢复。没有 commit/push。
