package capabilities

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/capabilities/officialskills"
	"astrorder.dev/app-server/internal/envtarget"
)

type MCPServer struct {
	Name      string            `json:"name"`
	Agent     string            `json:"agent"`
	Enabled   bool              `json:"enabled"`
	Transport string            `json:"transport"`
	URL       string            `json:"url,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args"`
	Headers   map[string]string `json:"headers"`
	Env       map[string]string `json:"env"`
	Tools     []string          `json:"tools"`
	Source    string            `json:"source"`
}

type Plugin struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	FullName    string         `json:"full_name"`
	Agent       string         `json:"agent"`
	Marketplace string         `json:"marketplace,omitempty"`
	Enabled     bool           `json:"enabled"`
	Source      string         `json:"source"`
	Config      map[string]any `json:"config"`
}

type Marketplace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Agent       string `json:"agent"`
	SourceType  string `json:"source_type"`
	Source      string `json:"source"`
	LastUpdated string `json:"last_updated,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Description string `json:"description,omitempty"`
}

type Skill struct {
	InstalledAgents []string `json:"installed_agents,omitempty"`
	Category        string   `json:"category,omitempty"`
	Content         string   `json:"content,omitempty"`
	DisplayName     string   `json:"display_name,omitempty"`
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	Scope           string   `json:"scope"`
	Agents          []string `json:"agents"`
	Agent           string   `json:"agent"`
	Source          string   `json:"source"`
	SourceURL       string   `json:"sourceUrl,omitempty"`
	Description     string   `json:"description"`
}

type Summary struct {
	SkillAgents  []SkillAgent   `json:"skill_agents"`
	TargetID     string         `json:"target_id"`
	TargetName   string         `json:"target_name"`
	Kind         string         `json:"kind"`
	Counts       map[string]int `json:"counts"`
	Skills       []Skill        `json:"skills"`
	MCPServers   []MCPServer    `json:"mcp_servers"`
	Plugins      []Plugin       `json:"plugins"`
	Marketplaces []Marketplace  `json:"marketplaces"`
	Warnings     []string       `json:"warnings,omitempty"`
}

// snapshot is what one machine currently has configured.
type snapshot struct {
	env   envtarget.Env
	paths envtarget.Paths
	cfg   map[string]map[string]any // agent -> parsed config
	lock  map[string]any            // ~/.agents/.skill-lock.json
	warns []string
}

func configPath(p envtarget.Paths, agent string) string {
	switch agent {
	case "codex":
		return p.Codex + "/config.toml"
	case "hermes":
		return p.Hermes + "/config.yaml"
	}
	return p.Grok + "/config.toml"
}

func (s *Service) snapshot(ctx context.Context, env envtarget.Env) (*snapshot, error) {
	paths, err := env.Paths(ctx)
	if err != nil {
		return nil, err
	}
	files := []string{configPath(paths, "codex"), configPath(paths, "hermes"), configPath(paths, "grok"), paths.Agents + "/.skill-lock.json"}
	raw, err := env.ReadMany(ctx, files)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{env: env, paths: paths, cfg: map[string]map[string]any{}, lock: map[string]any{}}
	for _, agent := range agents {
		file := configPath(paths, agent)
		cfg, parseErr := parseConfig(agent, stripBOM(raw[file]))
		if parseErr != nil {
			snap.warns = append(snap.warns, agent+" 配置无法解析："+parseErr.Error())
			cfg = map[string]any{}
		}
		snap.cfg[agent] = cfg
	}
	if data := raw[paths.Agents+"/.skill-lock.json"]; len(data) > 0 {
		_ = json.Unmarshal(data, &snap.lock)
	}
	return snap, nil
}

func stripBOM(data []byte) []byte {
	return []byte(strings.TrimPrefix(string(data), "\ufeff"))
}

func transportOf(url string) string {
	if url != "" {
		return "sse/http"
	}
	return "stdio"
}

func headersKey(agent string) string {
	if agent == "codex" {
		return "http_headers"
	}
	return "headers"
}

// mcpEntries returns the raw MCP server definitions of one agent.
func (sn *snapshot) mcpEntries(agent string) map[string]any {
	return asMap(sn.cfg[agent]["mcp_servers"])
}

// disabledMCP lists Grok servers switched off through disabled_mcp_servers.
func (sn *snapshot) disabledMCP(agent string) []string {
	if agent != "grok" {
		return nil
	}
	return asStrings(sn.cfg[agent]["disabled_mcp_servers"])
}

func mcpFromEntry(agent, name string, entry map[string]any, disabled []string, redact bool) MCPServer {
	url := asString(entry["url"])
	server := MCPServer{
		Name: name, Agent: agent, Transport: transportOf(url), URL: url, Command: asString(entry["command"]),
		Args: asStrings(entry["args"]), Headers: asStringMap(entry[headersKey(agent)]), Env: asStringMap(entry["env"]),
		Tools: []string{}, Source: agent, Enabled: asBool(entry["enabled"], true) && !contains(disabled, name),
	}
	if redact {
		server.Headers, server.Env = redactMap(server.Headers), redactMap(server.Env)
	}
	return server
}

func (sn *snapshot) mcpServers(agent string, redact bool) []MCPServer {
	entries := sn.mcpEntries(agent)
	out := make([]MCPServer, 0, len(entries))
	for _, name := range sortedAnyKeys(entries) {
		out = append(out, mcpFromEntry(agent, name, asMap(entries[name]), sn.disabledMCP(agent), redact))
	}
	return out
}

func (sn *snapshot) plugins(ctx context.Context) []Plugin {
	var out []Plugin
	codexPlugins := asMap(sn.cfg["codex"]["plugins"])
	for _, full := range sortedAnyKeys(codexPlugins) {
		entry := asMap(codexPlugins[full])
		name, market, _ := strings.Cut(full, "@")
		out = append(out, Plugin{ID: "codex::" + full, Name: name, FullName: full, Agent: "codex", Marketplace: market,
			Enabled: asBool(entry["enabled"], true), Source: "codex-plugin", Config: asMapOrEmpty(redactAny(entry))})
	}
	hermes := asMap(sn.cfg["hermes"]["plugins"])
	enabled, disabled := asStrings(hermes["enabled"]), asStrings(hermes["disabled"])
	entries := asMap(hermes["entries"])
	names := map[string]bool{}
	for _, name := range append(append([]string{}, enabled...), disabled...) {
		names[name] = true
	}
	for name := range entries {
		names[name] = true
	}
	known := map[string]bool{}
	for name := range names {
		known[name] = true
	}
	if dirs, err := sn.env.ListDir(ctx, sn.paths.Hermes+"/plugins"); err == nil {
		for _, dir := range dirs {
			if dir.Dir && !strings.HasPrefix(dir.Name, ".") && dir.Name != "__pycache__" && !names[dir.Name] {
				names[dir.Name] = true
			}
		}
	}
	hermesNames := make([]string, 0, len(names))
	for name := range names {
		hermesNames = append(hermesNames, name)
	}
	sort.Strings(hermesNames)
	for _, name := range hermesNames {
		market := "hermes-official"
		if !known[name] {
			market = "local"
		}
		out = append(out, Plugin{ID: "hermes::" + name, Name: name, FullName: name, Agent: "hermes", Marketplace: market,
			Enabled: contains(enabled, name) || (!contains(disabled, name) && known[name]), Source: "hermes-plugin",
			Config: asMapOrEmpty(redactAny(entries[name]))})
	}
	for _, name := range asStrings(asMap(sn.cfg["grok"]["plugins"])["enabled"]) {
		out = append(out, Plugin{ID: "grok::" + name, Name: name, FullName: name, Agent: "grok", Marketplace: "xai-official",
			Enabled: true, Source: "grok-plugin", Config: map[string]any{}})
	}
	return out
}

func asMapOrEmpty(value any) map[string]any {
	if m := asMap(value); m != nil {
		return m
	}
	return map[string]any{}
}

func (sn *snapshot) marketplaces() []Marketplace {
	out := []Marketplace{{ID: "global::skills.sh", Name: "Vercel Skills 官方生态 (skills.sh)", Agent: "all", SourceType: "registry",
		Source: "https://skills.sh", Description: "全球最大的开源 Agent 技能市场，支持一键分发到各 Agent"}}
	markets := asMap(sn.cfg["codex"]["marketplaces"])
	for _, name := range sortedAnyKeys(markets) {
		entry := asMap(markets[name])
		kind := asString(entry["source_type"])
		if kind == "" {
			kind = "git"
		}
		out = append(out, Marketplace{ID: "codex::" + name, Name: name, Agent: "codex", SourceType: kind, Source: asString(entry["source"]),
			LastUpdated: asString(entry["last_updated"]), Ref: asString(entry["ref"]), Description: "Codex 已订阅的 " + kind + " 市场源"})
	}
	for _, item := range asAnyList(asMap(sn.cfg["grok"]["marketplace"])["sources"]) {
		entry := asMap(item)
		name := asString(entry["name"])
		if name == "" {
			name = "xAI Marketplace"
		}
		out = append(out, Marketplace{ID: "grok::" + name, Name: name, Agent: "grok", SourceType: "git", Source: asString(entry["git"]),
			Description: "Grok 已订阅的市场源"})
	}
	return out
}

func asAnyList(value any) []any {
	list, _ := value.([]any)
	return list
}

// ---- skills -----------------------------------------------------------

type skillRoot struct {
	dir   string
	agent string // owning agent, "" for the shared store
}

func (sn *snapshot) skillRoots() []skillRoot {
	roots := []skillRoot{
		{sn.paths.Codex + "/skills", "codex"},
		{sn.paths.Hermes + "/skills", "hermes"},
		{sn.paths.Grok + "/skills", "grok"},
		{sn.paths.Agents + "/skills", ""},
	}
	for _, dir := range asStrings(asMap(sn.cfg["hermes"]["skills"])["external_dirs"]) {
		dir = strings.TrimSpace(dir)
		if strings.HasPrefix(dir, "~/") {
			dir = sn.paths.Home + dir[1:]
		}
		if strings.HasPrefix(dir, "/") || (len(dir) > 2 && dir[1] == ':') {
			roots = append(roots, skillRoot{strings.ReplaceAll(dir, `\`, "/"), "hermes"})
		}
	}
	return roots
}

var frontKeyRE = regexp.MustCompile(`^(name|description):\s*(.*)$`)

func parseFrontMatter(head, fallback string) (name, description string) {
	name = fallback
	lines := strings.Split(strings.ReplaceAll(head, "\r\n", "\n"), "\n")
	for i, line := range lines {
		match := frontKeyRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		value := strings.TrimSpace(match[2])
		if match[1] == "description" && (value == ">" || value == "|" || value == ">-" || value == "|-") {
			for _, next := range lines[i+1:] {
				if trimmed := strings.TrimSpace(next); trimmed != "" {
					value = trimmed
					break
				}
			}
		}
		value = strings.Trim(value, `"'`)
		if match[1] == "name" && value != "" {
			name = value
		} else if match[1] == "description" {
			description = value
		}
	}
	return name, description
}

