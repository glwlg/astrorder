package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

const JevConfigPreference = "services:jev"

type JevConfig struct {
	Mode  string `json:"mode"`
	Model string `json:"model"`
}

func (c JevConfig) Validate() error {
	if c.Mode != "official" && c.Mode != "gateway" {
		return errors.New("请选择通过 LLM 网关或 Jev 官方")
	}
	if c.Mode == "gateway" && strings.TrimSpace(c.Model) == "" {
		return errors.New("请选择 System One 模型")
	}
	return nil
}

func GetJevConfig(ctx context.Context, st *store.Store) (JevConfig, error) {
	config := JevConfig{Mode: "official"}
	if st == nil {
		return config, nil
	}
	pref, err := st.GetPreference(ctx, JevConfigPreference)
	if err != nil || !pref.Exists {
		return config, err
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, err
	}
	return config, config.Validate()
}

type JevEndpoint struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

func ResolveJevConfig(ctx context.Context, st *store.Store, config JevConfig, officialKey *string) (JevEndpoint, error) {
	if err := config.Validate(); err != nil {
		return JevEndpoint{}, err
	}
	if config.Mode == "official" {
		key := GetJevKey(ctx, st)
		if officialKey != nil {
			key = strings.TrimSpace(*officialKey)
		}
		if key == "" {
			return JevEndpoint{}, errors.New("请配置 Jev 官方 API Key")
		}
		return JevEndpoint{URL: defaultJevURL, APIKey: key, Model: "jev-latest"}, nil
	}
	if st == nil {
		return JevEndpoint{}, errors.New("LLM 网关配置不可用")
	}
	gateway, err := modelgateway.GetGatewayConfig(ctx, st)
	if err != nil {
		return JevEndpoint{}, err
	}
	if gateway.APIKey == "" {
		return JevEndpoint{}, errors.New("请先在 LLM 网关中保存网关密钥")
	}
	base, err := url.Parse(strings.TrimRight(gateway.InferenceURL, "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return JevEndpoint{}, errors.New("LLM 网关推理地址无效")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/systemone"
	return JevEndpoint{URL: base.String(), APIKey: gateway.APIKey, Model: strings.TrimSpace(config.Model)}, nil
}
