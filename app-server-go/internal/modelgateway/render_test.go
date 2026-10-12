package modelgateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseCatalogModels(t *testing.T, rendered string) []map[string]any {
	t.Helper()
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered catalog is not valid JSON: %v\n%s", err, rendered)
	}
	return doc.Models
}

// The golden file was cut byte-for-byte from a Python-era magpie-catalog.json
// (the reference format the user confirmed is correct). Rendering the same
// model with the same previous content must reproduce the file exactly.
func TestRenderCodexCatalogByteMatchesPythonGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "magpie_catalog_model_golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	var doc struct {
		Models []any `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("golden not valid JSON: %v", err)
	}
	previous := doc.Models[0]

	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "antigravity/gemini-3.8-flash",
		Provider:               "antigravity",
		ID:                     "antigravity/gemini-3.8-flash",
		ContextWindow:          500000,
		AutoCompactTokenLimit:  400000,
		InputModalities:        []string{"text", "image"},
		ReasoningEfforts:       []string{"low", "medium", "high"},
		DefaultReasoningEffort: "medium",
	}}}

	got := RenderCodexCatalog(catalog, []any{previous}, "Magpie")
	if got != string(raw) {
		t.Fatalf("rendered catalog does not byte-match the Python reference format.\n--- want ---\n%s\n--- got ---\n%s", raw, got)
	}
}

func TestRenderCodexCatalogWritesCodexSchemaFields(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "codex/gpt-6-astra",
		Provider:               "codex",
		ID:                     "codex/gpt-6-astra",
		ContextWindow:          272000,
		InputModalities:        []string{"text", "image"},
		ReasoningEfforts:       []string{"low", "medium", "high", "xhigh", "max"},
		DefaultReasoningEffort: "medium",
	}}}

	models := parseCatalogModels(t, RenderCodexCatalog(catalog, nil, "Magpie"))
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]

	if m["slug"] != "codex/gpt-6-astra" {
		t.Fatalf("slug missing or wrong: %v", m["slug"])
	}
	if m["display_name"] != "codex/gpt-6-astra" {
		t.Fatalf("display_name missing or wrong: %v", m["display_name"])
	}
	if m["description"] != "由 Magpie 提供" {
		t.Fatalf("description wrong: %v", m["description"])
	}
	if m["default_reasoning_level"] != "medium" {
		t.Fatalf("default_reasoning_level wrong: %v", m["default_reasoning_level"])
	}
	if m["context_window"] != float64(272000) {
		t.Fatalf("context_window wrong: %v", m["context_window"])
	}
	if m["max_context_window"] != float64(272000) {
		t.Fatalf("max_context_window wrong: %v", m["max_context_window"])
	}
	// Derived compaction limit: 272000 * 4 / 5 = 217600.
	if m["auto_compact_token_limit"] != float64(217600) {
		t.Fatalf("auto_compact_token_limit wrong: %v", m["auto_compact_token_limit"])
	}
	if m["visibility"] != "list" || m["priority"] != float64(5) {
		t.Fatalf("prototype defaults missing: visibility=%v priority=%v", m["visibility"], m["priority"])
	}
	for _, bad := range []string{"id", "name", "native", "provider", "max_input_tokens", "default_reasoning_effort"} {
		if _, exists := m[bad]; exists {
			t.Fatalf("codex rejects/stale field %q must not be written", bad)
		}
	}
}

func TestRenderCodexCatalogPreservesPreviousCustomFields(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "xai/grok-4.7",
		Provider:               "xai",
		ContextWindow:          500000,
		ReasoningEfforts:       []string{"low", "medium", "high"},
		DefaultReasoningEffort: "medium",
	}}}
	previous := map[string]any{
		"slug":         "xai/grok-4.7",
		"custom":       true,
		"display_name": "Custom Grok",
	}

	models := parseCatalogModels(t, RenderCodexCatalog(catalog, []any{previous}, "Magpie"))
	m := models[0]
	if m["custom"] != true {
		t.Fatalf("previous custom field lost: %v", m)
	}
	if m["display_name"] != "Custom Grok" {
		t.Fatalf("previous display_name must win (python parity): %v", m["display_name"])
	}
	if m["slug"] != "xai/grok-4.7" {
		t.Fatalf("slug wrong: %v", m["slug"])
	}
}

func TestRenderCodexCatalogFillsMissingReasoningLevels(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "xai/grok-4.7",
		Provider:               "xai",
		DefaultReasoningEffort: "medium",
	}}}
	previous := map[string]any{
		"slug":                      "xai/grok-4.7",
		"default_reasoning_level":   "medium",
		"supported_reasoning_levels": []any{},
	}

	models := parseCatalogModels(t, RenderCodexCatalog(catalog, []any{previous}, "Magpie"))
	m := models[0]
	levels := m["supported_reasoning_levels"].([]any)
	if len(levels) != 1 {
		t.Fatalf("expected single fallback level, got %v", levels)
	}
	lvl := levels[0].(map[string]any)
	if lvl["effort"] != "medium" || lvl["description"] != "medium" {
		t.Fatalf("fallback level wrong: %v", lvl)
	}
	if m["default_reasoning_level"] != "medium" {
		t.Fatalf("default level wrong: %v", m["default_reasoning_level"])
	}
}

func TestRenderCodexCatalogPreservesPreviousLevelsWhenGatewayOmitsThem(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "xjj-openai/deepseek-v4-flash",
		Provider:               "xjj-openai",
		DefaultReasoningEffort: "medium",
	}}}
	previous := map[string]any{
		"slug":                    "xjj-openai/deepseek-v4-flash",
		"default_reasoning_level": "medium",
		"supported_reasoning_levels": []any{
			map[string]any{"effort": "low", "description": "Fast"},
			map[string]any{"effort": "medium", "description": "Balanced"},
			map[string]any{"effort": "high", "description": "Deep"},
		},
	}

	models := parseCatalogModels(t, RenderCodexCatalog(catalog, []any{previous}, "Magpie"))
	m := models[0]
	levels := m["supported_reasoning_levels"].([]any)
	if len(levels) != 3 {
		t.Fatalf("expected previous levels preserved, got %v", levels)
	}
	if levels[1].(map[string]any)["description"] != "Balanced" {
		t.Fatalf("previous level descriptions lost: %v", levels)
	}
}

func TestRenderCodexCatalogUsesPreviousDescriptionsForGatewayEfforts(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "xai/grok-4.7",
		Provider:               "xai",
		ReasoningEfforts:       []string{"low", "high"},
		DefaultReasoningEffort: "high",
	}}}
	previous := map[string]any{
		"slug": "xai/grok-4.7",
		"supported_reasoning_levels": []any{
			map[string]any{"effort": "low", "description": "Fast"},
			map[string]any{"effort": "high", "description": "Deep"},
		},
	}

	models := parseCatalogModels(t, RenderCodexCatalog(catalog, []any{previous}, "Magpie"))
	levels := models[0]["supported_reasoning_levels"].([]any)
	if levels[0].(map[string]any)["description"] != "Fast" {
		t.Fatalf("old description not reused: %v", levels)
	}
	if levels[1].(map[string]any)["description"] != "Deep" {
		t.Fatalf("old description not reused: %v", levels)
	}
}

func TestRenderCodexCatalogOmitsZeroCompactionLimitWithoutWindow(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{
		Slug:                   "xai/grok-4.7",
		Provider:               "xai",
		DefaultReasoningEffort: "medium",
	}}}

	rendered := RenderCodexCatalog(catalog, nil, "Magpie")
	if strings.Contains(rendered, "auto_compact_token_limit") {
		t.Fatalf("zero compaction limit must not be written:\n%s", rendered)
	}
	models := parseCatalogModels(t, rendered)
	if models[0]["context_window"] != float64(0) {
		t.Fatalf("context_window should be 0: %v", models[0]["context_window"])
	}
}

// Full-file parity: the golden is the complete 8-model Python-era
// magpie-catalog.json. Re-rendering with the same gateway input (a no-op
// re-sync) must reproduce the file byte for byte, including key order.
func TestRenderCodexCatalogFullFileByteParity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "magpie_catalog_full_golden.json"))
	if err != nil {
		t.Fatalf("read full golden: %v", err)
	}

	var doc struct {
		Models []any `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("full golden not valid JSON: %v", err)
	}

	specs := []struct {
		slug    string
		efforts []string
		window  int
	}{
		{"antigravity/gemini-3.8-flash", []string{"low", "medium", "high"}, 500000},
		{"antigravity/gemini-3.8-flash-tiered", []string{"medium"}, 500000},
		{"codex/gpt-5.6-luna", []string{"low", "medium", "high", "xhigh", "max"}, 272000},
		{"codex/gpt-5.6-sol", []string{"low", "medium", "high", "xhigh", "max", "ultra"}, 272000},
		{"codex/gpt-5.6-terra", []string{"low", "medium", "high", "xhigh", "max", "ultra"}, 272000},
		{"codex/gpt-6-astra", []string{"low", "medium", "high", "xhigh", "max", "ultra"}, 272000},
		{"codex/gpt-6-luna", []string{"low", "medium", "high", "xhigh", "max"}, 272000},
		{"codex/gpt-6-sol", []string{"low", "medium", "high", "xhigh", "max", "ultra"}, 272000},
	}
	catalog := &Catalog{}
	for _, s := range specs {
		catalog.Models = append(catalog.Models, NormalizedModel{
			Slug:                   s.slug,
			Provider:               strings.SplitN(s.slug, "/", 2)[0],
			ContextWindow:          s.window,
			InputModalities:        []string{"text", "image"},
			ReasoningEfforts:       s.efforts,
			DefaultReasoningEffort: "medium",
		})
	}

	got := RenderCodexCatalog(catalog, doc.Models, "Magpie")
	if got == string(raw) {
		return
	}
	// Locate the first differing line for a readable failure.
	wantLines := strings.Split(string(raw), "\n")
	gotLines := strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			lo := i - 3
			if lo < 0 {
				lo = 0
			}
			t.Fatalf("full-file parity broken at line %d:\n--- want[%d:%d] ---\n%s\n--- got[%d:%d] ---\n%s",
				i+1, lo, i+4, strings.Join(wantLines[lo:min(i+4, len(wantLines))], "\n"),
				lo, i+4, strings.Join(gotLines[lo:min(i+4, len(gotLines))], "\n"))
		}
	}
	t.Fatalf("full-file parity broken: same lines but different content (len want=%d got=%d)", len(raw), len(got))
}
