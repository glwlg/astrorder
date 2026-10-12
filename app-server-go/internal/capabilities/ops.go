package capabilities

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

// agentEnv points an agent's own CLI at the same home the service reads.
func agentEnv(agent string, paths envtarget.Paths) map[string]string {
	env := map[string]string{"NO_COLOR": "1", "CI": "1"}
	switch agent {
	case "codex":
		env["CODEX_HOME"] = paths.Codex
	case "hermes":
		env["HERMES_HOME"] = paths.Hermes
	}
	return env
}

func (s *Service) agentCLI(ctx context.Context, env envtarget.Env, agent string, args ...string) (string, error) {
	paths, err := env.Paths(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	result, err := env.Run(ctx, envtarget.Command{Name: agent, Args: args, Env: agentEnv(agent, paths)})
	if err != nil {
		return "", err
	}
	out := cleanOutput(result.Stdout, result.Stderr)
	if result.Code != 0 {
		return out, envtarget.Errorf(502, "%s %s 失败：%s", agent, strings.Join(args[:min(len(args), 2)], " "), tail(out, 600))
	}
	return out, nil
}

func (s *Service) editConfig(ctx context.Context, env envtarget.Env, agent string, mutate func([]byte) ([]byte, error), verify func([]byte) error) error {
	paths, err := env.Paths(ctx)
	if err != nil {
		return err
	}
	file := configPath(paths, agent)
	defer s.lock(env.Target().ID + "|" + file)()
	return s.commit(ctx, env, file, mutate, verify)
}

// ---- MCP --------------------------------------------------------------

type MCPResult struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Agent  string `json:"agent"`
	Server string `json:"server"`
}

func (s *Service) UpdateMCP(ctx context.Context, targetID, agent, action string, raw map[string]any) (*MCPResult, error) {
	if !validAgent(agent) {
		return nil, envtarget.Errorf(422, "不支持的 Agent：%s", agent)
	}
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	switch action {
	case "save":
		in, err := parseMCPInput(raw)
		if err != nil {
			return nil, envtarget.Errorf(422, "%v", err)
		}
		return s.saveMCP(ctx, env, agent, in)
	case "delete":
		name := strings.TrimSpace(asString(raw["name"]))
		if !mcpNameRE.MatchString(name) {
			return nil, envtarget.Errorf(422, "MCP 服务名称无效")
		}
		mutate := func(data []byte) ([]byte, error) {
			if agent == "hermes" {
				return removeMCPYAML(data, name)
			}
			return removeMCPTOML(data, agent, name)
		}
		if err := s.editConfig(ctx, env, agent, mutate, verifyMCPRemoved(agent, name)); err != nil {
			return nil, err
		}
		return &MCPResult{OK: true, Action: action, Agent: agent, Server: name}, nil
	}
	return nil, envtarget.Errorf(422, "不支持的操作：%s", action)
}

func (s *Service) saveMCP(ctx context.Context, env envtarget.Env, agent string, in mcpInput) (*MCPResult, error) {
	mutate := func(data []byte) ([]byte, error) {
		if agent == "hermes" {
			return applyMCPYAML(data, in)
		}
		return applyMCPTOML(data, agent, in)
	}
	if err := s.editConfig(ctx, env, agent, mutate, verifyMCP(agent, in)); err != nil {
		return nil, err
	}
	return &MCPResult{OK: true, Action: "save", Agent: agent, Server: in.Name}, nil
}

// storedMCP returns one server as stored (secrets included).
func (s *Service) storedMCP(ctx context.Context, env envtarget.Env, agent, name string) (*MCPServer, error) {
	sn, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	entry := asMap(sn.mcpEntries(agent)[name])
	if entry == nil {
		return nil, envtarget.Errorf(404, "%s 的配置里没有 MCP 服务 %s", agent, name)
	}
	server := mcpFromEntry(agent, name, entry, sn.disabledMCP(agent), false)
	return &server, nil
}

type PingResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Message    string `json:"message"`
}

const pingScript = `url="$1"; code=$(curl -sS -o /dev/null -m 5 -w '%{http_code}' -H @- "$url" 2>&1); printf '%s' "$code"`