func (sn *snapshot) discoverSkills(ctx context.Context) ([]Skill, error) {
	roots := sn.skillRoots()
	dirs := make([]string, len(roots))
	for i, root := range roots {
		dirs[i] = root.dir
	}
	found, err := sn.env.Find(ctx, dirs, "SKILL.md", 5, 40)
	if err != nil {
		return nil, err
	}
	locked := asMap(sn.lock["skills"])
	byName := map[string]*Skill{}

	var order []string
	for _, hit := range found {
		var root *skillRoot
		for i := range roots {
			if strings.HasPrefix(hit.Path, roots[i].dir+"/") && (root == nil || len(roots[i].dir) > len(root.dir)) {
				root = &roots[i]
			}
		}
		if root == nil || hiddenSegment(strings.TrimPrefix(hit.Path, root.dir+"/")) {
			continue
		}
		dir := path.Dir(hit.Path)
		name, description := parseFrontMatter(hit.Head, path.Base(dir))
		managedOfficial := false
		if _, official := officialskills.GetOfficialSkill(name); official {
			if raw, err := sn.env.Read(ctx, path.Join(dir, officialManifestFile)); err == nil {
				var manifest officialManifest
				managedOfficial = json.Unmarshal(raw, &manifest) == nil && manifest.Owner == "astrorder-capabilities" && manifest.Version == 1 && manifest.Skill == name && manifest.Agent == root.agent && manifest.Files["SKILL.md"] != ""
			}
		}
		identity := name
		if managedOfficial {
			identity = "official::" + name
		}
		agent, source, sourceURL := root.agent, root.agent+"-native", ""
		agentList := []string{agent}
		if root.agent == "" {
			agent, source, agentList = "codex", "skills.sh", []string{"codex"}
			if entry := asMap(locked[name]); entry != nil {
				source, sourceURL = firstNonEmpty(asString(entry["source"]), source), asString(entry["sourceUrl"])
			}
		}
		if existing, ok := byName[identity]; ok {
			for _, a := range agentList {
				if !contains(existing.Agents, a) {
					existing.Agents = append(existing.Agents, a)
				}
			}
			if existing.Description == "" {
				existing.Description = description
			}
			continue
		}
		byName[identity] = &Skill{Name: name, Path: hit.Path, Scope: "global", Agents: agentList, Agent: agent, Source: source, SourceURL: sourceURL, Description: description}
		if managedOfficial {
			byName[identity].Source = "astrorder-installed"
		}
		order = append(order, identity)
	}
	sort.Strings(order)
	out := make([]Skill, 0, len(order))
	for _, name := range order {
		sort.Strings(byName[name].Agents)

		out = append(out, *byName[name])
	}
	return out, nil
}

