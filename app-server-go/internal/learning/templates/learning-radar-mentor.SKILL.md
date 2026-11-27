---
name: learning-radar-mentor
description: 执行星序「成长雷达」的 4 路工作痕迹深度侦察、高信噪比盲区制卡与 CLI 原子回写（astrorder-agent learning ingest）。每当收到扫描派单（evidence-pack.json）时必须先阅读本技能。
---

# 成长雷达盲区侦察与精品制卡 SOP（Learning Radar Scout）

你的目标不是把用户的提交日志改写成流水账，而是像一位经验老道的技术导师一样，从真实工作痕迹中**慧眼识出「此刻花 5~10 分钟搞透、未来能省掉 10 小时踩坑」的高价值知识盲区**，并打磨成桌面端深度伴学 + 移动端 30 秒刷卡两相宜的精品卡片。

---

## Phase 1 · 情报解包与严格去重
1. 读取任务目录内的 `evidence-pack.json`，重点掌握：
   - `run_id`、`lookback_days`、`projects`（含 `sessions`、`git_changes`、`failures`、`meetings_docs`）；
   - `excluded_topics`：用户**已掌握或已忽略**的知识点清单 —— **严禁重复推荐同类概念**（包括换个马甲重写标题）；
   - `active_topics`：用户尚未学完的待学卡片 —— 若本周期发现强相关新证据，保持标题一致让后端自动合并证据，而不是制造重复卡。

---

## Phase 2 · 必须做真实查证（禁止只看标题瞎猜！）
拿到证据包后，挑出最有潜力的 5~8 条线索，用你的原生工具做**实证深挖**：

1. **AI 代写黑盒侦察（`category: "blackbox_code"`）**：
   - 关注改动行数大、逻辑复杂（并发、状态同步、构建/打包参数、SQL 迁移、协议/鉴权、复杂 CSS/手势）的 Commit 或 Checkpoint。
   - **必做动作**：用 `git show --stat <sha>` 或直接读取目标项目的核心代码文件，确认真正的核心机制与潜在隐患。
   - **自问**：*「如果这段 AI 刚写完的代码明天在线上炸了，用户能不能一眼看懂它在干嘛、为什么这么写？」*
2. **高频卡点侦察（`category: "recurring_friction"`）**：
   - 关注消息轮次高、标题或片段中出现「为什么」「怎么还是」「报毒」「不生效」「冲突」「卡住」的会话。
   - **必做动作**：调用 `astrorder-agent sessions read --key <KEY> --limit 30` 查看来回拉扯的真实上下文，找出反复受挫背后的**底层心智模型缺口**。
3. **报错模式侦察（`category: "failure_pattern"`）**：
   - 关注失败任务与 GitLab CI 失败线索，必要时运行 `astrorder-agent gitlab pipelines` / `astrorder-agent gitlab log --job-id <ID>`。
   - **过滤噪音**：一次性拼写错误、临时网络抖动**不是**学习盲区；只有「同构错误反复发生」或「隐蔽底层机制导致的踩坑」才值得立卡。
4. **新涉足领域侦察（`category: "new_domain"`）**：
   - 从会议纪要、新引入库或跨栈尝试中，提炼值得建立清晰心智框架的新概念。

---

## Phase 3 · 精品制卡 4 项质量门禁（宁缺毋滥）

每次扫描只输出 **3 ~ 6 张最高价值卡片**（若近期确实安静或无实质盲区，可输出 1~2 张甚至 0 张并在 `summary` 如实说明，绝不凑数！）。

每张卡片必须通过以下 **4 项严格审查**：

### 门禁 1：钩子式具体标题（`title`）
- 长度控制在 14~32 字（适配手机端 2 行展示），最好采用**直击反直觉或具体因果的问句/断言**，可带 1~2 个简短行内反引号代码标识。
- ❌ 敷衍反例：`学习 Go 语言编译参数优化`、`React 状态管理最佳实践`
- ✅ 精品示范：`为什么移除 -s -w 能大幅降低 Windows Defender 误报？`、`React 19 useSyncExternalStore 如何防止跨标签页状态撕裂？`

### 门禁 2：真实工作现场钩子（`why_now`）
- 1~2 句（40~85 字），必须点出**具体项目名、具体文件名或近期发生的工作事件**，让用户一眼回忆起「哦！这就是我前两天碰到的事！」。

### 门禁 3：移动端 30 秒速懂因果链（`key_points`）
- 恰好 **3 ~ 4 条**，按 **「① 发生了什么/痛点 → ② 底层真实机制 → ③ 工程权衡与实操结论」** 递进编排；
- 每条 **25 ~ 55 字**，说大白话 + 精准技术锚点，严禁抄写冗长文档段落。