// Ping probes an MCP server from the machine that runs the agent.
func (s *Service) Ping(ctx context.Context, targetID string, raw map[string]any) (*PingResult, error) {
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	server := MCPServer{Name: asString(raw["name"]), Agent: asString(raw["agent"]), URL: strings.TrimSpace(asString(raw["url"])),
		Command: strings.TrimSpace(asString(raw["command"])), Headers: asStringMap(raw["headers"])}
	if server.Name != "" && validAgent(server.Agent) {
		if stored, storedErr := s.storedMCP(ctx, env, server.Agent, server.Name); storedErr == nil {
			server.Headers = resolveHeaders(server.Headers, stored.Headers)
			if server.URL == "" {
				server.URL = stored.URL
			}
			if server.Command == "" {
				server.Command = stored.Command
			}
		}
	}
	started := time.Now()
	elapsed := func() int64 { return time.Since(started).Milliseconds() }
	if server.URL == "" {
		if server.Command == "" {
			return &PingResult{LatencyMS: -1, Message: "缺少 URL 或启动命令"}, nil
		}
		if strings.ContainsAny(server.Command, "\r\n\x00") {
			return &PingResult{LatencyMS: -1, Message: "启动命令无效"}, nil
		}
		executable := server.Command
		if fields := strings.Fields(executable); len(fields) > 0 && !strings.ContainsAny(executable, `/\`) {
			executable = fields[0]
		}
		if found, ok := env.Which(ctx, executable); ok || (strings.ContainsAny(executable, `/\`) && env.IsDir(ctx, path.Dir(executable))) {
			_ = found
			return &PingResult{OK: true, LatencyMS: elapsed(), Message: "可执行文件已找到：" + executable}, nil
		}
		return &PingResult{LatencyMS: elapsed(), Message: "未找到可执行文件：" + executable}, nil
	}
	if !strings.HasPrefix(server.URL, "http://") && !strings.HasPrefix(server.URL, "https://") {
		return &PingResult{LatencyMS: -1, Message: "URL 必须以 http:// 或 https:// 开头"}, nil
	}
	status, err := s.probeHTTP(ctx, env, server.URL, server.Headers)
	if err != nil {
		return &PingResult{LatencyMS: -1, Message: "连接失败：" + err.Error()}, nil
	}
	ok := status >= 200 && status < 300 || status == 400 || status == 404 || status == 405 || status == 406
	message := fmt.Sprintf("HTTP %d (%dms)", status, elapsed())
	if status == 401 || status == 403 {
		message += "，服务可达但需要认证"
	}
	return &PingResult{OK: ok, StatusCode: status, LatencyMS: elapsed(), Message: message}, nil
}

func resolveHeaders(sent, stored map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range sent {
		if value == redactedValue {
			if old, ok := stored[key]; ok {
				out[key] = old
			}
			continue
		}
		out[key] = value
	}
	return out
}

func (s *Service) probeHTTP(ctx context.Context, env envtarget.Env, url string, headers map[string]string) (int, error) {
	if env.IsLocal() {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := s.HTTP.Do(request)
		if err != nil {
			return 0, err
		}
		response.Body.Close()
		return response.StatusCode, nil
	}
	var lines bytes.Buffer
	for key, value := range headers {
		if strings.ContainsAny(key+value, "\r\n") {
			return 0, fmt.Errorf("请求头包含换行")
		}
		fmt.Fprintf(&lines, "%s: %s\n", key, value)
	}
	result, err := env.Shell(ctx, pingScript, lines.Bytes(), url)
	if err != nil {
		return 0, err
	}
	var status int
	if _, scanErr := fmt.Sscanf(strings.TrimSpace(result.Stdout), "%d", &status); scanErr != nil || status == 0 {
		return 0, fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr), "无响应"))
	}
	return status, nil
}

// ---- plugins ----------------------------------------------------------

type PluginResult struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Agent  string `json:"agent"`
	Plugin string `json:"plugin"`
}

func (sn *snapshot) findPlugin(ctx context.Context, agent, name string) (Plugin, bool) {
	for _, plugin := range sn.plugins(ctx) {
		if plugin.Agent == agent && (plugin.FullName == name || plugin.Name == name) {
			return plugin, true
		}
	}
	return Plugin{}, false
}

func (s *Service) UpdatePlugin(ctx context.Context, targetID, agent, action, name string, enabled bool) (*PluginResult, error) {
	if !validAgent(agent) {
		return nil, envtarget.Errorf(422, "不支持的 Agent：%s", agent)
	}
	if !pluginNameRE.MatchString(name) {
		return nil, envtarget.Errorf(422, "插件名称无效")
	}
	if action != "toggle" && action != "uninstall" {
		return nil, envtarget.Errorf(422, "不支持的操作：%s", action)
	}
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if err := s.applyPlugin(ctx, env, agent, action, name, enabled); err != nil {
		return nil, err
	}
	return &PluginResult{OK: true, Action: action, Agent: agent, Plugin: name}, nil
}

func (s *Service) applyPlugin(ctx context.Context, env envtarget.Env, agent, action, name string, enabled bool) error {
	sn, err := s.snapshot(ctx, env)
	if err != nil {
		return err
	}
	_, present := sn.findPlugin(ctx, agent, name)
	if !present {
		return envtarget.Errorf(404, "%s 里没有插件 %s", agent, name)
	}
	_, hasCLI := env.Which(ctx, agent)
	switch {
	case action == "uninstall" && hasCLI && agent == "codex":
		_, err = s.agentCLI(ctx, env, "codex", "plugin", "remove", name)
	case action == "uninstall" && hasCLI && agent == "hermes":
		_, err = s.agentCLI(ctx, env, "hermes", "plugins", "remove", name)
	case action == "uninstall" && hasCLI && agent == "grok":
		_, err = s.agentCLI(ctx, env, "grok", "plugin", "uninstall", name)
	case action == "toggle" && hasCLI && agent == "grok":
		verb := "disable"
		if enabled {
			verb = "enable"
		}
		_, err = s.agentCLI(ctx, env, "grok", "plugin", verb, name)
	default:
		err = s.editPluginConfig(ctx, env, agent, action, name, enabled)
	}
	if err != nil {
		return err
	}
	after, err := s.snapshot(ctx, env)
	if err != nil {
		return err
	}
	plugin, still := after.findPlugin(ctx, agent, name)
	switch {
	case action == "uninstall" && still && agent != "hermes":
		return envtarget.Errorf(502, "卸载后 %s 仍在 %s 的配置里", name, agent)
	case action == "toggle" && (!still || plugin.Enabled != enabled):
		return envtarget.Errorf(502, "切换后 %s 的状态与期望不一致", name)
	}
	return nil
}

func (s *Service) editPluginConfig(ctx context.Context, env envtarget.Env, agent, action, name string, enabled bool) error {
	remove := action == "uninstall"
	mutate := func(data []byte) ([]byte, error) {
		switch agent {
		case "codex":
			if remove {
				return removeCodexPlugin(data, name)
			}
			return setCodexPluginEnabled(data, name, enabled)
		case "hermes":
			return setHermesPlugin(data, name, enabled, remove)
		}
		return setGrokPlugin(data, name, enabled && !remove)
	}
	verify := func(stored []byte) error {
		cfg, err := parseConfig(agent, stored)
		if err != nil {
			return err
		}
		switch agent {
		case "codex":
			entry := asMap(asMap(cfg["plugins"])[name])
			if remove != (entry == nil) || (!remove && asBool(entry["enabled"], true) != enabled) {
				return fmt.Errorf("插件状态与期望不一致")
			}
		case "hermes":
			plugins := asMap(cfg["plugins"])
			if contains(asStrings(plugins["enabled"]), name) != (enabled && !remove) {
				return fmt.Errorf("插件状态与期望不一致")
			}
		default:
			if contains(asStrings(asMap(cfg["plugins"])["enabled"]), name) != (enabled && !remove) {
				return fmt.Errorf("插件状态与期望不一致")
			}
		}
		return nil
	}
	return s.editConfig(ctx, env, agent, mutate, verify)
}

func setGrokPlugin(data []byte, name string, on bool) ([]byte, error) {
	cfg, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	list := without(asStrings(asMap(cfg["plugins"])["enabled"]), name)
	if on {
		list = append(list, name)
	}
	doc := newTOMLDoc(data)
	doc.setKey([]string{"plugins"}, "enabled", encodeValue(nonNil(list)))
	return doc.bytes(), nil
}

// ---- marketplaces -----------------------------------------------------

type MarketResult struct {
	OK          bool   `json:"ok"`
	Action      string `json:"action"`
	Agent       string `json:"agent"`
	Marketplace string `json:"marketplace"`
}

func safeSource(text string) bool {
	if !notFlag(text) || len(text) > 400 || strings.ContainsAny(text, " \t") {
		return false
	}
	for _, r := range text {
		if r > 126 || r < 33 || strings.ContainsRune("`$&|;<>()\"'\\*?{}!^%", r) {
			return false
		}
	}
	return true
}

