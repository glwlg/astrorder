package modelgateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nativeGrokModels = map[string]bool{
	"grok-4.5": true, "grok-build": true, "grok-4.20": true, "grok-code": true,
}

var grokEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

var gatewayEnvKeys = map[string]string{
	"opencodex": "OPENCODEX_API_AUTH_TOKEN",
	"magpie":    "MAGPIE_API_KEY",
}

func sha256Text(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func tomlQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// codexCatalogKeyOrder mirrors the Python renderer's dict insertion order for
// a catalog model entry, so rendered files stay byte-comparable across
// implementations (the daemon hashes and diffs this content).
var codexCatalogKeyOrder = []string{
	"visibility", "priority", "base_instructions", "supported_in_api",
	"supports_parallel_tool_calls", "supports_reasoning_summaries", "shell_type",
	"support_verbosity", "default_verbosity", "apply_patch_tool_type",
	"web_search_tool_type", "truncation_policy", "supports_image_detail_original",
	"comp_hash", "effective_context_window_percent", "experimental_supported_tools",
	"input_modalities", "supports_search_tool", "supports_experimental_context",
	"node_repl_auto_review_required", "node_repl_disabled", "multi_agent_version",
	"slug", "display_name", "description", "context_window", "max_context_window",
	"supported_reasoning_levels", "default_reasoning_level", "auto_compact_token_limit",
}

var codexCatalogKeySet = func() map[string]bool {
	set := make(map[string]bool, len(codexCatalogKeyOrder))
	for _, key := range codexCatalogKeyOrder {
		set[key] = true
	}
	return set
}()

// orderedMap is a JSON object that preserves key insertion order when
// marshaled (Go's default map marshaling sorts keys, which would break the
// Python-parity byte format).
type orderedMap struct {
	keys []string
	vals map[string]any
}

func newOrderedMap() *orderedMap {
	return &orderedMap{vals: make(map[string]any)}
}

func (o *orderedMap) Set(key string, value any) {
	if _, exists := o.vals[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
}

func (o *orderedMap) Get(key string) (any, bool) {
	value, ok := o.vals[key]
	return value, ok
}

func (o *orderedMap) Delete(key string) {
	if _, exists := o.vals[key]; !exists {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := marshalCompactNoEscape(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valJSON, err := marshalCompactNoEscape(o.vals[key])
		if err != nil {
			return nil, err
		}
		buf.Write(valJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func marshalCompactNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	return b, nil
}

func newCodexCatalogPrototype() *orderedMap {
	row := newOrderedMap()
	row.Set("visibility", "list")
	row.Set("priority", 5)
	row.Set("base_instructions", "You are a coding agent powered by the model. You and the user share one workspace, and your job is to collaborate with them until their intended goal is completely handled.")
	row.Set("supported_in_api", true)
	row.Set("supports_parallel_tool_calls", true)
	row.Set("supports_reasoning_summaries", true)
	row.Set("shell_type", "unified_exec")
	row.Set("support_verbosity", true)
	row.Set("default_verbosity", "low")
	row.Set("apply_patch_tool_type", "freeform")
	row.Set("web_search_tool_type", "text_and_image")
	truncation := newOrderedMap()
	truncation.Set("mode", "tokens")
	truncation.Set("limit", 10000)
	row.Set("truncation_policy", truncation)
	row.Set("supports_image_detail_original", true)
	row.Set("comp_hash", "3000")
	row.Set("effective_context_window_percent", 95)
	row.Set("experimental_supported_tools", []any{"send_user_message_async", "clock"})
	row.Set("input_modalities", []any{"text", "image"})
	row.Set("supports_search_tool", true)
	row.Set("supports_experimental_context", true)
	row.Set("node_repl_auto_review_required", true)
	row.Set("node_repl_disabled", false)
	row.Set("multi_agent_version", "v1")
	return row
}

func normalizeCodexValue(key string, value any) any {
	switch key {
	case "truncation_policy":
		if m, ok := value.(map[string]any); ok {
			out := newOrderedMap()
			if mode, ok := m["mode"]; ok {
				out.Set("mode", mode)
			}
			if limit, ok := m["limit"]; ok {
				out.Set("limit", limit)
			}
			return out
		}
	case "supported_reasoning_levels":
		if levels, ok := value.([]any); ok {
			normalized := make([]any, 0, len(levels))
			for _, lvl := range levels {
				lm, ok := lvl.(map[string]any)
				if !ok {
					normalized = append(normalized, lvl)
					continue
				}
				out := newOrderedMap()
				if effort, ok := lm["effort"]; ok {
					out.Set("effort", effort)
				}
				if desc, ok := lm["description"]; ok {
					out.Set("description", desc)
				}
				normalized = append(normalized, out)
			}
			return normalized
		}
	}
	return value
}

func mergeCodexPrevious(row *orderedMap, previous map[string]any) {
	if previous == nil {
		return
	}
	for _, key := range codexCatalogKeyOrder {
		if value, ok := previous[key]; ok {
			row.Set(key, normalizeCodexValue(key, value))
		}
	}
	var extras []string
	for key := range previous {
		if !codexCatalogKeySet[key] {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		row.Set(key, normalizeCodexValue(key, previous[key]))
	}
}

func catalogInputModalities(mods []string) []any {
	if len(mods) == 0 {
		return []any{"text"}
	}
	out := make([]any, len(mods))
	for i, mod := range mods {
		out[i] = mod
	}
	return out
}

// RenderCodexCatalog serializes the catalog into the Codex model catalog JSON
// format. Field names, values and key order mirror the Python reference
// renderer so existing catalogs (and native consumers) stay compatible.
func RenderCodexCatalog(catalog *Catalog, existing []any, gatewayName string) string {
	if gatewayName == "" {
		gatewayName = "OpenCodeX"
	}

	old := make(map[string]map[string]any)
	for _, item := range existing {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if slug, ok := m["slug"].(string); ok && slug != "" {
			old[slug] = m
		}
	}

	out := make([]any, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		previous := old[m.Slug]

		previousLevels := make([]any, 0, 4)
		if previous != nil {
			if raw, ok := previous["supported_reasoning_levels"].([]any); ok {
				for _, lvl := range raw {
					lm, ok := lvl.(map[string]any)
					if !ok {
						continue
					}
					if eff, ok := lm["effort"].(string); ok && eff != "" {
						previousLevels = append(previousLevels, lm)
					}
				}
			}
		}
		oldDescriptions := make(map[string]string, len(previousLevels))
		for _, lvl := range previousLevels {
			lm := lvl.(map[string]any)
			eff := lm["effort"].(string)
			desc := eff
			if d, ok := lm["description"].(string); ok && d != "" {
				desc = d
			}
			oldDescriptions[eff] = desc
		}

		efforts := make([]any, 0, len(m.ReasoningEfforts))
		if len(m.ReasoningEfforts) > 0 {
			for _, eff := range m.ReasoningEfforts {
				desc, ok := oldDescriptions[eff]
				if !ok {
					desc = eff
				}
				level := newOrderedMap()
				level.Set("effort", eff)
				level.Set("description", desc)
				efforts = append(efforts, level)
			}
		} else {
			for _, lvl := range previousLevels {
				lm := lvl.(map[string]any)
				desc, _ := lm["description"].(string)
				if desc == "" {
					desc, _ = lm["effort"].(string)
				}
				level := newOrderedMap()
				level.Set("effort", lm["effort"])
				level.Set("description", desc)
				efforts = append(efforts, level)
			}
		}
		if len(efforts) == 0 {
			fallback := m.DefaultReasoningEffort
			if fallback == "" && previous != nil {
				fallback, _ = previous["default_reasoning_level"].(string)
			}
			if fallback == "" {
				fallback = "medium"
			}
			level := newOrderedMap()
			level.Set("effort", fallback)
			level.Set("description", fallback)
			efforts = append(efforts, level)
		}

		effortNames := make(map[string]bool, len(efforts))
		for _, lvl := range efforts {
			if eff, ok := lvl.(*orderedMap).Get("effort"); ok {
				if s, ok := eff.(string); ok {
					effortNames[s] = true
				}
			}
		}
		defaultEffort := m.DefaultReasoningEffort
		if defaultEffort == "" && previous != nil {
			defaultEffort, _ = previous["default_reasoning_level"].(string)
		}
		if !effortNames[defaultEffort] {
			first, _ := efforts[0].(*orderedMap).Get("effort")
			defaultEffort, _ = first.(string)
		}

		contextWindow := m.ContextWindow
		maxContextWindow := m.ContextWindow
		if previous != nil {
			if contextWindow == 0 {
				if cw := getInt(previous["context_window"], 0); cw != 0 {
					contextWindow = cw
				} else if cw := getInt(previous["max_context_window"], 0); cw != 0 {
					contextWindow = cw
				}
			}
			if maxContextWindow == 0 {
				maxContextWindow = getInt(previous["max_context_window"], 0)
			}
		}

		compactLimit := 0
		hasCompact := false
		if m.AutoCompactTokenLimit != 0 {
			compactLimit = m.AutoCompactTokenLimit
			hasCompact = true
		} else if contextWindow > 0 {
			compactLimit = contextWindow * 4 / 5
			hasCompact = true
		}

		row := newCodexCatalogPrototype()
		row.Set("slug", m.Slug)
		row.Set("display_name", m.Slug)
		row.Set("description", fmt.Sprintf("由 %s 提供", gatewayName))
		row.Set("visibility", "list")
		mergeCodexPrevious(row, previous)

		row.Set("context_window", contextWindow)
		row.Set("max_context_window", maxContextWindow)
		row.Set("input_modalities", catalogInputModalities(m.InputModalities))
		row.Set("supported_reasoning_levels", efforts)
		row.Set("default_reasoning_level", defaultEffort)
		if hasCompact {
			row.Set("auto_compact_token_limit", compactLimit)
		} else {
			row.Delete("auto_compact_token_limit")
		}

		out = append(out, row)
	}

	root := newOrderedMap()
	root.Set("models", out)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		fallback, _ := json.Marshal(root)
		return string(fallback)
	}
	return buf.String()
}

// ConfiguredProvider describes a provider configuration for Codex.
type ConfiguredProvider struct {
	Key     string
	Name    string
	BaseURL string
	EnvKey  string
}

// RenderCodexConfig merges existing codex config.toml with gateway providers.
func RenderCodexConfig(existing, inferenceURL, catalogPath, providerKey, providerName, envKey string, allProviders []ConfiguredProvider) string {
	existing = strings.TrimPrefix(existing, "\ufeff")

	providersMap := make(map[string]ConfiguredProvider)
	for _, p := range allProviders {
		if p.Key != "" {
			providersMap[p.Key] = p
		}
	}
	providersMap[providerKey] = ConfiguredProvider{
		Key:     providerKey,
		Name:    providerName,
		BaseURL: inferenceURL,
		EnvKey:  envKey,
	}

	// Remove old sections for known providers
	removeSections := map[string]bool{
		"model_providers.opencodex": true,
		"model_providers.magpie":    true,
	}
	for k := range providersMap {
		removeSections["model_providers."+k] = true
	}

	text := replaceSections(existing, removeSections, "")
	text = replaceTopKeys(text, map[string]string{
		"model_provider":     providerKey,
		"model_catalog_json": catalogPath,
	})

	var sections []string
	orderedKeys := []string{providerKey}
	for k := range providersMap {
		if k != providerKey {
			orderedKeys = append(orderedKeys, k)
		}
	}
	sort.Slice(orderedKeys[1:], func(i, j int) bool {
		return orderedKeys[1+i] < orderedKeys[1+j]
	})

	for _, k := range orderedKeys {
		p := providersMap[k]
		pName := p.Name
		if pName == "" {
			if k == "magpie" {
				pName = "Magpie Proxy"
			} else {
				pName = "OpenCodeX Proxy"
			}
		}
		pURL := p.BaseURL
		if pURL == "" {
			pURL = inferenceURL
		}
		pEnv := p.EnvKey
		if pEnv == "" {
			pEnv = envKey
		}

		sec := fmt.Sprintf(`[model_providers.%s]
name = %s
base_url = %s
wire_api = "responses"
requires_openai_auth = true
env_key = %s`, k, tomlQuote(pName), tomlQuote(pURL), tomlQuote(pEnv))
		sections = append(sections, sec)
	}

	res := strings.TrimRight(text, "\r\n") + "\n\n" + strings.Join(sections, "\n\n") + "\n"
	return res
}

// RenderGrokConfig updates grok's config.toml with models from the catalog.
func RenderGrokConfig(existing string, catalog *Catalog, inferenceURL, apiKey string) string {
	existing = strings.TrimPrefix(existing, "\ufeff")
	var blocks []string

	for _, m := range catalog.Models {
		if nativeGrokModels[m.Slug] {
			continue
		}
		key := strings.ReplaceAll(m.Slug, "/", "-")
		var efforts []string
		for _, eff := range m.ReasoningEfforts {
			if grokEfforts[eff] {
				efforts = append(efforts, eff)
			}
		}
		if len(efforts) == 0 && m.Provider == "xai" {
			efforts = []string{"high", "medium", "low"}
		}
		defEffort := ""
		if grokEfforts[m.DefaultReasoningEffort] {
			defEffort = m.DefaultReasoningEffort
		} else if len(efforts) > 0 {
			defEffort = efforts[0]
		}

		var lines []string
		lines = append(lines,
			fmt.Sprintf("[model.%s]", tomlQuote(key)),
			fmt.Sprintf("model = %s", tomlQuote(m.Slug)),
			fmt.Sprintf("base_url = %s", tomlQuote(inferenceURL)),
			fmt.Sprintf("api_key = %s", tomlQuote(apiKey)),
			fmt.Sprintf("name = %s", tomlQuote(m.Slug)),
			"description = \"由 OpenCodeX 提供\"",
			"api_backend = \"chat_completions\"",
			"extra_headers = { \"x-opencodex-grok\" = \"1\" }",
		)
		if len(efforts) > 0 {
			lines = append(lines, "supports_reasoning_effort = true")
		} else {
			lines = append(lines, "supports_reasoning_effort = false")
		}
		if defEffort != "" {
			lines = append(lines, fmt.Sprintf("reasoning_effort = %s", tomlQuote(defEffort)))
		}
		for _, eff := range efforts {
			isDef := "false"
			if eff == defEffort {
				isDef = "true"
			}
			lines = append(lines,
				"",
				fmt.Sprintf("[[model.%s.reasoning_efforts]]", tomlQuote(key)),
				fmt.Sprintf("value = %s", tomlQuote(eff)),
				fmt.Sprintf("label = %s", tomlQuote(eff)),
				fmt.Sprintf("description = %s", tomlQuote(eff)),
				fmt.Sprintf("default = %s", isDef),
			)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	base := replaceSections(existing, map[string]bool{"model": true}, strings.Join(blocks, "\n\n"))
	reModels := regexp.MustCompile(`(?m)^\[models\]\s*$`)
	if !reModels.MatchString(base) {
		firstNonNative := ""
		for _, m := range catalog.Models {
			if !nativeGrokModels[m.Slug] {
				firstNonNative = strings.ReplaceAll(m.Slug, "/", "-")
				break
			}
		}
		defModel := "grok-4.5"
		if firstNonNative != "" {
			defModel = firstNonNative
		}
		base = fmt.Sprintf("[models]\ndefault = %s\ndefault_reasoning_effort = \"high\"\n\n", tomlQuote(defModel)) + base
	}
	return base
}

var hermesAliases = map[string][]string{
	"magpie":    {"magpie"},
	"opencodex": {"ocx", "opencodex"},
}

// RenderHermesConfig updates Hermes config.yaml.
func RenderHermesConfig(existing string, bindings map[string]map[string]string) string {
	existing = strings.TrimPrefix(existing, "\ufeff")
	if strings.TrimSpace(existing) == "" || len(bindings) == 0 {
		return ""
	}

	lines := strings.Split(existing, "\n")

	// 1. Update model.base_url if model.provider matches any binding
	lines = updateHermesModelSection(lines, bindings)

	// 2. Update providers section for each binding
	lines = updateHermesProvidersSection(lines, bindings)

	return strings.TrimRight(strings.Join(lines, "\n"), "\r\n") + "\n"
}

func updateHermesModelSection(lines []string, bindings map[string]map[string]string) []string {
	modelIdx := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "model:" {
			modelIdx = i
			break
		}
	}
	if modelIdx == -1 {
		return lines
	}

	// Find the bounds of model section
	modelEnd := len(lines)
	for i := modelIdx + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "	") {
			modelEnd = i
			break
		}
	}

	// Find current provider inside model:
	currentModelProvider := ""
	for i := modelIdx + 1; i < modelEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "provider:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "provider:"))
			currentModelProvider = strings.Trim(val, `"' `)
			break
		}
	}

	if currentModelProvider == "" {
		return lines
	}

	// Check if currentModelProvider matches any binding or its aliases
	for bindKey, fields := range bindings {
		newBaseURL, hasURL := fields["base_url"]
		if !hasURL || newBaseURL == "" {
			continue
		}

		matched := false
		if currentModelProvider == bindKey {
			matched = true
		} else {
			for _, alias := range hermesAliases[bindKey] {
				if currentModelProvider == alias {
					matched = true
					break
				}
			}
		}

		if matched {
			// Update or insert base_url under model:
			baseURLIdx := -1
			for i := modelIdx + 1; i < modelEnd; i++ {
				trimmed := strings.TrimSpace(lines[i])
				if strings.HasPrefix(trimmed, "base_url:") {
					baseURLIdx = i
					break
				}
			}
			if baseURLIdx != -1 {
				lines[baseURLIdx] = fmt.Sprintf("  base_url: %q", newBaseURL)
			} else {
				// Insert after provider: or at start of model children
				insertPos := modelIdx + 1
				for i := modelIdx + 1; i < modelEnd; i++ {
					if strings.HasPrefix(strings.TrimSpace(lines[i]), "provider:") {
						insertPos = i + 1
						break
					}
				}
				newLines := make([]string, 0, len(lines)+1)
				newLines = append(newLines, lines[:insertPos]...)
				newLines = append(newLines, fmt.Sprintf("  base_url: %q", newBaseURL))
				newLines = append(newLines, lines[insertPos:]...)
				lines = newLines
			}
			break
		}
	}

	return lines
}

func updateHermesProvidersSection(lines []string, bindings map[string]map[string]string) []string {
	providersIdx := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "providers:" {
			providersIdx = i
			break
		}
	}

	if providersIdx == -1 {
		// Create providers: section before trailing non-mapping blocks or at end
		var newLines []string
		newLines = append(newLines, lines...)
		newLines = append(newLines, "providers:")
		providersIdx = len(newLines) - 1
		lines = newLines
	}

	for bindKey, fields := range bindings {
		aliases := hermesAliases[bindKey]
		if len(aliases) == 0 {
			aliases = []string{bindKey}
		}

		// Re-find providers bounds in potentially modified lines
		for i, l := range lines {
			if strings.TrimSpace(l) == "providers:" {
				providersIdx = i
				break
			}
		}

		providersEnd := len(lines)
		for i := providersIdx + 1; i < len(lines); i++ {
			l := lines[i]
			if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "	") {
				providersEnd = i
				break
			}
		}

		// Search for existing alias in providers children (indent of 2 spaces)
		targetProviderIdx := -1
		targetProviderName := bindKey

		for i := providersIdx + 1; i < providersEnd; i++ {
			l := lines[i]
			if len(l) >= 3 && (l[0] == ' ' && l[1] == ' ' && l[2] != ' ') {
				trimmed := strings.TrimSpace(l)
				if strings.HasSuffix(trimmed, ":") {
					name := strings.TrimSuffix(trimmed, ":")
					for _, alias := range aliases {
						if name == alias {
							targetProviderIdx = i
							targetProviderName = name
							break
						}
					}
				}
			}
			if targetProviderIdx != -1 {
				break
			}
		}

		if targetProviderIdx != -1 {
			// Provider block exists, find its children range
			blockEnd := providersEnd
			for i := targetProviderIdx + 1; i < providersEnd; i++ {
				l := lines[i]
				if strings.TrimSpace(l) == "" {
					continue
				}
				// If line has indent <= 2 spaces, it is a sibling provider or out of block
				if !strings.HasPrefix(l, "   ") {
					blockEnd = i
					break
				}
			}

			// Update fields inside this provider block
			for fKey, fVal := range fields {
				fieldFound := false
				for i := targetProviderIdx + 1; i < blockEnd; i++ {
					trimmed := strings.TrimSpace(lines[i])
					if strings.HasPrefix(trimmed, fKey+":") {
						lines[i] = fmt.Sprintf("    %s: %q", fKey, fVal)
						fieldFound = true
						break
					}
				}
				if !fieldFound {
					// Insert field at end of this provider block
					newLines := make([]string, 0, len(lines)+1)
					newLines = append(newLines, lines[:blockEnd]...)
					newLines = append(newLines, fmt.Sprintf("    %s: %q", fKey, fVal))
					newLines = append(newLines, lines[blockEnd:]...)
					lines = newLines
					blockEnd++
					providersEnd++
				}
			}
		} else {
			// Provider block does not exist. Insert it at providersEnd (inside providers: section)
			var insertLines []string
			insertLines = append(insertLines, fmt.Sprintf("  %s:", targetProviderName))
			for fKey, fVal := range fields {
				insertLines = append(insertLines, fmt.Sprintf("    %s: %q", fKey, fVal))
			}

			newLines := make([]string, 0, len(lines)+len(insertLines))
			newLines = append(newLines, lines[:providersEnd]...)
			newLines = append(newLines, insertLines...)
			newLines = append(newLines, lines[providersEnd:]...)
			lines = newLines
		}
	}

	return lines
}

