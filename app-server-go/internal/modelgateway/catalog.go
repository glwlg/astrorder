package modelgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			// Never leak Authorization header across cross-origin redirects
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
}

// NormalizedModel represents a model in the catalog.
type NormalizedModel struct {
	Slug                  string   `json:"slug"`
	Provider              string   `json:"provider"`
	ID                    string   `json:"id"`
	ContextWindow         int      `json:"context_window"`
	MaxInputTokens        int      `json:"max_input_tokens"`
	AutoCompactTokenLimit int      `json:"auto_compact_token_limit"`
	InputModalities       []string `json:"input_modalities"`
	ReasoningEfforts      []string `json:"reasoning_efforts"`
	DefaultReasoningEffort string  `json:"default_reasoning_effort"`
	Native                bool     `json:"native"`
}

// Catalog represents a fetched and normalized catalog.
type Catalog struct {
	Models      []NormalizedModel `json:"models"`
	Fingerprint string            `json:"fingerprint"`
}

func fetchCatalog(ctx context.Context, client *http.Client, config GatewayConfig) (*Catalog, error) {
	gwType := strings.ToLower(strings.TrimSpace(config.GatewayType))
	if gwType == "magpie" {
		return fetchMagpieCatalog(ctx, client, config)
	}
	return fetchOpenCodeXCatalog(ctx, client, config)
}

func fetchMagpieCatalog(ctx context.Context, client *http.Client, config GatewayConfig) (*Catalog, error) {
	inferenceURL := strings.TrimRight(config.InferenceURL, "/")
	modelsURL := inferenceURL + "/models"
	if strings.HasSuffix(inferenceURL, "/models") {
		modelsURL = inferenceURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	if config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("magpie catalog request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("magpie catalog returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
	if err != nil {
		return nil, err
	}

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid magpie catalog json: %w", err)
	}

	var list []any
	if m, ok := raw.(map[string]any); ok {
		if data, ok := m["data"].([]any); ok {
			list = data
		}
	} else if l, ok := raw.([]any); ok {
		list = l
	}

	if list == nil {
		return nil, errors.New("Magpie 模型目录格式无效")
	}

	var models []NormalizedModel
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mid, _ := row["id"].(string)
		mid = strings.TrimSpace(mid)
		if mid == "" {
			continue
		}
		provider, _ := row["owned_by"].(string)
		provider = strings.TrimSpace(provider)
		if provider == "" {
			if strings.Contains(mid, "/") {
				provider = strings.Split(mid, "/")[0]
			} else {
				provider = "magpie"
			}
		}

		var efforts []string
		if levels, ok := row["supported_reasoning_levels"].([]any); ok {
			for _, lvl := range levels {
				if lm, ok := lvl.(map[string]any); ok {
					if eff, ok := lm["effort"].(string); ok && eff != "" {
						efforts = append(efforts, eff)
					}
				}
			}
		}

		cw := getInt(row["context_window"], getInt(row["context_length"], 0))
		maxInput := getInt(row["max_input_tokens"], 0)
		autoCompact := 0
		if cw > 0 {
			autoCompact = int(float64(cw) * 0.8)
		}

		defEffort := ""
		if len(efforts) > 1 {
			defEffort = efforts[1]
		} else if len(efforts) > 0 {
			defEffort = efforts[0]
		}

		models = append(models, NormalizedModel{
			Slug:                  mid,
			Provider:              provider,
			ID:                    mid,
			ContextWindow:         cw,
			MaxInputTokens:        maxInput,
			AutoCompactTokenLimit: autoCompact,
			InputModalities:       []string{"text", "image"},
			ReasoningEfforts:      efforts,
			DefaultReasoningEffort: defEffort,
			Native:                false,
		})
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Slug < models[j].Slug
	})

	canonicalJSON, _ := json.Marshal(models)
	h := sha256.Sum256(canonicalJSON)
	fingerprint := hex.EncodeToString(h[:])

	return &Catalog{
		Models:      models,
		Fingerprint: fingerprint,
	}, nil
}

