# Luna：只做真实链路收敛与测试服务清理
根目录P:/workspace/glwlg/ai/astrorder，原会话20260906_235516_6f65df。默认Luna/ocx/max，配置300000不变。用户要求继续完成有效。沿用stitch-parity-completion.md安全边界与V3两级项目侧栏。

协调者独立验证最新工作树：backend pytest58 passed + Ruff通过，frontend test52 passed + lint/build通过。不要再扩展UI、研究设计或重复实现已完成通知/语音。最后160轮日志确认你仍未真实SSH/native验收，并留下隔离服务。报告末尾149附近旧“功能范围外”文字和144的“服务已清理”已与当前实际冲突，先纠正，不可沿用。

1. 清理你上一轮启动的测试服务：协调者刚查LISTEN 30012 PID45072是本仓库frontend Vite；30102 PID37960是Python、cwd本仓库backend。PID会变化，先核实来源命令/父子与上一轮启动证据，确认是你自己的fixture后停止并回读端口释放；无法核实不杀。不得停止30001/30002或任何用户其他进程。报告清理真实状态。
2. 真实SSH/native首要：使用已保存目标的非敏感配置，先验证现有SSH权限/host-key和runtime条件，再在既有授权Astrorder插件范围内真实deploy/start/handshake，测试仅新隔离会话，不接管用户既有会话，不打印/读取私钥和token，不sudo、不改核心/全局plugin enable，不绕过审批或host-key。若必须更广权限则停止那一步，明确准确阻塞，不泛泛要求用户重新提供环境。已授权目标不是未授权，不得不尝试就报阻塞。
3. 对照native project catalog与bootstrap source/project集合（metadata only）找出3 vs12的具体身份/导入根因，修可修部分并写回归。3个duplicate identity需要稳定来源关系回读/非破坏迁移，不以隐藏或删除真实会话替代。保持跨机器/profile隔离与零会话项目。
4. 验证真实任务事件来自Hermes公开hooks→connector→API/store→UI、连接历史阶段真实流；不把合成fixture说成真实。project-local-only activation不得扩大为全局plugin启用；若现有设计必须全局启用，报告事实和实现选项，不偷偷执行。
5. 检查上一轮为了浏览器测试将TaskDetails Drawer transition duration设0的改动；自动化应等待可见而非砍正常动效。如属测试问题修等待与reduced-motion，避免重新大改UI。
6. 最终全量回归及报告：已实现与真实验收区分，列确实缺失/环境阻塞。用真实执行证据结束；最后20轮留作验证与清理，不留未测末尾修改。不commit/push/reset，不动现有预览和Overlook/Hermes核心。

预算到或同一问题三次失败停止该路径，写明已尝试命令类型与非敏感错误。无真实环境进展也应给出具体阻塞，不再继续无边界UI迭代。
