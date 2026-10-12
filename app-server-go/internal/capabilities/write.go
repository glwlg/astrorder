package capabilities

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// mcpInput is what the browser sends for one MCP server. Env and Headers are
// pointers: nil leaves what is stored untouched, a map replaces it (a value of
// [REDACTED] keeps the stored value for that key).
type mcpInput struct {
	Name      string
	Transport string
	URL       string
	Command   string
	Args      []string
	Enabled   bool
	Env       *map[string]string
	Headers   *map[string]string
}

func parseMCPInput(raw map[string]any) (mcpInput, error) {
	in := mcpInput{Name: strings.TrimSpace(asString(raw["name"])), Enabled: asBool(raw["enabled"], true)}
	if !mcpNameRE.MatchString(in.Name) {
		return in, fmt.Errorf("MCP 服务名称只能包含字母、数字、下划线、点和连字符（最长 64 位）")
	}
	in.URL, in.Command = strings.TrimSpace(asString(raw["url"])), strings.TrimSpace(asString(raw["command"]))
	switch args := raw["args"].(type) {
	case string:
		in.Args = strings.Fields(args)
	case nil:
	default:
		in.Args = asStrings(args)
	}
	in.Transport = asString(raw["transport"])
	if in.Transport == "" {
		in.Transport = transportOf(in.URL)
	}
	switch in.Transport {
	case "sse/http", "http", "sse":
		parsed, err := url.Parse(in.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return in, fmt.Errorf("URL 必须是完整的 http 或 https 地址")
		}
		in.Transport, in.Command, in.Args = "sse/http", "", nil
	case "stdio":
		if in.Command == "" || strings.ContainsAny(in.Command, "\r\n\x00") {
			return in, fmt.Errorf("stdio 服务必须填写启动命令")
		}
		in.URL = ""
	default:
		return in, fmt.Errorf("不支持的传输类型：%s", in.Transport)
	}
	for _, arg := range in.Args {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return in, fmt.Errorf("参数不能包含换行")
		}
	}
	var err error
	if in.Env, err = optionalStringMap(raw, "env"); err != nil {
		return in, err
	}
	if in.Headers, err = optionalStringMap(raw, "headers"); err != nil {
		return in, err
	}
	return in, nil
}

func optionalStringMap(raw map[string]any, key string) (*map[string]string, error) {
	value, present := raw[key]
	if !present || value == nil {
		return nil, nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 必须是键值对象", key)
	}
	out := map[string]string{}
	for name, item := range m {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n\x00=") {
			return nil, fmt.Errorf("%s 的键名无效：%q", key, name)
		}
		text := asString(item)
		if strings.ContainsAny(text, "\r\n\x00") {
			return nil, fmt.Errorf("%s 的值不能包含换行", key)
		}
		out[name] = text
	}
	return &out, nil
}

