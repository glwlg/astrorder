# 星序 Stitch 设计落地 — Luna

工作根目录必须是 P:/workspace/glwlg/ai/astrorder。恢复会话 20260906_235516_6f65df。用户已授权现在开始编码；本轮不再生成新设计。默认模型 gpt-5.6-luna / ocx，配置上下文300000，不改全局配置、不扩大上下文。先确认工具cwd，再读本任务和相关源码。

## 优先级与权威材料
1. 用户最后纠正的侧栏方案：项目→会话两级；所有连接的项目混排，不设连接父节点或连接分区。远程短标识仅在项目行右侧、数量左侧；本机无来源标签。会话不挂连接名。不同连接/profile的同名项目仍必须内部隔离，不按名称合并。
2. docs/design/overlook-parity-and-interactions.md 是功能与交互验收清单；docs/design/project-first-v3-review.md 是设计审查结论。
3. .stitch/revision-v3.json 的五张页面是最新侧栏：.stitch/designs/v3/workspace-v3.html/png、connections-v3、connection-detail-v3、mobile-sessions-v3、monitor-v3。必须查看截图/HTML，而不是只读生成器描述。旧V2三层连接树已作废。
4. 移动其他五页：.stitch/revision-v2.json 中 mobile-connections-v2、mobile-chat-v2、mobile-tasks-v2、mobile-voice-v2、mobile-ssh-v2。PNG/HTML可能尚未下载；从 .stitch/revision-v2-readback.json 的对应screen ID与downloadUrl下载到.stitch/designs/v2再读取。不要照抄V2旧导航。必要时用Stitch get_screen精确回读，list_screens有滞后，不要重新生成。
5. P:/workspace/glwlg/ai/Hermes-plugins/overlook 只读参考。必须对照 desktop/plugin.js 与 server/mobile.html 及相关测试建立功能迁移覆盖，不只实现静态展示。

## 工作方式与范围
- 保护全部现有未提交修改，不reset/回滚、不commit/push、不改Hermes核心或Overlook、不杀用户进程/重启Desktop或Relay。
- 前后端业务及connector可以按需改，但先读定义和用法，保持项目风格，避免大文件继续膨胀。加载TDD/systematic-debugging/必要的设计实现技能。
- 先尽快创建 docs/reports/stitch-implementation-luna.md，写基线与里程碑；每阶段更新，避免预算/网络中断丢失进度。同一问题三次失败停止该路径并记录，不无限补丁。
- 需要审批的敏感操作按正常审批；无法批准则跳过并记录。不使用yolo、不读/打印私钥、token或.env，不向用户既有真实会话发测试指令。
- 用户授权已保存远程目标的Astrorder插件部署不等于绕过主机密钥验证/sudo/接管既有会话。本轮若真实测试需额外权限则报告阻塞。
- 端口保持前端30001、后端30002；不要用5173。未经新授权不重启当前预览；可用另一个30000+端口短暂启动你自己的隔离测试栈并自行清理。

## 实施阶段（各阶段可运行）
A. 基线与根因：跑前后端现有测试/lint/build。之前SessionRail.tsx误用Agent.control_state导致构建失败，先确认并修复真实类型归属。将当前SSH插件错误、重复本机Hermes、项目完整性状态写入报告，不把旧worker的connected报告当用户验收。
B. 框架和连接管理：按V3实现两级项目侧栏、全局跨来源项目排序、来源/计数分离、搜索/运行中/未读/置顶、加载更多/局部错误/只读历史。连接管理采用列表+详情抽屉、多SSH连接、逐阶段部署状态、唯一重试动作、运行历史收纳。不能仅CSS隐藏重复实例，需要稳定连接身份和旧记录迁移测试。
C. 工作台与Overlook功能：输入框正上方显示真实分支/改动、后台任务、Todo进度、子代理摘要；点击展开详情和日志。真实数据没有则诚实显示未知/不支持，不写死demo计数。语音转文字与录音附件回退，权限拒绝/取消/停止/草稿预览；不自动发送。附件/引用/模型/推理选择/上下文信息/队列/发送/独立停止、状态/未读/置顶、公开工具进度、代码复制/换行、阅读历史不抢滚动、回到最新、任务日志/通知。不要暴露私有推理。监控室多会话稳定位置、不限四席、待机队列和布局切换。
D. 移动适配：六类移动设计对应真实可点流程，包含项目导航、聊天运行态、任务sheet、语音sheet、连接列表、SSH分步设置；安全区与键盘适配。所有UI中文，生成HTML残留Tasks/Sub-agents等必须本地化。动作热区>=44px，手机输入字体>=16px。
E. 状态与动效：运行/思考/排队/审批/完成未读/失败/离线各自语义；只有真实运行时旋转/呼吸，完成即停；独立未读标记；状态更新不打乱列表位置。补prefers-reduced-motion（Stitch HTML缺失）、键盘焦点与aria-live节流。

## 严格验收
- 后端pytest全套与Ruff，前端test/lint/build，相关新增回归必须RED→GREEN。不能弱化断言。
- 浏览器真实点击PC与移动，不只组件快照；包括导航无遮挡、混合来源同名项目隔离、项目数不被当前cwd过滤、多SSH保存、断开/重连、队列所属会话、语音权限与取消、任务sheet和reduced-motion。
- SSH真实插件部署/启动/握手必须与前端实际调用链一致；若环境阻塞，逐项标明，不能声称修好。原生目录与Astrorder导入数量/ID要对照；禁止仅隐藏重复项冒充修复。
- mock用于测试时明确区分，不把mock事件/演示数据/accepted当真实完成。
- 每阶段报告改动、运行命令与真实结果、剩余项；若预算不足，交付已测试阶段和明确后续清单，不把全清单勾完。最后列预览服务是否使用新代码（未重启就说明），不得假称已部署。

现在开始实际编码，先检查基线和设计，再实现，勿停在计划。