func fetchOpenCodeXCatalog(ctx context.Context, client *http.Client, config GatewayConfig) (*Catalog, error) {
	mgmtURL := strings.TrimRight(config.ManagementURL, "/")

	// 1. Models
	modelsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, mgmtURL+"/api/models", nil)
	if err != nil {
		return nil, err
	}
	if config.APIKey != "" {
		modelsReq.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	modelsResp, err := client.Do(modelsReq)
	if err != nil {
		return nil, fmt.Errorf("opencodex models request: %w", err)
	}
	defer modelsResp.Body.Close()
	if modelsResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencodex models returned HTTP %d", modelsResp.StatusCode)
	}
	modelsBody, err := io.ReadAll(io.LimitReader(modelsResp.Body, 10_000_000))
	if err != nil {
		return nil, err
	}

	var rawModels []any
	if err := json.Unmarshal(modelsBody, &rawModels); err != nil {
		return nil, errors.New("OpenCodeX 模型目录格式无效")
	}

	// 2. Providers
	providersReq, err := http.NewRequestWithContext(ctx, http.MethodGet, mgmtURL+"/api/providers", nil)
	if err != nil {
		return nil, err
	}
	if config.APIKey != "" {
		providersReq.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	providersResp, err := client.Do(providersReq)
	if err != nil {
		return nil, fmt.Errorf("opencodex providers request: %w", err)
	}
	defer providersResp.Body.Close()
	if providersResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencodex providers returned HTTP %d", providersResp.StatusCode)
	}
	providersBody, err := io.ReadAll(io.LimitReader(providersResp.Body, 10_000_000))
	if err != nil {
		return nil, err
	}

	var rawProviders []any
	if err := json.Unmarshal(providersBody, &rawProviders); err != nil {
		return nil, errors.New("OpenCodeX 提供方目录格式无效")
	}

	disabledProviders := make(map[string]bool)
	for _, p := range rawProviders {
		if pm, ok := p.(map[string]any); ok {
			if dis, _ := pm["disabled"].(bool); dis {
				if name, _ := pm["name"].(string); name != "" {
					disabledProviders[name] = true
				}
			}
		}
	}

	var models []NormalizedModel
	for _, m := range rawModels {
		row, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if dis, _ := row["disabled"].(bool); dis {
			continue
		}
		provider, _ := row["provider"].(string)
		provider = strings.TrimSpace(provider)
		if provider == "" || disabledProviders[provider] {
			continue
		}

		slug, _ := row["namespaced"].(string)
		if slug == "" {
			slug, _ = row["id"].(string)
		}
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}

		mid, _ := row["id"].(string)
		if mid == "" {
			mid = slug
		}

		var efforts []string
		if rawEfforts, ok := row["reasoningEfforts"].([]any); ok {
			for _, eff := range rawEfforts {
				if s, ok := eff.(string); ok && s != "" {
					efforts = append(efforts, s)
				}
			}
		}

		defEffort, _ := row["defaultReasoningEffort"].(string)
		if defEffort == "" && len(efforts) > 0 {
			defEffort = efforts[0]
		}

		cw := getInt(row["contextWindow"], 0)
		maxInput := getInt(row["maxInputTokens"], 0)
		autoCompact := getInt(row["autoCompactTokenLimit"], 0)

		modalities := []string{"text"}
		if rawMods, ok := row["inputModalities"].([]any); ok {
			modSet := make(map[string]bool)
			for _, mod := range rawMods {
				if s, ok := mod.(string); ok && s != "" {
					modSet[s] = true
				}
			}
			modalities = nil
			for k := range modSet {
				modalities = append(modalities, k)
			}
			sort.Strings(modalities)
		}

		models = append(models, NormalizedModel{
			Slug:                  slug,
			Provider:              provider,
			ID:                    mid,
			ContextWindow:         cw,
			MaxInputTokens:        maxInput,
			AutoCompactTokenLimit: autoCompact,
			InputModalities:       modalities,
			ReasoningEfforts:      efforts,
			DefaultReasoningEffort: defEffort,
			Native:                false,
		})
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Slug < models[j].Slug
	})

	canonicalJSON, _ := json.Marshal(models)
	h := sha256.Sum256(canonicalJSON)
	fingerprint := hex.EncodeToString(h[:])

	return &Catalog{
		Models:      models,
		Fingerprint: fingerprint,
	}, nil
}

func getInt(v any, fallback int) int {
	if v == nil {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return fallback
}
