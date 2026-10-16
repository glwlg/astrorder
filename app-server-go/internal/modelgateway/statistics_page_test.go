package modelgateway_test

import (
	"astrorder.dev/app-server/internal/modelgateway"
	"context"
	"testing"
)

func TestStatisticsPageURLsFollowGateway(t *testing.T) {
	st, _, _, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()
	ctx := context.Background()
	for _, payload := range []map[string]any{
		{"gateway_type": "magpie", "statistics_url": "https://magpie.example/?k=fixture#usage"},
		{"gateway_type": "opencodex"},
	} {
		if _, err := modelgateway.SaveGatewayConfig(ctx, st, payload); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := modelgateway.GetGatewayConfig(ctx, st)
	if err != nil || loaded.StatisticsURL != "" {
		t.Fatal("switching to an unconfigured gateway carried the other statistics URL")
	}
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"statistics_url": "https://ocx.example/#usage"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ gateway, address string }{
		{"magpie", "https://magpie.example/?k=fixture#usage"},
		{"opencodex", "https://ocx.example/#usage"},
	} {
		if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"gateway_type": tc.gateway}); err != nil {
			t.Fatal(err)
		}
		loaded, err := modelgateway.GetGatewayConfig(ctx, st)
		if err != nil || loaded.ToPublic().StatisticsURL != tc.address {
			t.Fatal("gateway statistics URL was not restored after save and reload")
		}
	}
}

func TestStatisticsPageURLMigratesOnlyToActiveGateway(t *testing.T) {
	st, _, _, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()
	ctx := context.Background()
	const address = "https://magpie.example/?k=fixture#usage"
	if err := st.SetPreference(ctx, modelgateway.PreferenceKey, map[string]any{
		"gateway_type": "magpie", "statistics_url": address,
		"gateway_profiles": map[string]any{
			"magpie":    map[string]any{"gateway_type": "magpie"},
			"opencodex": map[string]any{"gateway_type": "opencodex"},
		},
	}, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := modelgateway.GetGatewayConfig(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	pub := loaded.ToPublic()
	if pub.GatewayProfiles["magpie"].StatisticsURL != address || pub.GatewayProfiles["opencodex"].StatisticsURL != "" {
		t.Fatal("legacy statistics URL was not scoped to its active gateway")
	}
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"gateway_type": "opencodex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{
		"gateway_profiles": map[string]any{
			"magpie":    map[string]any{"statistics_url": ""},
			"opencodex": map[string]any{"statistics_url": "https://ocx.example/#usage"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{
		"gateway_profiles": map[string]any{"magpie": map[string]any{"statistics_url": "javascript:alert(1)"}},
	}); err == nil {
		t.Fatal("invalid inactive profile statistics URL accepted")
	}
	loaded, err = modelgateway.GetGatewayConfig(ctx, st)
	if err != nil || loaded.StatisticsURL != "https://ocx.example/#usage" || loaded.ToPublic().GatewayProfiles["magpie"].StatisticsURL != "" {
		t.Fatal("clearing one profile changed another, or failed validation changed stored profiles")
	}
	loaded, err = modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"gateway_type": "magpie"})
	if err != nil || loaded.StatisticsURL != "" {
		t.Fatal("cleared statistics URL returned on gateway switch")
	}
}

func TestStatisticsPageURLPersistsAndValidates(t *testing.T) {
	st, _, _, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()
	const address = "https://gateway.example/usage?view=requests#today"
	saved, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{"statistics_url": address})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ToPublic().StatisticsURL != address {
		t.Fatal("statistics URL not preserved")
	}
	loaded, err := modelgateway.GetGatewayConfig(context.Background(), st)
	if err != nil || loaded.ToPublic().StatisticsURL != address {
		t.Fatal("statistics URL not persisted")
	}
	for _, invalid := range []string{"javascript:alert(1)", "file:///C:/windows", "https://user@gateway.example/"} {
		if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{"statistics_url": invalid}); err == nil {
			t.Fatal("invalid statistics URL accepted")
		}
	}
	cleared, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{"statistics_url": ""})
	if err != nil || cleared.ToPublic().StatisticsURL != "" {
		t.Fatal("statistics URL cannot be cleared")
	}
}
