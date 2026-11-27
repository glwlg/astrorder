package learning

import _ "embed"

const (
	mentorTutoringSkillSlug = "feynman-code-tutoring"
	mentorVisualSkillSlug   = "visual-explainer"
)

//go:embed templates/AGENTS.md
var mentorAgentsMDTemplate string

//go:embed templates/learning-radar-mentor.SKILL.md
var mentorSkillMDTemplate string

//go:embed templates/feynman-code-tutoring.SKILL.md
var mentorTutoringSkillMDTemplate string

//go:embed templates/visual-explainer.SKILL.md
var mentorVisualSkillMDTemplate string

// mentorExtraSkills lists additional modular skills bundled into the official Learning Mentor Bot.
var mentorExtraSkills = []struct {
	Slug    string
	Content string
}{
	{Slug: mentorTutoringSkillSlug, Content: mentorTutoringSkillMDTemplate},
	{Slug: mentorVisualSkillSlug, Content: mentorVisualSkillMDTemplate},
}