// resolveSecrets turns the browser's map into the stored one: a redacted value
// stands for whatever is stored under that key.
func resolveSecrets(sent *map[string]string, stored map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range *sent {
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

// ---- TOML agents (Codex, Grok) ----------------------------------------

func applyMCPTOML(data []byte, agent string, in mcpInput) ([]byte, error) {
	cfg, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	stored := asMap(asMap(cfg["mcp_servers"])[in.Name])
	doc := newTOMLDoc(data)
	path := []string{"mcp_servers", in.Name}
	if _, ok := doc.find(path); !ok && stored != nil {
		return nil, fmt.Errorf("%s 在配置里是内联写法，请直接编辑配置文件", in.Name)
	}
	doc.ensureTable(path)
	setOrDrop := func(key string, present bool, value any) {
		if present {
			doc.setKey(path, key, encodeValue(value))
		} else {
			doc.removeKey(path, key)
		}
	}
	setOrDrop("url", in.URL != "", in.URL)
	setOrDrop("command", in.Command != "", in.Command)
	setOrDrop("args", len(in.Args) > 0, in.Args)
	if !in.Enabled || doc.hasKey(path, "enabled") || agent == "grok" {
		doc.setKey(path, "enabled", encodeValue(in.Enabled))
	}
	secrets := func(key string, sent *map[string]string) {
		if sent == nil {
			return
		}
		merged := resolveSecrets(sent, asStringMap(stored[key]))
		doc.removeKey(path, key)
		doc.replaceMapTable(append(append([]string{}, path...), key), merged, sortedKeys(merged))
	}
	secrets("env", in.Env)
	if in.Headers != nil && agent == "codex" {
		// An old entry may keep headers under the key the other agents use.
		doc.deleteTable(append(append([]string{}, path...), "headers"))
		doc.removeKey(path, "headers")
	}
	secrets(headersKey(agent), in.Headers)
	if agent == "grok" {
		disabled := asStrings(cfg["disabled_mcp_servers"])
		disabled = without(disabled, in.Name)
		if !in.Enabled {
			disabled = append(disabled, in.Name)
		}
		if len(disabled) > 0 || doc.hasKey(nil, "disabled_mcp_servers") {
			doc.setKey(nil, "disabled_mcp_servers", encodeValue(disabled))
		}
	}
	return doc.bytes(), nil
}

func removeMCPTOML(data []byte, agent, name string) ([]byte, error) {
	cfg, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	doc := newTOMLDoc(data)
	doc.deleteTable([]string{"mcp_servers", name})
	if agent == "grok" {
		if disabled := asStrings(cfg["disabled_mcp_servers"]); contains(disabled, name) {
			doc.setKey(nil, "disabled_mcp_servers", encodeValue(without(disabled, name)))
		}
	}
	return doc.bytes(), nil
}

func setCodexPluginEnabled(data []byte, name string, enabled bool) ([]byte, error) {
	if _, err := parseTOML(data); err != nil {
		return nil, err
	}
	doc := newTOMLDoc(data)
	doc.setKey([]string{"plugins", name}, "enabled", encodeValue(enabled))
	return doc.bytes(), nil
}

func removeCodexPlugin(data []byte, name string) ([]byte, error) {
	if _, err := parseTOML(data); err != nil {
		return nil, err
	}
	doc := newTOMLDoc(data)
	doc.deleteTable([]string{"plugins", name})
	return doc.bytes(), nil
}

// ---- YAML agent (Hermes) ----------------------------------------------

func applyMCPYAML(data []byte, in mcpInput) ([]byte, error) {
	doc, root, err := yamlParse(data)
	if err != nil {
		return nil, err
	}
	servers := ensureMap(root, "mcp_servers")
	entry := mapGet(servers, in.Name)
	if entry == nil || entry.Kind != yaml.MappingNode {
		if err := mapSet(servers, in.Name, map[string]any{}); err != nil {
			return nil, err
		}
		entry = mapGet(servers, in.Name)
	}
	set := func(key string, present bool, value any) error {
		if !present {
			mapDel(entry, key)
			return nil
		}
		return mapSet(entry, key, value)
	}
	if err := set("url", in.URL != "", in.URL); err != nil {
		return nil, err
	}
	if err := set("command", in.Command != "", in.Command); err != nil {
		return nil, err
	}
	if err := set("args", len(in.Args) > 0, in.Args); err != nil {
		return nil, err
	}
	if err := set("enabled", !in.Enabled, false); err != nil {
		return nil, err
	}
	for key, sent := range map[string]*map[string]string{"env": in.Env, "headers": in.Headers} {
		if sent == nil {
			continue
		}
		merged := resolveSecrets(sent, nodeStringMap(mapGet(entry, key)))
		if err := set(key, len(merged) > 0, merged); err != nil {
			return nil, err
		}
	}
	return yamlDump(doc)
}

func removeMCPYAML(data []byte, name string) ([]byte, error) {
	doc, root, err := yamlParse(data)
	if err != nil {
		return nil, err
	}
	mapDel(mapGet(root, "mcp_servers"), name)
	return yamlDump(doc)
}

// setHermesPlugin moves name between plugins.enabled and plugins.disabled, or
// drops it from both (and from plugins.entries) when remove is set.
func setHermesPlugin(data []byte, name string, enabled, remove bool) ([]byte, error) {
	doc, root, err := yamlParse(data)
	if err != nil {
		return nil, err
	}
	plugins := ensureMap(root, "plugins")
	on, off := without(nodeStrings(mapGet(plugins, "enabled")), name), without(nodeStrings(mapGet(plugins, "disabled")), name)
	switch {
	case remove:
		mapDel(mapGet(plugins, "entries"), name)
	case enabled:
		on = append(on, name)
	default:
		off = append(off, name)
	}
	if err := mapSet(plugins, "enabled", nonNil(on)); err != nil {
		return nil, err
	}
	if err := mapSet(plugins, "disabled", nonNil(off)); err != nil {
		return nil, err
	}
	return yamlDump(doc)
}

// ---- verification -----------------------------------------------------

func verifyMCP(agent string, in mcpInput) func([]byte) error {
	return func(stored []byte) error {
		cfg, err := parseConfig(agent, stored)
		if err != nil {
			return err
		}
		entry := asMap(asMap(cfg["mcp_servers"])[in.Name])
		if entry == nil {
			return fmt.Errorf("配置中找不到 %s", in.Name)
		}
		if asString(entry["url"]) != in.URL || asString(entry["command"]) != in.Command || !sameStrings(asStrings(entry["args"]), in.Args) {
			return fmt.Errorf("%s 的连接参数与提交的不一致", in.Name)
		}
		if asBool(entry["enabled"], true) != in.Enabled {
			return fmt.Errorf("%s 的启用状态与提交的不一致", in.Name)
		}

		return nil
	}
}

func verifyMCPRemoved(agent, name string) func([]byte) error {
	return func(stored []byte) error {
		cfg, err := parseConfig(agent, stored)
		if err != nil {
			return err
		}
		if asMap(cfg["mcp_servers"])[name] != nil {
			return fmt.Errorf("%s 仍在配置中", name)
		}
		return nil
	}
}