func (s *Service) UpdateMarketplace(ctx context.Context, targetID, agent, action string, raw map[string]any) (*MarketResult, error) {
	if agent != "codex" && agent != "grok" {
		return nil, envtarget.Errorf(422, "%s 没有市场源", agent)
	}
	name := strings.TrimSpace(asString(raw["name"]))
	source := strings.TrimSpace(asString(raw["source"]))
	ref := strings.TrimSpace(asString(raw["ref"]))
	if name != "" && !marketNameRE.MatchString(name) {
		return nil, envtarget.Errorf(422, "市场源名称无效")
	}
	env, err := s.open(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if _, ok := env.Which(ctx, agent); !ok {
		return nil, envtarget.Errorf(404, "%s 未安装 %s", env.Target().Name, agent)
	}
	before, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	var args []string
	switch {
	case action == "add":
		if !safeSource(source) {
			return nil, envtarget.Errorf(422, "市场源地址无效（支持 owner/repo、Git 地址或本地路径）")
		}
		if ref != "" && (!safeSource(ref) || agent != "codex") {
			ref = ""
		}
		if agent == "codex" {
			args = []string{"plugin", "marketplace", "add"}
			if ref != "" {
				args = append(args, "--ref", ref)
			}
			args = append(args, source)
		} else {
			args = []string{"plugin", "marketplace", "add", source}
		}
	case action == "delete" || action == "pull":
		if name == "" {
			return nil, envtarget.Errorf(422, "市场源名称不能为空")
		}
		if !marketExists(before, agent, name) {
			return nil, envtarget.Errorf(404, "%s 里没有市场源 %s", agent, name)
		}
		verb := map[string]string{"delete": "remove", "pull": "upgrade"}[action]
		if agent == "grok" && action == "pull" {
			verb = "update"
		}
		if agent == "codex" {
			args = []string{"plugin", "marketplace", verb, name}
		} else {
			target := name
			if action == "pull" {
				target = grokMarketSource(before, name)
			}
			args = []string{"plugin", "marketplace", verb, target}
		}
	default:
		return nil, envtarget.Errorf(422, "不支持的操作：%s", action)
	}
	if _, err := s.agentCLI(ctx, env, agent, args...); err != nil {
		return nil, err
	}
	after, err := s.snapshot(ctx, env)
	if err != nil {
		return nil, err
	}
	switch action {
	case "add":
		if len(after.marketplaces()) <= len(before.marketplaces()) && !marketExists(after, agent, name) {
			return nil, envtarget.Errorf(502, "添加后在 %s 的配置里没有看到新的市场源", agent)
		}
	case "delete":
		if marketExists(after, agent, name) {
			return nil, envtarget.Errorf(502, "删除后 %s 仍在 %s 的配置里", name, agent)
		}
	}
	return &MarketResult{OK: true, Action: action, Agent: agent, Marketplace: firstNonEmpty(name, source)}, nil
}

func marketExists(sn *snapshot, agent, name string) bool {
	for _, market := range sn.marketplaces() {
		if market.Agent == agent && (market.Name == name || market.Source == name) {
			return true
		}
	}
	return false
}

func grokMarketSource(sn *snapshot, name string) string {
	for _, market := range sn.marketplaces() {
		if market.Agent == "grok" && market.Name == name && market.Source != "" {
			return market.Source
		}
	}
	return name
}
