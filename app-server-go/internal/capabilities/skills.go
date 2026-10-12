package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

// skillsAgents are the names the skills CLI knows the three agents by.
var skillsAgents = []string{"codex", "hermes-agent", "grok"}

func (s *Service) skillsCLI(ctx context.Context, env envtarget.Env, args ...string) (string, error) {
	full := append([]string{"-y", "skills"}, args...)
	return s.cli(ctx, env, "npx", full...)
}

// ---- search -----------------------------------------------------------

type MarketSkill struct {
	Pkg      string `json:"pkg"`
	Name     string `json:"name"`
	Repo     string `json:"repo"`
	Installs string `json:"installs"`
	URL      string `json:"url"`
}

type SearchResult struct {
	Query string        `json:"query"`
	Items []MarketSkill `json:"items"`
	Error string        `json:"error,omitempty"`
}

func formatInstalls(n float64) string {
	switch {
	case n >= 1e6:
		return fmt.Sprintf("%.1fM installs", n/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fK installs", n/1e3)
	}
	return fmt.Sprintf("%.0f installs", n)
}

// Search queries the skills.sh registry directly; no Node needed.
func (s *Service) Search(ctx context.Context, query string) SearchResult {
	query = strings.TrimSpace(query)
	result := SearchResult{Query: query, Items: []MarketSkill{}}
	if query == "" {
		return result
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, SearchURL+"?q="+url.QueryEscape(query)+"&limit=30", nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	request.Header.Set("Accept", "application/json")
	client := *s.HTTP
	client.Timeout = 12 * time.Second
	response, err := client.Do(request)
	if err != nil {
		result.Error = "skills.sh 不可达：" + err.Error()
		return result
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("skills.sh 返回 HTTP %d", response.StatusCode)
		return result
	}
	var payload struct {
		Skills []struct {
			ID       string  `json:"id"`
			Source   string  `json:"source"`
			SkillID  string  `json:"skillId"`
			Name     string  `json:"name"`
			Installs float64 `json:"installs"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Error = "skills.sh 返回了无法解析的内容"
		return result
	}
	for _, item := range payload.Skills {
		name := firstNonEmpty(item.SkillID, item.Name)
		if item.Source == "" || name == "" {
			continue
		}
		result.Items = append(result.Items, MarketSkill{Pkg: item.Source + "@" + name, Name: name, Repo: item.Source,
			Installs: formatInstalls(item.Installs), URL: "https://skills.sh/" + item.ID})
	}
	return result
}

// SearchURL is the registry endpoint; tests point it elsewhere.
var SearchURL = "https://skills.sh/api/search"

// ---- install / remove -------------------------------------------------

type SkillInstallResult struct {
	TargetID string `json:"target_id"`
	Success  bool   `json:"success"`
	Package  string `json:"package"`
	Skill    string `json:"skill,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Service) InstallSkill(ctx context.Context, targetID, pkg, skill string) SkillInstallResult {
	result := SkillInstallResult{TargetID: targetID, Package: pkg, Skill: skill}
	fail := func(err error) SkillInstallResult {
		_, result.Error = statusOf(err)
		return result
	}
	if !safeSource(pkg) {
		return fail(envtarget.Errorf(422, "技能包地址无效（支持 owner/repo、owner/repo@skill 或 Git 地址）"))
	}
	if skill != "" && !skillNameRE.MatchString(skill) {
		return fail(envtarget.Errorf(422, "技能名称无效"))
	}
	env, err := s.open(ctx, targetID)
	if err != nil {
		return fail(err)
	}
	if _, ok := env.Which(ctx, "npx"); !ok {
		return fail(envtarget.Errorf(404, "%s 未安装 Node.js（缺少 npx），无法安装技能", env.Target().Name))
	}
	args := append([]string{"add", pkg, "-g", "-y", "-a"}, skillsAgents...)
	if skill != "" {
		args = append(args, "--skill", skill)
	}
	defer s.lock(env.Target().ID + "|skills")()
	out, err := s.skillsCLI(ctx, env, args...)
	result.Output = tail(out, 4000)
	if err != nil {
		return fail(err)
	}
	if skill != "" {
		skills, listErr := s.TargetSkillList(ctx, targetID)
		if listErr == nil && !hasSkill(skills, skill) {
			return fail(envtarget.Errorf(502, "安装命令已结束，但在 %s 上没有找到技能 %s", env.Target().Name, skill))
		}
	}
	result.Success = true
	return result
}

func hasSkill(skills []Skill, name string) bool {
	for _, skill := range skills {
		if skill.Name == name {
			return true
		}
	}
	return false
}

type SkillRemoveResult struct {
	TargetID string `json:"target_id"`
	Success  bool   `json:"success"`
	Skill    string `json:"skill"`
	Output   string `json:"output,omitempty"`
}

// RemoveSkill uninstalls a skill. Skills the skills CLI manages go through it;
// the rest are moved into the owning agent's .archive so nothing is lost.
func (s *Service) RemoveSkill(ctx context.Context, targetID, name string) (*SkillRemoveResult, error) {
	if !skillNameRE.MatchString(name) {
		return nil, envtarget.Errorf(422, "技能名称无效")
	}
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	defer s.lock(env.Target().ID + "|skills")()
	sn, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	skills, err := sn.discoverSkills(ctx)
	if err != nil {
		return nil, err
	}
	var target *Skill
	for i := range skills {
		if skills[i].Name == name {
			target = &skills[i]
		}
	}
	if target == nil {
		return nil, envtarget.Errorf(404, "%s 上没有技能 %s", env.Target().Name, name)
	}
	result := &SkillRemoveResult{TargetID: targetID, Skill: name}
	if asMap(asMap(sn.lock["skills"])[name]) != nil {
		if _, ok := env.Which(ctx, "npx"); !ok {
			return nil, envtarget.Errorf(404, "%s 未安装 Node.js（缺少 npx），无法卸载技能", env.Target().Name)
		}
		out, cliErr := s.skillsCLI(ctx, env, "remove", name, "-g", "-y")
		result.Output = tail(out, 4000)
		if cliErr != nil {
			return nil, cliErr
		}
	}
	after, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	left, err := after.discoverSkillsDetailed(ctx)
	if err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	archived := 0
	for _, location := range left[name] {
		dir := path.Dir(location.path)
		if hiddenSegment(strings.TrimPrefix(dir, location.root+"/")) {
			continue
		}
		if err := env.Rename(ctx, dir, location.root+"/.archive/"+path.Base(dir)+"-"+stamp); err != nil {
			return nil, err
		}
		archived++
	}
	if archived > 0 {
		result.Output = strings.TrimSpace(result.Output + fmt.Sprintf("\n已将 %d 处原生技能目录移入各 Agent 的 .archive", archived))
	}
	final, err := s.TargetSkillList(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if hasSkill(final, name) {
		return nil, envtarget.Errorf(502, "卸载后 %s 上仍能找到技能 %s", env.Target().Name, name)
	}
	result.Success = true
	return result, nil
}

type skillLocation struct{ path, root string }

// discoverSkillsDetailed maps a skill name to every SKILL.md that defines it.
func (sn *snapshot) discoverSkillsDetailed(ctx context.Context) (map[string][]skillLocation, error) {
	roots := sn.skillRoots()
	dirs := make([]string, len(roots))
	for i, root := range roots {
		dirs[i] = root.dir
	}
	found, err := sn.env.Find(ctx, dirs, "SKILL.md", 5, 40)
	if err != nil {
		return nil, err
	}
	out := map[string][]skillLocation{}
	for _, hit := range found {
		best := ""
		for _, root := range roots {
			if strings.HasPrefix(hit.Path, root.dir+"/") && len(root.dir) > len(best) {
				best = root.dir
			}
		}
		if best == "" || hiddenSegment(strings.TrimPrefix(hit.Path, best+"/")) {
			continue
		}
		name, _ := parseFrontMatter(hit.Head, path.Base(path.Dir(hit.Path)))
		out[name] = append(out[name], skillLocation{hit.Path, best})
	}
	for name := range out {
		sort.Slice(out[name], func(i, j int) bool { return out[name][i].path < out[name][j].path })
	}
	return out, nil
}
