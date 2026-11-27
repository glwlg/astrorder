package digest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/store"
)

const prefDigestTemplates = "digest:templates"

// DigestTemplate defines the structure, tone, and audience rules for generating a report.
type DigestTemplate struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Audience     string `json:"audience"`
	BuiltIn      bool   `json:"built_in"`
	SystemPrompt string `json:"system_prompt"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

func builtinTemplates() []DigestTemplate {
	return []DigestTemplate{
		{
			ID:          "weekly_exec",
			Name:        "周报 · 汇报版",
			Description: "面向管理者与跨团队同步：结论先行、按项目汇总成果、突出量化指标、风险阻塞与下周计划。",
			Audience:    "管理者 / 跨部门同步",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`你是一位资深研发与项目效能助理。请根据提供的真实工作素材，撰写一份清晰、专业、可直接提交给管理层的中文工作周报（Markdown 格式）。
要求：
1. 结论先行：开篇用 3-5 条「本周核心成果」概括最高价值的交付，尽量用素材中的真实数字支撑（如项目数、功能交付、修复缺陷、上线验证等），严禁编造素材中不存在的事实或指标。
2. 分项目进展：按项目归纳关键进展与业务/工程价值，合并琐碎过程记录，提炼为成果与里程碑；如有表格数据可生成简洁的 Markdown 进度总览表。
3. 风险与阻塞：提炼仍未完成或需要协调的事项（若素材中无明显风险，注明“暂无重大阻塞”）。
4. 下周计划：结合进行中的任务与会话上下文给出 3-5 条具体可落地的下周重点。
5. 语言简洁克制，避免堆砌底层命令行细节或代码片段。`),
		},
		{
			ID:          "daily_brief",
			Name:        "日报 · 精简版",
			Description: "适合每日站会与团队群同步：今日完成、进行中、明日计划、需协调事项，控制在 1 分钟可读完。",
			Audience:    "团队日常同步",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`请根据提供的真实工作素材，输出一份精炼的中文工作日报（Markdown 格式）。
结构固定为四部分：
## 今日完成
- 按项目列出已完成的具体事项与结果（每条不超过 2 句话）
## 进行中
- 列出尚未收尾的工作与当前状态
## 明日计划
- 列出接下来的 2-4 项优先任务
## 需协调 / 阻塞
- 若无则写“无”

要求：严禁编造数据；去除冗长排查过程，只保留明确产出与状态。`),
		},
		{
			ID:          "tech_retro",
			Name:        "技术复盘版",
			Description: "面向工程团队与架构沉淀：目标背景、关键设计与代码变更、根因与修复、验证结果及遗留技术债。",
			Audience:    "研发 / 运维团队",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`请根据提供的真实会话、Git 提交、代码变更与数据库执行记录，撰写一份结构化的中文技术复盘文档（Markdown 格式）。
包含以下章节：
1. 概览与目标背景
2. 关键架构 / 代码与数据库变更（可附项目变更统计表）
3. 重点问题根因与修复方案
4. 验证与交付情况
5. 遗留技术债与后续改进建议

要求：基于素材准确表述模块、变更范围与验证方式；避免虚构未发生的测试或指标。`),
		},
		{
			ID:          "external_sanitized",
			Name:        "对外脱敏版",
			Description: "面向客户、合作方或外部汇报：严格隐去内网 IP、本机路径、库名/表名/账号与敏感内部代号。",
			Audience:    "客户 / 外部合作方",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`请根据提供的工作素材，撰写一份可面向外部客户或合作方发送的中文项目进展汇报（Markdown 格式）。
【强制脱敏规则】：
- 严禁出现任何内网/公网 IP 地址、端口号、服务器主机名、本机文件系统路径（如 /home/...、C:\...）。
- 严禁出现具体数据库名、数据表名、连接账号、密钥、内部环境标识（如 PROD/预发内部地址）或未公开内部代号。
- 将底层代码改动与运维操作转化为客户可理解的「功能交付」「体验优化」「稳定性提升」表述。

结构建议：
1. 本期核心交付亮点
2. 分模块进展与客户价值（可用简洁状态表）
3. 下阶段重点安排`),
		},
		{
			ID:          "office_weekly",
			Name:        "办公周报 · 通用版",
			Description: "面向业务、产品、运营与职能团队：按项目总结文档产出、会议决议、沟通进展与下周行动项，零研发术语。",
			Audience:    "业务 / 职能 / 管理层",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`你是一位专业、严谨的办公效率与业务助理。请根据提供的真实办公素材（会议纪要、文档更新、对话讨论与补充事项），撰写一份清晰、得体、可直接提交的中文办公周报（Markdown 格式）。
要求：
1. 严禁编造素材中不存在的数字、人名或进度，也不要堆砌底层代码、分支或命令行术语。
2. 开篇用「本周核心成果」列出 3-5 条最高价值的交付与推进结果。
3. 「重点工作与会议推进」按工作空间或主题分点归纳文档产出、会议达成决议及跨部门协作状态；如有多条事项可附简洁 Markdown 进度表。
4. 「待办与风险协调」清晰列出尚未关闭的行动项、负责人/截止时间（如有）及需协助解决的卡点。
5. 「下周重点安排」给出 3-5 条具体、可执行的下周目标。`),
		},
		{
			ID:          "okr_review",
			Name:        "OKR / 目标复盘",
			Description: "面向月度或季度复盘：按关键目标对齐产出证据、亮点经验、偏差分析与下一阶段改进动作。",
			Audience:    "团队目标复盘 / 述职",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`请根据提供的真实工作证据（文档交付、会议决议、任务完成情况与讨论总结），撰写一份结构化的中文 OKR / 阶段性目标复盘报告（Markdown 格式）。
