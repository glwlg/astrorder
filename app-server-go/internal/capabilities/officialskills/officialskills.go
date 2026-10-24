package officialskills

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

//go:embed astrorder-sessions/SKILL.md
var sessionsSkillMD []byte

//go:embed astrorder-browser/SKILL.md
var browserSkillMD []byte

//go:embed astrorder-blackboard/SKILL.md
var blackboardSkillMD []byte

//go:embed astrorder-swarm/SKILL.md
var swarmSkillMD []byte

//go:embed astrorder-workspace/SKILL.md
var workspaceSkillMD []byte

//go:embed astrorder-monitor/SKILL.md
var monitorSkillMD []byte

type OfficialSkill struct {
	Name                  string   `json:"name"`
	DisplayName           string   `json:"display_name"`
	Description           string   `json:"description"`
	Category              string   `json:"category"`
	Source                string   `json:"source"`
	Scope                 string   `json:"scope"`
	Agent                 string   `json:"agent"`
	Agents                []string `json:"agents"`
	Content               string   `json:"content"`
	RequiresCallerBinding bool     `json:"requires_caller_binding"`
	RequiresCLI           bool     `json:"requires_cli"`
	SupportedAgents       []string `json:"supported_agents"`
}

var officialCatalog = []OfficialSkill{
	{
		Name:                  "astrorder-sessions",
		DisplayName:           "会话管理",
		Description:           "Astrorder 多 Agent 会话管理与协作调度 (sessions spawn/read/send/tree/list)",
		Category:              "astrorder",
		Source:                "astrorder",
		Scope:                 "builtin",
		Agent:                 "astrorder",
		Agents:                []string{},
		Content:               string(sessionsSkillMD),
		RequiresCallerBinding: true,
		RequiresCLI:           true,
		SupportedAgents:       []string{"codex", "hermes", "grok"},
	},
	{
		Name:                  "astrorder-browser",
		DisplayName:           "浏览器控制",
		Description:           "Astrorder 会话浏览器脚本求值与 CDP 控制 (browser eval / browser.cdp)",
		Category:              "astrorder",
		Source:                "astrorder",
		Scope:                 "builtin",
		Agent:                 "astrorder",
		Agents:                []string{},
		Content:               string(browserSkillMD),
		RequiresCallerBinding: true,
		RequiresCLI:           true,
		SupportedAgents:       []string{"codex", "hermes", "grok"},
	},
	{
		Name:                  "astrorder-blackboard",
		DisplayName:           "作战黑板",
		Description:           "Astrorder 共享黑板读写、Generative UI 组件与自动渲染 (blackboard.*)",
		Category:              "astrorder",
		Source:                "astrorder",
		Scope:                 "builtin",
		Agent:                 "astrorder",
		Agents:                []string{},
		Content:               string(blackboardSkillMD),
		RequiresCallerBinding: true,
		RequiresCLI:           true,
		SupportedAgents:       []string{"codex", "hermes", "grok"},
	},
	{
		Name:                  "astrorder-swarm",
		DisplayName:           "群体协同",
		Description:           "Astrorder 资源锁、里程碑、遥测与 SOS 协同 (swarm.*)",
		Category:              "astrorder",
		Source:                "astrorder",
		Scope:                 "builtin",
		Agent:                 "astrorder",
		Agents:                []string{},
		Content:               string(swarmSkillMD),
		RequiresCallerBinding: true,
		RequiresCLI:           true,
		SupportedAgents:       []string{"codex", "hermes", "grok"},
	},
	{
		Name:                  "astrorder-workspace",
		DisplayName:           "工作区与插件",
		Description:           "Astrorder 实例拓扑发现与插件控制 (agents/machines/projects/plugins)",
		Category:              "astrorder",
		Source:                "astrorder",
		Scope:                 "builtin",
		Agent:                 "astrorder",
		Agents:                []string{},
		Content:               string(workspaceSkillMD),
		RequiresCallerBinding: true,
		RequiresCLI:           true,
		SupportedAgents:       []string{"codex", "hermes", "grok"},
	},
	{
		Name:                  "astrorder-monitor",
		DisplayName:           "监控墙",
		Description:           "Astrorder 监控墙会话编组与网格布局 (monitor.sessions/layout)",
		Category:              "astrorder",
		Source:                "astrorder",
		Scope:                 "builtin",
		Agent:                 "astrorder",
		Agents:                []string{},
		Content:               string(monitorSkillMD),
		RequiresCallerBinding: true,
		RequiresCLI:           true,
		SupportedAgents:       []string{"codex", "hermes", "grok"},
	},
}

// ListOfficialSkills returns all official Astrorder builtin skills.
func ListOfficialSkills() []OfficialSkill {
	out := make([]OfficialSkill, len(officialCatalog))
	for i, skill := range officialCatalog {
		out[i] = cloneSkill(skill)
	}
	return out
}

// GetOfficialSkill finds an official skill by its name.
func GetOfficialSkill(name string) (*OfficialSkill, bool) {
	for i := range officialCatalog {
		if officialCatalog[i].Name == name {
			skill := cloneSkill(officialCatalog[i])
			return &skill, true
		}
	}
	return nil, false
}

// RenderOfficialSkill renders template variables inside the skill content.
func RenderOfficialSkill(skill OfficialSkill, home string) []byte {
	content := skill.Content
	callerBash := path.Join(strings.ReplaceAll(home, `\`, "/"), "astrorder-caller.json")
	callerWin := strings.ReplaceAll(callerBash, "/", `\`)
	content = strings.ReplaceAll(content, "{{CALLER_CONFIG_PATH}}", callerBash)
	content = strings.ReplaceAll(content, "{{CALLER_CONFIG_PATH_WIN}}", callerWin)
	return []byte(content)
}

func cloneSkill(skill OfficialSkill) OfficialSkill {
	skill.Agents = append([]string{}, skill.Agents...)
	skill.SupportedAgents = append([]string{}, skill.SupportedAgents...)
	return skill
}

// OfficialCatalogJSON serializes the official catalog into JSON for fixtures and UI exports.
func OfficialCatalogJSON() ([]byte, error) {
	return json.MarshalIndent(officialCatalog, "", "  ")
}

// String provides a human-readable catalog summary.
func (s OfficialSkill) String() string {
	return fmt.Sprintf("%s (%s): %s", s.Name, s.DisplayName, s.Description)
}
