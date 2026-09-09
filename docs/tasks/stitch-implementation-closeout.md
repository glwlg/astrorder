# Luna 有界收尾：只修最终接线与验收
根目录 P:/workspace/glwlg/ai/astrorder；原会话 20260906_235516_6f65df。上一轮180轮耗尽，不能称全部完成。沿用 docs/tasks/stitch-implementation-luna.md 的授权边界、V3项目优先规则、安全规则和端口要求，但本轮不新增功能范围。

协调者刚独立运行最终代码：后端 uv run pytest -q =52 passed；uv run ruff check . ../connectors ../scripts通过。前端npm run test=35 passed，lint通过，build失败：src/components/sessionRailModel.ts(71,20): error TS2554: Expected 4 arguments, but got 2.

按顺序完成：
1. 先读相关定义/调用，修最终构建失败，添加覆盖调用路径的回归，不降低断言。
2. 将项目目录从bootstrap/state经AppShellLayout/Sidebar完整传到SessionRail；零会话项目可显示，同源稳定身份、多来源同名隔离、来源标签仅远程项目行且在数量左边（上一轮diff中数量在来源前，需检查CSS实际顺序）。不能引入连接父分组。
3. 验证旧数据库启动时ProjectRow表/数据迁移是否安全，以临时旧版数据库副本fixture验证，绝不直接读改用户真实敏感数据。不静默删项目/会话。
4. 重跑后端pytest/Ruff、前端test/lint/build。实际浏览器PC和手机验证上述最终接线以及运行条、连接抽屉、任务/语音入口和状态；mock与真实后端清楚分开。可启动自己的30000+隔离栈，结束只清理自己启动进程；不要重启30001/30002既有预览。
5. 在 docs/reports/stitch-implementation-luna.md 补齐上一轮实际已完成的连接列表/运行辅助条/VoiceInputSheet/monitor/项目目录，以及本轮最终命令结果和未完成清单。报告过时必须修正。不声称真实SSH已验收，没做就是未验证。不得把测试绿说成全部Overlook功能完成；据功能清单逐项区分已实现/仅入口/缺失/环境阻塞。

不要继续大规模设计审查/重新生成设计/研究无关代码。每20轮更新报告；同问题三次失败停止该路径记录。保留既有修改，不commit/push、不改Hermes核心、不读凭据、不向真实会话发测试。预算内优先可构建、接线完整和最终报告，而非继续堆未验证功能。