func replaceSections(content string, sections map[string]bool, replacement string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inSkipped := false
	reSec := regexp.MustCompile(`^\s*\[([^\]]+)\]`)

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if m := reSec.FindStringSubmatch(trimmed); len(m) > 1 {
			secName := m[1]
			skip := false
			for s := range sections {
				if secName == s || strings.HasPrefix(secName, s+".") {
					skip = true
					break
				}
			}
			inSkipped = skip
			if inSkipped {
				continue
			}
		}
		if !inSkipped {
			result = append(result, l)
		}
	}
	base := strings.TrimRight(strings.Join(result, "\n"), "\r\n")
	if replacement != "" {
		base += "\n\n" + replacement
	}
	return base
}

func replaceTopKeys(content string, keys map[string]string) string {
	lines := strings.Split(content, "\n")
	remaining := make(map[string]string)
	for k, v := range keys {
		remaining[k] = v
	}

	var output []string
	reSec := regexp.MustCompile(`^\s*\[`)
	seenSec := false

	for _, l := range lines {
		if reSec.MatchString(l) {
			seenSec = true
		}
		if !seenSec {
			parts := strings.SplitN(l, "=", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				if v, ok := remaining[k]; ok {
					output = append(output, fmt.Sprintf("%s = %s", k, tomlQuote(v)))
					delete(remaining, k)
					continue
				}
			}
		}
		output = append(output, l)
	}

	// Insert any remaining keys before first section
	if len(remaining) > 0 {
		var inserted []string
		for k, v := range remaining {
			inserted = append(inserted, fmt.Sprintf("%s = %s", k, tomlQuote(v)))
		}
		secIdx := -1
		for i, l := range output {
			if reSec.MatchString(l) {
				secIdx = i
				break
			}
		}
		if secIdx != -1 {
			prefix := append([]string{}, output[:secIdx]...)
			prefix = append(prefix, inserted...)
			prefix = append(prefix, "")
			output = append(prefix, output[secIdx:]...)
		} else {
			output = append(output, inserted...)
		}
	}

	return strings.TrimRight(strings.Join(output, "\n"), "\r\n") + "\n"
}
