package officepersona

import (
	"context"
	"encoding/json"
	"strings"

	"astrorder.dev/app-server/internal/store"
)

const PreferenceKey = "office:persona"

// OfficePersona stores the user's workplace identity, audience, writing style,
// domain vocabulary, and privacy rules so every office workflow (drafting,
// refining, weekly reports, meeting minutes, and chat templates) automatically
// matches their personal voice.
type OfficePersona struct {
	DisplayName     string `json:"display_name"`
	Role            string `json:"role"`
	Department      string `json:"department"`
	DefaultAudience string `json:"default_audience"`
	WritingStyle    string `json:"writing_style"` // concise_exec | formal_doc | friendly_sync | client_facing
	ToneRules       string `json:"tone_rules"`
	Glossary        string `json:"glossary"`
	AvoidWords      string `json:"avoid_words"`
	AutoInjectChat  bool   `json:"auto_inject_chat"`
}

func Default() OfficePersona {
	return OfficePersona{
		WritingStyle:   "concise_exec",
		AutoInjectChat: true,
	}
}

func Normalize(p OfficePersona) OfficePersona {
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	p.Role = strings.TrimSpace(p.Role)
	p.Department = strings.TrimSpace(p.Department)
	p.DefaultAudience = strings.TrimSpace(p.DefaultAudience)
	p.ToneRules = strings.TrimSpace(p.ToneRules)
	p.Glossary = strings.TrimSpace(p.Glossary)
	p.AvoidWords = strings.TrimSpace(p.AvoidWords)
	switch strings.TrimSpace(p.WritingStyle) {
	case "concise_exec", "formal_doc", "friendly_sync", "client_facing":
		p.WritingStyle = strings.TrimSpace(p.WritingStyle)
	default:
		p.WritingStyle = "concise_exec"
	}
	return p
}

func Load(ctx context.Context, st *store.Store) OfficePersona {
	def := Default()
	if st == nil {
		return def
	}
	pref, err := st.GetPreference(ctx, PreferenceKey)
	if err != nil || !pref.Exists || pref.Value == nil {
		return def
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return def
	}
	out := def
	if err := json.Unmarshal(raw, &out); err != nil {
		return def
	}
	return Normalize(out)
}

func Save(ctx context.Context, st *store.Store, incoming OfficePersona) (OfficePersona, error) {
	clean := Normalize(incoming)
	if st != nil {
		if err := st.SetPreference(ctx, PreferenceKey, clean, false); err != nil {
			return clean, err
		}
	}
	return clean, nil
}

func StyleDescription(style string) string {
	switch style {
	case "formal_doc":
		return "正式公文风格：措辞严谨规范、层级分明、客观庄重，适合正式通知、汇报材料与制度文件。"
	case "friendly_sync":
		return "清晰务实风格：口吻自然直接、重点突出、便于跨部门与团队内部高效对齐。"
	case "client_facing":
		return "对外商务风格：专业得体、礼貌克制、强调客户价值与交付保障，自动规避内部技术细节。"
	default:
		return "结论先行风格：面向管理层，开篇直给核心结论与量化结果，行文精炼紧凑。"
	}
}

// PromptSection renders a compact system-prompt addendum when the user has
// configured any personal office persona details. Returns empty string when blank.
func PromptSection(p OfficePersona) string {
	p = Normalize(p)
	hasCustom := p.DisplayName != "" || p.Role != "" || p.Department != "" ||
		p.DefaultAudience != "" || p.ToneRules != "" || p.Glossary != "" ||
		p.AvoidWords != "" || p.WritingStyle != "concise_exec"
	if !hasCustom {
		return ""
	}
	var b strings.Builder
	b.WriteString("【用户办公画像与写作偏好（请自然遵循，不要复述此说明）】\n")
	if p.DisplayName != "" || p.Role != "" || p.Department != "" {
		parts := make([]string, 0, 3)
		if p.DisplayName != "" {
			parts = append(parts, "称呼："+p.DisplayName)
		}
		if p.Department != "" {
			parts = append(parts, "部门："+p.Department)
		}
		if p.Role != "" {
			parts = append(parts, "岗位："+p.Role)
		}
		b.WriteString("- 身份：" + strings.Join(parts, " / ") + "\n")
	}
	if p.DefaultAudience != "" {
		b.WriteString("- 常见汇报/沟通对象：" + p.DefaultAudience + "\n")
	}
	b.WriteString("- 默认行文风格：" + StyleDescription(p.WritingStyle) + "\n")
	if p.ToneRules != "" {
		b.WriteString("- 个性化表达要求：" + p.ToneRules + "\n")
	}
	if p.Glossary != "" {
		b.WriteString("- 常用专有名词与术语（保持准确写法）：" + p.Glossary + "\n")
	}
	if p.AvoidWords != "" {
		b.WriteString("- 禁用词/需自动脱敏的内容：" + p.AvoidWords + "\n")
	}
	return strings.TrimSpace(b.String())
}