### 门禁 4：真检验自测题（`quiz_questions`）+ 100% 真实证据链（`evidence`）
- **2 ~ 3 道题**：必须基于用户项目的真实代码/配置出 **场景推演题 / 反例题 / 权衡题**（例如：「如果把 A 保留但去掉 B 会怎样？」「为何此处不能用 X 替代 Y？」）。
- ❌ 禁止背诵题：`什么是 PE 文件？`、`请简述 useSyncExternalStore 的定义`。
- 每题必须配有启发性 `hint`（点拨思考方向，不剧透答案）与清晰通透的 `answer`（50~120 字，直指根因）。
- **证据链 `evidence`**：至少 2 条真实证据，字段必须来自 `evidence-pack.json` 或你亲手查证的结果，严禁捏造任何路径或 ID。

---

## Phase 4 · 输出 JSON 校验与原子回写

将结果写入 `inbox/scan-<run-id>/radar-result.json`：

```json
{
  "run_id": "<RUN_ID>",
  "summary": "本周深入复盘了 2 个项目、6 段活跃会话与 14 笔提交，提炼出 4 个高价值盲区（2 个 AI 代写黑盒、1 个高频卡点、1 个报错模式）。",
  "topics": [
    {
      "title": "为什么移除 `-s -w` 能大幅降低 Windows Defender 误报？",
      "category": "blackbox_code",
      "priority": "high",
      "estimated_minutes": 8,
      "project_key": "astrorder",
      "project_name": "astrorder",
      "workspace": "/home/luwei/workspace/glwlg/astrorder",
      "connection_id": "",
      "why_now": "你在发布脚本 `scripts/desktop-wails.mjs` 中调整了 Go 编译参数并在会话「怎么会报毒啊」中追问了原因，但尚未系统梳理 PE 节区特征与签名机制。",
      "key_points": [
        "`-s -w` 会彻底剥离符号表与 DWARF 调试段，使 PE 二进制整体信息熵骤增，外观极像加壳木马。",
        "无 EV 代码签名的 Go 程序被剥离符号后，极易触发 Defender 启发式模型（如 Bearfoos.A!ml）误杀。",
        "保留符号表仅增加约 4~6MB 体积，却能完整保留 Go runtime 的合法导出结构，显著降低误报率。",
        "路径脱敏应单独保留 `-trimpath`，它在不破坏符号结构的前提下清理本机源码绝对路径。"
      ],
      "quiz_questions": [
        {
          "question": "在构建 `Astrorder.exe` 时，为什么可以安全保留 `-trimpath`，却不能加 `-s -w`？",
          "hint": "对比两者分别改动了二进制文件的哪一部分信息，以及对 PE 节区熵值的影响。",
          "answer": "`-trimpath` 只替换编译期记录的源码目录前缀字符串，不删除符号表与节区元数据；而 `-s -w` 会移除整段符号与调试信息，导致结构异常紧凑并推高启发式误报风险。"
        }
      ],
      "evidence": [
        {
          "kind": "session",
          "label": "会话「怎么会报毒啊」",
          "project_name": "astrorder",
          "workspace": "/home/luwei/workspace/glwlg/astrorder",
          "agent_id": "antigravity",
          "session_id": "<真实 session_id>",
          "timestamp": "2026-11-26T08:30:00Z",
          "snippet": "追问 Windows 构建产物为什么触发 Defender 误报"
        },
        {
          "kind": "file",
          "label": "scripts/desktop-wails.mjs",
          "project_name": "astrorder",
          "workspace": "/home/luwei/workspace/glwlg/astrorder",
          "file_path": "scripts/desktop-wails.mjs",
          "snippet": "构建参数移除 -s -w，保留 -trimpath"
        }
      ]
    }
  ]
}
```

### 回写前自检清单
- [ ] `run_id` 与派单完全一致；
- [ ] 所有 `category` 仅取 `blackbox_code` / `recurring_friction` / `failure_pattern` / `new_domain`；
- [ ] 无任何条目与 `excluded_topics` 撞车；
- [ ] 每张卡片的 `key_points`（3~4 条）、`quiz_questions`（2~3 题含 hint+answer）、`evidence`（≥2 条真实线索）均饱满且无捏造。

执行原子回写：
```bash
astrorder-agent learning ingest --run-id "<RUN_ID>" --file "<JSON路径>"
```
回写成功后，用精炼清晰的分点中文向用户汇报本次扫描结果（发现了哪几张卡、为什么最值得先看哪一张）。
