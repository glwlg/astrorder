# 移动端内核管理

移动端「应用设置 → 内核管理」提供大小内核运行状态、小内核候选更新状态，以及「更新并重启小内核」「重启小内核」「重启大内核」。操作通过应用内确认弹层提交，小内核确认说明会话与终端进程可能中断；无法取得活动会话列表时明确显示未知。

后端新增 `/api/v1/services/kernels/status`、`/api/v1/services/kernels/operations` 和 `/api/v1/services/kernels/operations/{id}`。要求浏览器登录，写操作要求可信 Origin 和 `confirm_active:true`，拒绝 URL query token。只允许 daemon/update、daemon/restart、app/restart。旧小内核重启接口在安装模式委托同一个独立控制器。

HTTP 请求先持久化操作编号，再启动独立 `astrorderctl kernel-operation`。操作记录写入安装状态目录 `kernel-operations/`，状态为 queued/running/success/failed。新进程身份、监听归属及健康通过后才记成功；小内核更新还检查候选已消费。手机 sessionStorage 保存操作编号，临时断连后继续查询，不自动重发。服务端控制器总超时 150 秒，界面 180 秒后明确报告结果未确认。文件锁与执行声明防止并发重复操作，历史按数量和时间清理。

Windows 在旧大内核退出时可能短暂返回进程查询 Access is denied。控制器只重试权威状态读取；只有确认 stopped、没有所属监听、没有旧进程身份后才恢复启动，冲突状态禁止启动。

验证与发布证据保存在本机 Hermes scratch 的 `mobile-kernels/release/`。验收使用独立安装目录、独立数据和随机监听端口；生产小内核及其托管进程不参与重启验收。2026-10-17 已发布安装版大内核、控制器及 113 个浏览器静态文件。最终回归后端 1144 项通过、10 项跳过，前端 659 项通过，vet/lint/类型检查/构建通过。真实 Chrome 390×844 视口在隔离安装中完成三项确认操作，均读回 success 和新进程身份；取消未发送请求，未调用原生浏览器确认框。生产安装版移动菜单、状态读取、确认取消已验收，未提交生产内核操作。生产小内核与 26 个直接子进程身份、已安装及暂存小内核文件哈希均保持。第一次发布验证脚本因本机默认 GBK 解码网页失败而自动回滚；改为 UTF-8 后重新发布并完成独立读回。源码未提交推送。
