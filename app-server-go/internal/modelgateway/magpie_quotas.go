package modelgateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type magpieCatalogModel struct {
	ID    string `json:"id"`
	Owner string `json:"owned_by"`
}

func decodeMagpieModels(body io.Reader) ([]magpieCatalogModel, error) {
	var list struct {
		Data []magpieCatalogModel `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(body, (10<<20)+1))
	if err != nil || len(raw) > 10<<20 {
		return nil, fmt.Errorf("model response too large")
	}
	if err = json.Unmarshal(raw, &list); err != nil || list.Data == nil {
		return nil, fmt.Errorf("invalid model list")
	}
	return list.Data, nil
}

type magpieQuotaWindow struct {
	Name      string     `json:"name"`
	Used      float64    `json:"used"`
	Remaining float64    `json:"remaining"`
	ResetsAt  *time.Time `json:"resetsAt,omitempty"`
	Unlimited bool       `json:"unlimited,omitempty"`
	Display   string     `json:"display,omitempty"`
}
type magpieQuota struct {
	Provider     string              `json:"provider"`
	Name         string              `json:"name"`
	Kind         string              `json:"kind"`
	User         string              `json:"user,omitempty"`
	Plan         string              `json:"plan,omitempty"`
	Alias        string              `json:"alias,omitempty"`
	From         string              `json:"from,omitempty"`
	Windows      []magpieQuotaWindow `json:"windows"`
	Balance      string              `json:"balance,omitempty"`
	Error        string              `json:"error,omitempty"`
	AsOf         *time.Time          `json:"asOf,omitempty"`
	ReadAt       *time.Time          `json:"readAt,omitempty"`
	ReadDeferred bool                `json:"readDeferred,omitempty"`
}

func (h *Handler) magpieQuotas(r *http.Request, cfg GatewayConfig) ([]magpieQuota, error) {
	var list struct {
		Data []magpieQuota `json:"data"`
	}
	if err := h.readMagpie(r, cfg, "quotas", url.Values{}, &list); err != nil {
		return nil, err
	}
	if list.Data == nil {
		list.Data = []magpieQuota{}
	}
	return list.Data, nil
}
func (h *Handler) handleGetMagpieQuotas(w http.ResponseWriter, r *http.Request) {
	cfg, err := GetGatewayConfig(r.Context(), h.store)
	if err != nil {
		writeError(w, 500, "config_error", err.Error())
		return
	}
	if cfg.GatewayType != "magpie" {
		writeJSON(w, 200, map[string]any{"gatewayType": cfg.GatewayType, "data": []magpieQuota{}})
		return
	}
	qs, err := h.magpieQuotas(r, cfg)
	if err != nil {
		writeError(w, 502, "upstream_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"gatewayType": "magpie", "data": qs})
}
func (h *Handler) serveMagpieModelQuota(w http.ResponseWriter, r *http.Request, cfg GatewayConfig, model string) {
	qs, err := h.magpieQuotas(r, cfg)
	if err != nil {
		writeError(w, 502, "upstream_error", err.Error())
		return
	}
	provider, candidate, qualified := strings.Cut(model, "/")
	// Native bindings carry the gateway provider before an already-qualified model ID.
	if nested, rest, ok := strings.Cut(candidate, "/"); qualified && ok && (provider == "magpie" || provider == nested) {
		provider, candidate = nested, rest
	}
	if !qualified {
		candidate = model
		// Resolve unqualified models from the catalog rather than guessing the provider.
		endpoint, err := magpieEndpoint(cfg, "")
		if err != nil {
			writeError(w, 502, "upstream_error", err.Error())
			return
		}
		endpoint = strings.TrimSuffix(endpoint, "/magpie/") + "/models"
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			writeError(w, 502, "upstream_error", "Magpie 模型地址无效")
			return
		}
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		resp, err := h.httpClient.Do(req)
		if err != nil {
			writeError(w, 502, "upstream_error", "Magpie 模型列表读取失败")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			writeError(w, 502, "upstream_error", fmt.Sprintf("Magpie 模型列表返回 HTTP %d", resp.StatusCode))
			return
		}
		models, err := decodeMagpieModels(resp.Body)
		if err != nil {
			writeError(w, 502, "upstream_error", "Magpie 模型列表格式无效")
			return
		}
		for _, m := range models {
			if m.ID == model || strings.HasSuffix(m.ID, "/"+model) {
				provider = m.Owner
				if p, _, ok := strings.Cut(m.ID, "/"); ok {
					provider = p
				}
				break
			}
		}
		if provider == "" || provider == model {
			writeJSON(w, 200, map[string]any{"model": candidate, "providers": []string{}, "reports": []any{}, "accounts": []any{}})
			return
		}
	}
	accounts := []map[string]any{}
	reports := []map[string]any{}
	providers := []string{}
	for i, q := range qs {
		if q.Provider != provider {
			continue
		}
		if len(providers) == 0 {
			providers = append(providers, provider)
		}
		custom := []map[string]any{}
		compat := map[string]any{}
		for _, win := range q.Windows {
			if win.Unlimited {
				continue
			}
			row := map[string]any{"label": win.Name, "percent": win.Used}
			if win.ResetsAt != nil {
				row["resetAt"] = win.ResetsAt.UnixMilli()
			}
			custom = append(custom, row)
			name := strings.ToLower(strings.ReplaceAll(win.Name, " ", ""))
			percentKey, resetKey := "", ""
			switch name {
			case "5h", "5hour", "5hours":
				percentKey = "shortPercent"
				resetKey = "shortResetAt"
			case "7d", "week", "weekly":
				percentKey = "weeklyPercent"
				resetKey = "weeklyResetAt"
			}
			if percentKey != "" {
				compat[percentKey] = win.Used
				if win.ResetsAt != nil {
					compat[resetKey] = win.ResetsAt.UnixMilli()
				}
			}
		}
		compat["customWindows"] = custom
		label := q.Alias
		if label == "" {
			label = q.User
		}
		if label == "" {
			label = q.Name
		}
		// Errors and balances never become an invented 100% allowance.
		if len(custom) == 0 {
			continue
		}
		accounts = append(accounts, map[string]any{"id": fmt.Sprintf("%s/%s/%d", q.Provider, q.User, i), "email": q.User, "logLabel": label, "plan": q.Plan, "error": q.Error, "quota": compat})
	}
	writeJSON(w, 200, map[string]any{"model": candidate, "providers": providers, "reports": reports, "accounts": accounts})
}