包含以下章节：
1. 阶段目标与整体达成概览（结合素材提炼核心量化或里程碑成果）
2. 分事项产出与关键证据（清晰呈现文档产出、项目推进与会议共识）
3. 亮点经验与有效做法
4. 偏差、未达成项与原因分析（客观克制，聚焦改进点）
5. 下阶段关键行动计划（建议表格列明：事项、预期结果、时间节点）

要求：完全基于真实素材撰写，不虚构未发生的成果。`),
		},
		{
			ID:          "client_progress",
			Name:        "客户项目进展同步",
			Description: "面向客户与合作伙伴的阶段同步简报：强调里程碑、交付物清单、需客户确认事项与下一步排期。",
			Audience:    "客户对接 / 合作方",
			BuiltIn:     true,
			SystemPrompt: strings.TrimSpace(`请根据提供的真实工作素材，撰写一份专业、礼貌、可直接发送给客户或外部合作方的中文项目进展同步邮件/简报（Markdown 格式）。
要求：
1. 自动过滤内部敏感信息（内网地址、本机路径、内部系统账号及内部争论细节）。
2. 结构包含：
   - **本期进展概览**（2-3 句话总结当前状态）
   - **已完成交付与关键成果**（列出核心文档、方案、会议确认结论）
   - **需贵方确认 / 配合事项**（清晰列出待确认项与建议时间）
   - **下阶段工作计划与排期**
3. 语气积极专业、条理清晰，便于对方快速阅读并回复确认。`),
		},
	}
}

func isBuiltinTemplateID(id string) bool {
	for _, tpl := range builtinTemplates() {
		if tpl.ID == id {
			return true
		}
	}
	return false
}

func (h *handler) loadCustomTemplates(ctx context.Context) ([]DigestTemplate, error) {
	if h.store == nil {
		return nil, nil
	}
	pref, err := h.store.GetPreference(ctx, prefDigestTemplates)
	if err != nil || !pref.Exists || pref.Value == nil {
		return nil, err
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return nil, err
	}
	var list []DigestTemplate
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil
	}
	out := make([]DigestTemplate, 0, len(list))
	for _, item := range list {
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		if item.ID == "" || item.Name == "" || isBuiltinTemplateID(item.ID) {
			continue
		}
		item.BuiltIn = false
		out = append(out, item)
	}
	return out, nil
}

func (h *handler) listAllTemplates(ctx context.Context) ([]DigestTemplate, error) {
	all := builtinTemplates()
	custom, err := h.loadCustomTemplates(ctx)
	if err != nil {
		return all, err
	}
	return append(all, custom...), nil
}

func (h *handler) findTemplate(ctx context.Context, id string) DigestTemplate {
	all, _ := h.listAllTemplates(ctx)
	trimmed := strings.TrimSpace(id)
	for _, t := range all {
		if t.ID == trimmed {
			return t
		}
	}
	return builtinTemplates()[0]
}

func (h *handler) saveCustomTemplate(ctx context.Context, tpl DigestTemplate) (DigestTemplate, error) {
	if h.store == nil {
		return DigestTemplate{}, errors.New("store unavailable")
	}
	tpl.Name = strings.TrimSpace(tpl.Name)
	tpl.SystemPrompt = strings.TrimSpace(tpl.SystemPrompt)
	tpl.Description = strings.TrimSpace(tpl.Description)
	tpl.Audience = strings.TrimSpace(tpl.Audience)
	if tpl.Name == "" {
		return DigestTemplate{}, errors.New("模板名称不能为空")
	}
	if tpl.SystemPrompt == "" {
		return DigestTemplate{}, errors.New("模板提示词不能为空")
	}
	tpl.ID = strings.TrimSpace(tpl.ID)
	if tpl.ID == "" {
		tpl.ID = "custom_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000"), ".", "")
	}
	if isBuiltinTemplateID(tpl.ID) {
		return DigestTemplate{}, errors.New("内置模板不可覆盖，请另存为自定义模板")
	}
	tpl.BuiltIn = false
	tpl.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.loadCustomTemplates(ctx)
	if err != nil {
		return DigestTemplate{}, err
	}
	replaced := false
	for i := range list {
		if list[i].ID == tpl.ID {
			list[i] = tpl
			replaced = true
			break
		}
	}
	if !replaced {
		if len(list) >= 30 {
			return DigestTemplate{}, errors.New("自定义模板数量已达上限 (30)")
		}
		list = append(list, tpl)
	}
	if err := h.store.SetPreference(ctx, prefDigestTemplates, list, true); err != nil {
		return DigestTemplate{}, err
	}
	return tpl, nil
}

func (h *handler) deleteCustomTemplate(ctx context.Context, id string) error {
	if h.store == nil {
		return errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("缺少模板 id")
	}
	if isBuiltinTemplateID(id) {
		return errors.New("内置模板不可删除")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list, err := h.loadCustomTemplates(ctx)
	if err != nil {
		return err
	}
	next := make([]DigestTemplate, 0, len(list))
	for _, item := range list {
		if item.ID != id {
			next = append(next, item)
		}
	}
	return h.store.SetPreference(ctx, prefDigestTemplates, next, true)
}

func loadLLMConfig(ctx context.Context, st *store.Store) aiprovider.ResolvedModel {
	return aiprovider.ResolveDefault(ctx, st)
}
