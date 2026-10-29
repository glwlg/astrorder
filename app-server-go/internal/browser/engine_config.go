package browser

import (
	"context"
	"errors"

	"astrorder.dev/app-server/internal/store"
)

const BrowserEnginePreference = "services:browser_engine"

func GetBrowserEngine(ctx context.Context, st *store.Store) (string, error) {
	if st == nil {
		return "browser_use", nil
	}
	pref, err := st.GetPreference(ctx, BrowserEnginePreference)
	if err != nil {
		return "", err
	}
	if !pref.Exists {
		return "browser_use", nil
	}
	engine, ok := pref.Value.(string)
	if !ok || (engine != "browser_use" && engine != "jev_ultrafast") {
		return "", errors.New("浏览器引擎配置无效")
	}
	return engine, nil
}
