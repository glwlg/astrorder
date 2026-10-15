# 大内核持久化消息队列

排队消息现在通过 `enqueue` 写入 App SQLite，附件通过现有上传接口落盘。桌面聊天、移动聊天和监控页读取同一组命令，并通过事件及 bootstrap 同步。App 运行期间独立调度，不依赖聊天页面挂载。

离线消息和 File 对象先暂存 IndexedDB；全局同步在连接恢复、online 事件及定时检查时迁移所有会话。迁移沿用原命令 ID，逐个保存附件上传进度。enqueue 回执丢失允许同 ID、同 payload 重读持久回执；原生 received/unknown 不自动重放。

调度以 live daemon 的身份、存储健康和精确 owned idle 状态为前提，原子领取每会话最早一条 queued 命令。received/accepted/running/unknown 阻止后续自动派发。Hermes、Codex、Grok 的 idle_only 检查防止快照过期时将队列输入转为引导；发现状态竞态时保留失败回执供明确重试。

自动调度要求小内核 protocol minor >= 1。旧小内核继续允许队列持久化和跨端同步，但不自动派发。新小内核仅暂存为安装目录的 astrorder-sessiond.exe.new，用户通过托盘“小内核 → 更新小内核”激活。此任务没有停止或重启生产小内核。

验证：App 全模块 1044 pass、9 skip；前端 115 文件、570 项通过；TypeScript、Vite browser/desktop 构建、Go vet、桌面 Go 测试和正式客户端 --check 均通过。补充回归覆盖数据库关闭重开后的附件原始字节及 FIFO、部分上传中断后恢复、丢回执同 ID 迁移、另一会话无聊天页时恢复同步、漏取消后的权威 bootstrap。小内核最终全模块 599 pass、20 skip；各适配器测试通过，结果见 scratch 日志。

Windows race 命令因缺失 gcc 在构建阶段失败，未完成竞态检测。上述回归使用隔离数据和 mock 原生 RPC，未向用户的真实模型会话发送验收消息，未宣称真实手机发送验收。

本次发布独立更新大内核、桌面 EXE 和 browser dist；核对已安装哈希、113 个静态文件、served index/主脚本/service worker、桌面进程及 HTTP health。小内核身份保持不变，发布前记录的 25 个子进程全部保持身份。具体证据：C:/Users/luwei/AppData/Local/hermes/cache/scratch/astrorder-queue/delivery-result.json。

边界：大内核停止时队列保留，重新启动后恢复调度。窗口隐藏或页面关闭不影响正在运行的大内核；显式退出客户端按现有生命周期会停止大内核。尚未完成不同浏览器上下文并发修改同一本地 IndexedDB staging 的验收。
