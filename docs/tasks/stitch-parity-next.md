# Luna继续：先修门禁与回归，再补剩余功能
根目录 P:/workspace/glwlg/ai/astrorder，原会话20260906_235516_6f65df，gpt-5.6-luna/ocx/max，不扩大300000配置。用户要求继续完成仍有效。沿用stitch-parity-completion.md全部安全规则和V3项目优先结构。

上一轮180轮耗尽，已有任务持久化/API/TaskDetails/connector hooks、连接历史API/分页/UI、移动SSH初稿。不要重复阅读所有设计，继续实现。
协调者独立最终验证：后端57 passed，但Ruff失败tests/test_connector_artifacts.py:144 unused bridge(F841)。前端42 passed/1 failed：SshSettingsCard.mobile.test.tsx:37 在部署阶段仍getByLabelText('SSH 主机')；上轮调整没有删掉末尾重复调用。test失败导致lint/build尚未执行。

首要里程碑（必须先做完验证和更新报告，再做下一项）：
1. 修移动SSH身份阶段可跳过问题。阶段导航、部署按钮和提交handler都必须守住确认门禁，改变主机/端口/身份参数后旧确认失效；写真实失败回归，不能仅改禁用外观。系统OpenSSH host-key校验必须保留，前端checkbox不是安全替代。测试通过正确返回基本信息页修改字段，不能简单删除测试意图来绿。
2. 修Ruff根因并跑全量pytest/Ruff/test/lint/build。避免在预算结尾新增未经验证结构改动。
3. 更新docs/reports/stitch-implementation-luna.md：其尾部仍重复声称任务sheet/历史没实现并沿用53/37旧结果，必须分清历史阶段与当前状态，移动SSH是三阶段不是六步。记录本轮真实结果，不做假的全完成勾选。

之后逐项完成并各自验证：
4. 连接历史结构化诊断递归脱敏回归（嵌套对象/数组/键名/文本），不读取真实秘密。
5. 通知去重、完成/失败/待审批事件与权限门控、默认不主动申请权限；按来源会话/稳定事件身份去重，不能按文本/时间猜。
6. 语音成功录制→停止→预览/编辑→用户确认，转文字不可用的录音附件回退、取消保留原草稿。不请求真实麦克风，用明确模拟media测试并做实际浏览器UI操作。
7. PC/移动任务sheet、连接历史、SSH三阶段真实浏览器点击；真实后端fixture与mock、真实connector分开。补任务connector链路验收与旧项目/重复identity隔离回归。遵循已有授权范围做真实SSH/目录比对；若权限或环境阻塞明确记录，不反复重试也不假称通过。

每完成一个切片立即保存报告和测试证据，同一问题三次失败停止该路径记录。保留修改，不commit/push/reset、不改Hermes核心/Overlook，不重启30001/30002/Desktop/Relay，不杀用户进程，不读输出凭证，不向既有会话发测试。可启动并清理自己30000+隔离栈。先恢复可验收基线再扩展；留至少最后20轮做全量回归/报告，不能再留一个最后修改未测。