func hiddenSegment(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ---- public reads -----------------------------------------------------

// Targets lists the machines capabilities can be managed on.
func (s *Service) TargetList(ctx context.Context) ([]envtarget.Target, error) {
	return s.Targets.List(ctx)
}

// Summary reads everything the three agents have configured on one machine.
func (s *Service) Summary(ctx context.Context, targetID, agentFilter string) (*Summary, error) {
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	sn, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	skills, err := sn.discoverSkills(ctx)
	if err != nil {
		return nil, err
	}
	var servers []MCPServer
	for _, agent := range agents {
		servers = append(servers, sn.mcpServers(agent, true)...)
	}
	plugins, markets := sn.plugins(ctx), sn.marketplaces()
	if agentFilter != "" && agentFilter != "all" {
		skills = filterSkills(skills, agentFilter)
		servers = filterBy(servers, func(m MCPServer) bool { return m.Agent == agentFilter })
		plugins = filterBy(plugins, func(p Plugin) bool { return p.Agent == agentFilter })
		markets = filterBy(markets, func(m Marketplace) bool { return m.Agent == agentFilter || m.Agent == "all" })
	}
	for _, builtin := range officialskills.ListOfficialSkills() {
		installed := []string{}
		kept := skills[:0]
		for _, skill := range skills {
			if skill.Name == builtin.Name && skill.Source == "astrorder-installed" {
				installed = append(installed, skill.Agents...)
				continue
			}
			kept = append(kept, skill)
		}
		skills = kept
		skills = append(skills, Skill{Name: builtin.Name, Scope: builtin.Scope, Source: builtin.Source, Category: builtin.Category, Agent: builtin.Agent, Agents: []string{}, InstalledAgents: installed, Content: builtin.Content, Description: builtin.Description, DisplayName: builtin.DisplayName})
	}
	target := env.Target()
	return &Summary{
		SkillAgents: registeredSkillAgents(),
		TargetID:    targetID, TargetName: target.Name, Kind: target.Kind,
		Counts: map[string]int{"skills": len(skills), "mcp": len(servers), "plugins": len(plugins), "marketplaces": len(markets)},
		Skills: nonNil(skills), MCPServers: nonNil(servers), Plugins: nonNil(plugins), Marketplaces: nonNil(markets), Warnings: sn.warns,
	}, nil
}

func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func filterBy[T any](list []T, keep func(T) bool) []T {
	out := make([]T, 0, len(list))
	for _, item := range list {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

func filterSkills(skills []Skill, agent string) []Skill {
	return filterBy(skills, func(skill Skill) bool { return contains(skill.Agents, agent) })
}

// TargetSkills is one row of the skills page.
type TargetSkills struct {
	envtarget.Target
	Skills      []Skill `json:"skills"`
	SkillsCount int     `json:"skills_count"`
	Error       string  `json:"error,omitempty"`
}

func (s *Service) skillsOf(ctx context.Context, target envtarget.Target) TargetSkills {
	row := TargetSkills{Target: target, Skills: []Skill{}}
	env, err := s.open(ctx, target.ID)
	if err == nil {
		var sn *snapshot
		if sn, err = s.snapshot(ctx, env); err == nil {
			var skills []Skill
			if skills, err = sn.discoverSkills(ctx); err == nil {
				row.Skills = nonNil(skills)
			}
		}
	}
	if err != nil {
		_, row.Error = statusOf(err)
	}
	row.SkillsCount = len(row.Skills)
	return row
}

// SkillTargets lists every machine with the skills installed on it.
func (s *Service) SkillTargets(ctx context.Context) ([]TargetSkills, error) {
	targets, err := s.Targets.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]TargetSkills, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = s.skillsOf(ctx, target)
		}()
	}
	wg.Wait()
	return rows, nil
}

func (s *Service) TargetSkillList(ctx context.Context, targetID string) ([]Skill, error) {
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	sn, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	skills, err := sn.discoverSkills(ctx)
	return nonNil(skills), err
}
