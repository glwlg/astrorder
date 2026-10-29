package settingsapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/modelgateway"
)

type jevPatch struct {
	Mode   *string         `json:"mode"`
	Model  *string         `json:"model"`
	APIKey json.RawMessage `json:"api_key"`
}

func (patch jevPatch) apply(config browser.JevConfig) (browser.JevConfig, *string, error) {
	if patch.Mode != nil {
		config.Mode = strings.TrimSpace(*patch.Mode)
	}
	if patch.Model != nil {
		config.Model = strings.TrimSpace(*patch.Model)
	}
	if err := config.Validate(); err != nil {
		return config, nil, err
	}
	var key *string
	if len(patch.APIKey) > 0 {
		if config.Mode == "gateway" {
			return config, nil, errors.New("网关模式使用 LLM 网关中保存的密钥")
		}
		if err := json.Unmarshal(patch.APIKey, &key); err != nil {
			return config, nil, errors.New("API Key 格式无效")
		}
		if key == nil {
			empty := ""
			key = &empty
		}
		clean := strings.TrimSpace(*key)
		key = &clean
	}
	return config, key, nil
}

func (h *handler) jevView(ctx context.Context) (map[string]any, error) {
	config, err := browser.GetJevConfig(ctx, h.Store)
	if err != nil {
		return nil, err
	}
	gateway, err := modelgateway.GetGatewayConfig(ctx, h.Store)
	if err != nil {
		return nil, err
	}
	key := browser.GetJevKey(ctx, h.Store)
	masked := ""
	switch {
	case len(key) > 20:
		masked = key[:10] + "..." + key[len(key)-8:]
	case key != "":
		masked = "已配置"
	}
	_, resolveErr := browser.ResolveJevConfig(ctx, h.Store, config, nil)
	return map[string]any{
		"mode": config.Mode, "model": config.Model, "configured": resolveErr == nil, "masked_key": masked,
		"gateway_type": gateway.GatewayType, "gateway_url": gateway.InferenceURL, "gateway_configured": gateway.APIKey != "",
	}, nil
}

func (h *handler) getJev(w http.ResponseWriter, r *http.Request) {
	view, err := h.jevView(r.Context())
	if err != nil {
		fail(w, 500, "读取 Jev 配置失败")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setJev(w http.ResponseWriter, r *http.Request) {
	var patch jevPatch
	if err := decode(r, &patch); err != nil {
		fail(w, 422, "Invalid request")
		return
	}
	config, err := browser.GetJevConfig(r.Context(), h.Store)
	if err != nil {
		fail(w, 500, "读取 Jev 配置失败")
		return
	}
	config, key, err := patch.apply(config)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	if config.Mode == "gateway" {
		if _, err := browser.ResolveJevConfig(r.Context(), h.Store, config, nil); err != nil {
			fail(w, 422, err.Error())
			return
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		fail(w, 500, "保存 Jev 配置失败")
		return
	}
	err = h.Store.WithTx(r.Context(), func(tx *sql.Tx) error {
		if key != nil {
			if *key == "" {
				if _, err := tx.ExecContext(r.Context(), `DELETE FROM workspace_preferences WHERE key=?`, prefJevKey); err != nil {
					return err
				}
			} else {
				encoded, err := json.Marshal(*key)
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(r.Context(), `INSERT INTO workspace_preferences(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, prefJevKey, string(encoded)); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(r.Context(), `INSERT INTO workspace_preferences(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, browser.JevConfigPreference, string(raw))
		return err
	})
	if err != nil {
		fail(w, 500, "保存 Jev 配置失败")
		return
	}
	h.getJev(w, r)
}

func (h *handler) jevModels(w http.ResponseWriter, r *http.Request) {
	gateway, err := modelgateway.GetGatewayConfig(r.Context(), h.Store)
	if err != nil {
		fail(w, 500, "读取 LLM 网关配置失败")
		return
	}
	if gateway.APIKey == "" {
		fail(w, 422, "请先在 LLM 网关中保存网关密钥")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(gateway.InferenceURL, "/")+"/models", nil)
	if err != nil {
		fail(w, 422, "LLM 网关推理地址无效")
		return
	}
	req.Header.Set("Authorization", "Bearer "+gateway.APIKey)
	// Magpie omits decision models from its ordinary chat-model catalog.
	req.Header.Set("X-Magpie-Deciders", "1")
	client := *h.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		fail(w, 502, "读取 System One 模型失败，请检查网关连接")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fail(w, 502, "网关拒绝读取 System One 模型，请检查网关密钥")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
	if err != nil || len(raw) > 10<<20 {
		fail(w, 502, "网关模型目录过大或读取失败")
		return
	}
	var catalog struct {
		Data []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Label string `json:"magpie_label"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &catalog) != nil || catalog.Data == nil {
		fail(w, 502, "网关模型目录格式无效")
		return
	}
	items := []map[string]string{}
	for _, model := range catalog.Data {
		if model.Kind != "decision" || model.ID == "" {
			continue
		}
		label := model.Label
		if label == "" {
			label = model.ID
		}
		items = append(items, map[string]string{"id": model.ID, "label": label})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["id"] < items[j]["id"] })
	writeJSON(w, 200, map[string]any{"items": items, "gateway_type": gateway.GatewayType, "gateway_url": gateway.InferenceURL})
}
