package pluginhost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"astrorder.dev/app-server/internal/auth"
)

type cookieOrBearerAuthorizer struct {
	expectedToken string
}

func (a cookieOrBearerAuthorizer) AuthorizeBrowser(r *http.Request) *auth.Error {
	if strings.TrimSpace(r.Header.Get("Authorization")) == "Bearer "+a.expectedToken {
		return nil
	}
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value == a.expectedToken {
		return nil
	}
	return &auth.Error{Status: http.StatusUnauthorized, Detail: "Authentication required"}
}

func TestSDKESMShimsServeExplicitJSMIMEAndExactExports(t *testing.T) {
	mgr := NewManager(Options{
		Store:      newTestStore(t),
		Authorizer: cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
	})
	defer mgr.Close()

	expectedCounts := map[string]struct {
		prop    string
		count   int
		mustHit []string
	}{
		"react.js":                 {prop: "react", count: 37, mustHit: []string{"useState", "useEffect", "createElement", "Fragment", "version"}},
		"jsx-runtime.js":           {prop: "jsxRuntime", count: 3, mustHit: []string{"Fragment", "jsx", "jsxs"}},
		"react-dom.js":             {prop: "reactDom", count: 12, mustHit: []string{"createPortal", "flushSync", "useFormStatus"}},
		"react-dom-client.js":      {prop: "reactDomClient", count: 2, mustHit: []string{"createRoot", "hydrateRoot"}},
		"mantine-core.js":          {prop: "mantineCore", count: 63, mustHit: []string{"ActionIcon", "Button", "Group", "LoadingOverlay", "Paper", "Text", "useComputedColorScheme", "Box", "Flex", "Skeleton", "Space"}},
		"mantine-notifications.js": {prop: "mantineNotifications", count: 12, mustHit: []string{"notifications", "showNotification", "useNotifications"}},
		"sdk.js":                   {prop: "sdk", count: 18, mustHit: []string{"SDK_VERSION", "defineAstrorderPlugin", "downloadFile", "getActiveSession", "getHostRuntime", "getSessions", "openArtifact", "openDatabaseWorkbench", "openExternal", "openPluginPanel", "openSettings", "quoteToComposer", "usePluginConfig", "useThemeMode"}},
	}

	if len(sdkESMModuleSpecs) != len(expectedCounts) {
		t.Fatalf("unexpected sdk shim module count: got %d want %d", len(sdkESMModuleSpecs), len(expectedCounts))
	}

	for name, want := range expectedCounts {
		spec := sdkESMModuleSpecs[name]
		if len(spec.exports) != want.count {
			t.Fatalf("%s export count = %d, want %d", name, len(spec.exports), want.count)
		}

		// Public GET without auth MUST succeed with explicit text/javascript MIME + nosniff.
		req := httptest.NewRequest(http.MethodGet, SDKESMDirPath+name, nil)
		rec := httptest.NewRecorder()
		mgr.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", name, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
			t.Fatalf("GET %s Content-Type=%q, want text/javascript; charset=utf-8", name, ct)
		}
		if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
			t.Fatalf("GET %s X-Content-Type-Options=%q, want nosniff", name, nosniff)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "globalThis.__ASTRORDER_PLUGIN_RUNTIME_V1__?."+want.prop) {
			t.Fatalf("GET %s missing runtime prop %q: %s", name, want.prop, body)
		}
		if !strings.Contains(body, "export default M;") {
			t.Fatalf("GET %s missing default export", name)
		}
		for _, sym := range want.mustHit {
			if !strings.Contains(body, sym) {
				t.Fatalf("GET %s missing expected symbol %q", name, sym)
			}
		}

		// HEAD succeeds with empty body.
		headReq := httptest.NewRequest(http.MethodHead, SDKESMDirPath+name, nil)
		headRec := httptest.NewRecorder()
		mgr.ServeHTTP(headRec, headReq)
		if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 {
			t.Fatalf("HEAD %s status=%d body_len=%d", name, headRec.Code, headRec.Body.Len())
		}

		// POST rejected with 405.
		postReq := httptest.NewRequest(http.MethodPost, SDKESMDirPath+name, nil)
		postRec := httptest.NewRecorder()
		mgr.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status=%d, want 405", name, postRec.Code)
		}
	}

	// Existing bridge.js also serves explicit RFC 9239 text/javascript MIME + nosniff.
	bridgeRec := httptest.NewRecorder()
	mgr.ServeHTTP(bridgeRec, httptest.NewRequest(http.MethodGet, SDKBridgePath, nil))
	if bridgeRec.Code != http.StatusOK || bridgeRec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || bridgeRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("GET bridge.js status=%d ct=%q nosniff=%q", bridgeRec.Code, bridgeRec.Header().Get("Content-Type"), bridgeRec.Header().Get("X-Content-Type-Options"))
	}

	// Unknown SDK module returns 404.
	missRec := httptest.NewRecorder()
	mgr.ServeHTTP(missRec, httptest.NewRequest(http.MethodGet, SDKESMDirPath+"tabler-icons.js", nil))
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown SDK module, got %d", missRec.Code)
	}
}

func TestManifestFrontendPagesViewersAndDefaultEnabled(t *testing.T) {
	validJSON := `{
		"schema_version": 1,
		"id": "demo-esm",
		"name": "Demo ESM",
		"version": "1.0.0",
		"default_enabled": false,
		"backend": {"type": "none"},
		"frontend": {
			"mode": "esm",
			"entry": "ui/entry.js",
			"styles": ["ui/entry.css"]
		},
		"contributes": {
			"pages": [
				{"id": "main", "title": "Main Page", "path": "/demo", "order": 10, "hide_in_office_mode": true}
			],
			"viewers": [
				{"id": "demo-viewer", "title": "Demo Viewer", "extensions": [".STL", ".glb"], "mime_types": ["model/gltf-binary"], "badge_label": "3D", "tab_color": "violet", "category": "media"}
			]
		}
	}`
	m, err := ParseManifest([]byte(validJSON))
	if err != nil {
		t.Fatalf("ParseManifest valid ESM: %v", err)
	}
	if m.Frontend == nil || m.Frontend.Mode != FrontendModeESM || m.Frontend.SDKVersion != "^1.0" {
		t.Fatalf("unexpected normalized frontend: %+v", m.Frontend)
	}
	if m.DefaultEnabled == nil || *m.DefaultEnabled != false {
		t.Fatalf("expected default_enabled=false, got %v", m.DefaultEnabled)
	}
	if len(m.Contributes.Viewers) != 1 || m.Contributes.Viewers[0].Extensions[0] != ".stl" {
		t.Fatalf("expected lowercased viewer extensions, got %+v", m.Contributes.Viewers)
	}
	if EffectiveDefaultEnabled(SourceOfficial, m) != false {
		t.Fatalf("expected official default_enabled=false")
	}

	trueVal := true
	mTrue := m
	mTrue.DefaultEnabled = &trueVal
	if !EffectiveDefaultEnabled(SourceOfficial, mTrue) {
		t.Fatalf("expected official explicit default_enabled=true")
	}
	if EffectiveDefaultEnabled(SourceExternal, mTrue) {
		t.Fatalf("external plugin must NEVER default to enabled even if manifest sets default_enabled=true")
	}

	invalidCases := []struct {
		name string
		raw  string
	}{
		{"esm_missing_entry", `{"schema_version":1,"id":"bad-esm","name":"X","version":"1.0","frontend":{"mode":"esm"}}`},
		{"esm_traversal_entry", `{"schema_version":1,"id":"bad-esm","name":"X","version":"1.0","frontend":{"mode":"esm","entry":"../evil.js"}}`},
		{"esm_hidden_entry", `{"schema_version":1,"id":"bad-esm","name":"X","version":"1.0","frontend":{"mode":"esm","entry":".secret/entry.js"}}`},
		{"esm_traversal_style", `{"schema_version":1,"id":"bad-esm","name":"X","version":"1.0","frontend":{"mode":"esm","entry":"ui/entry.js","styles":["../a.css"]}}`},
		{"esm_bad_mode", `{"schema_version":1,"id":"bad-esm","name":"X","version":"1.0","frontend":{"mode":"script","entry":"ui/entry.js"}}`},
		{"dup_page", `{"schema_version":1,"id":"bad-p","name":"X","version":"1.0","contributes":{"pages":[{"id":"p1","title":"A"},{"id":"p1","title":"B"}]}}`},
		{"missing_viewer_title", `{"schema_version":1,"id":"bad-v","name":"X","version":"1.0","contributes":{"viewers":[{"id":"v1"}]}}`},
	}
	for _, tc := range invalidCases {
		if _, err := ParseManifest([]byte(tc.raw)); err == nil {
			t.Fatalf("case %s: expected validation error", tc.name)
		}
	}
}

func TestOfficialPluginsDirESMServingSecurityMIMEAndDigestReload(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "office"); err != nil {
		t.Fatalf("set office profile: %v", err)
	}

	root := t.TempDir()
	officialDir := filepath.Join(root, "official")
	externalDir := filepath.Join(root, "external")

	pluginDir := filepath.Join(officialDir, "three-demo")
	if err := os.MkdirAll(filepath.Join(pluginDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{
		"schema_version": 1,
		"id": "three-demo",
		"name": "Three Demo",
		"version": "1.0.0",
		"category": "media",
		"default_enabled": false,
		"backend": {"type": "none"},
		"frontend": {"mode": "esm", "entry": "ui/entry.js", "styles": ["ui/entry.css"], "sdk_version": "^1.0"},
		"contributes": {
			"viewers": [{"id": "three-demo", "title": "3D", "extensions": [".glb", ".stl"]}]
		}
	}`), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "entry.js"), []byte(`export function activate(){ return "v1"; }`), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "entry.css"), []byte(`.three{color:red}`), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "meta.json"), []byte(`{"ok":true}`), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "kernel.wasm"), []byte("\x00asm\x01\x00\x00\x00"), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "icon.svg"), []byte(`<svg/>`), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "tex.png"), []byte{0x89, 'P', 'N', 'G'}, 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, ".env"), []byte("SECRET=1"), 0o644)

	outsideFile := filepath.Join(root, "outside.js")
	_ = os.WriteFile(outsideFile, []byte("console.log('leaked')"), 0o644)
	_ = os.Symlink(outsideFile, filepath.Join(pluginDir, "ui", "escape.js"))
	// In-directory symlink pointing to hidden .env MUST also be blocked by both _esm and _ui!
	_ = os.Symlink(filepath.Join("..", ".env"), filepath.Join(pluginDir, "ui", "hidden-alias.js"))

	// Seed a colliding external plugin with same ID to verify official cannot be shadowed.
	shadowDir := filepath.Join(externalDir, "three-demo")
	_ = os.MkdirAll(shadowDir, 0o755)
	_ = os.WriteFile(filepath.Join(shadowDir, "plugin.json"), []byte(`{"schema_version":1,"id":"three-demo","name":"Evil Shadow","version":"9.9.9","backend":{"type":"none"}}`), 0o644)

	var mu sync.Mutex
	var events []ExtensionChangeEvent

	mgr := NewManager(Options{
		Store:              st,
		Authorizer:         cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
		PluginsDir:         externalDir,
		OfficialPluginsDir: officialDir,
		OnChange: func(ev ExtensionChangeEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	defer mgr.Close()

	// 1. Discovered as SourceOfficial, disabled by default, with digest & esm_base_url, not shadowed.
	view, err := mgr.GetPlugin(ctx, "three-demo")
	if err != nil {
		t.Fatalf("GetPlugin three-demo: %v", err)
	}
	if view.Source != SourceOfficial || view.Name != "Three Demo" || view.Enabled || view.DefaultEnabled {
		t.Fatalf("unexpected official view: %+v", view)
	}
	if len(view.ContentDigest) != 16 || view.ESMBaseURL != "/api/v1/ext/three-demo/_esm" {
		t.Fatalf("unexpected digest/esm_base_url: digest=%q url=%q", view.ContentDigest, view.ESMBaseURL)
	}
	initialDigest := view.ContentDigest

	// Cannot uninstall or overwrite official plugin.
	if err := mgr.Uninstall(ctx, "three-demo"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict uninstalling official plugin, got %v", err)
	}
	overwriteZip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"three-demo","name":"Overwrite","version":"2.0.0"}`,
	})
	if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(overwriteZip), int64(len(overwriteZip))); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict overwriting official plugin, got %v", err)
	}

	// 2. Unauthenticated request -> 401.
	unauthRec := httptest.NewRecorder()
	mgr.ServeHTTP(unauthRec, httptest.NewRequest(http.MethodGet, "/api/v1/ext/three-demo/_esm/ui/entry.js", nil))
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", unauthRec.Code)
	}

	// 3. Disabled plugin with valid cookie auth -> 403.
	disabledReq := httptest.NewRequest(http.MethodGet, "/api/v1/ext/three-demo/_esm/ui/entry.js", nil)
	disabledReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: testBrowserToken})
	disabledRec := httptest.NewRecorder()
	mgr.ServeHTTP(disabledRec, disabledReq)
	if disabledRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disabled official plugin, got %d %s", disabledRec.Code, disabledRec.Body.String())
	}

	// 4. Enable official ESM plugin IN OFFICE MODE -> succeeds with zero permission prompt!
	enabledView, pending, err := mgr.SetEnabled(ctx, "three-demo", true, nil)
	if err != nil || len(pending) != 0 || !enabledView.Enabled {
		t.Fatalf("enable official ESM in office mode: view=%+v pending=%v err=%v", enabledView, pending, err)
	}

	// 5. Cookie-authenticated _esm requests return explicit MIME + immutable cache when ?v= matches.
	mimeCases := []struct {
		file string
		mime string
	}{
		{"ui/entry.js", "text/javascript; charset=utf-8"},
		{"ui/entry.css", "text/css; charset=utf-8"},
		{"ui/meta.json", "application/json; charset=utf-8"},
		{"ui/kernel.wasm", "application/wasm"},
		{"ui/icon.svg", "image/svg+xml"},
		{"ui/tex.png", "image/png"},
	}
	for _, tc := range mimeCases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ext/three-demo/_esm/"+tc.file+"?v="+initialDigest, nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: testBrowserToken})
		rec := httptest.NewRecorder()
		mgr.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", tc.file, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != tc.mime {
			t.Fatalf("GET %s Content-Type=%q, want %q", tc.file, got, tc.mime)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("GET %s missing nosniff", tc.file)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
			t.Fatalf("GET %s with matching digest Cache-Control=%q", tc.file, got)
		}
	}

	// Missing/mismatched ?v= -> Cache-Control: no-store.
	noVReq := httptest.NewRequest(http.MethodGet, "/api/v1/ext/three-demo/_esm/ui/entry.js?v=stale", nil)
	noVReq.Header.Set("Authorization", "Bearer "+testBrowserToken)
	noVRec := httptest.NewRecorder()
	mgr.ServeHTTP(noVRec, noVReq)
	if noVRec.Code != http.StatusOK || noVRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected no-store for mismatched ?v=, status=%d cc=%q", noVRec.Code, noVRec.Header().Get("Cache-Control"))
	}

	// 6. Security: path traversal, hidden files, symlink escape, and in-dir symlink to hidden file all blocked!
	secCases := []struct {
		name   string
		path   string
		status int
	}{
		{"traversal", "/api/v1/ext/three-demo/_esm/ui/../../outside.js", http.StatusBadRequest},
		{"hidden", "/api/v1/ext/three-demo/_esm/.env", http.StatusForbidden},
		{"symlink_escape", "/api/v1/ext/three-demo/_esm/ui/escape.js", http.StatusForbidden},
		{"symlink_hidden_esm", "/api/v1/ext/three-demo/_esm/ui/hidden-alias.js", http.StatusForbidden},
		{"symlink_hidden_ui", "/api/v1/ext/three-demo/_ui/ui/hidden-alias.js", http.StatusForbidden},
	}
	for _, tc := range secCases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+testBrowserToken)
		rec := httptest.NewRecorder()
		mgr.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("security case %s (%s): got %d want %d body=%s", tc.name, tc.path, rec.Code, tc.status, rec.Body.String())
		}
	}

	// 7. Modifying ui/entry.js + ReloadExternal updates ContentDigest and emits reloaded event!
	_ = os.WriteFile(filepath.Join(pluginDir, "ui", "entry.js"), []byte(`export function activate(){ return "v2"; }`), 0o644)
	if err := mgr.ReloadExternal(ctx); err != nil {
		t.Fatalf("ReloadExternal: %v", err)
	}
	reloadedView, err := mgr.GetPlugin(ctx, "three-demo")
	if err != nil {
		t.Fatalf("GetPlugin after reload: %v", err)
	}
	if reloadedView.ContentDigest == initialDigest || len(reloadedView.ContentDigest) != 16 {
		t.Fatalf("expected changed 16-char ContentDigest after file edit, old=%q new=%q", initialDigest, reloadedView.ContentDigest)
	}

	mu.Lock()
	defer mu.Unlock()
	foundEnabled := false
	foundReloaded := false
	for _, ev := range events {
		if ev.Action == "enabled" && ev.PluginID == "three-demo" {
			foundEnabled = true
		}
		if ev.Action == "reloaded" && ev.PluginID == "three-demo" && ev.ContentDigest == reloadedView.ContentDigest {
			foundReloaded = true
		}
	}
	if !foundEnabled || !foundReloaded {
		t.Fatalf("expected enabled + reloaded OnChange events, got %+v", events)
	}
}

func TestExternalESMDualTrackTrustPolicyAndEd25519TamperDetection(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "office"); err != nil {
		t.Fatalf("set office profile: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pluginsDir := filepath.Join(t.TempDir(), "plugins", "external")
	mgr := NewManager(Options{
		Store:             st,
		Authorizer:        cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
		PluginsDir:        pluginsDir,
		TrustedPublicKeys: map[string]ed25519.PublicKey{"trusted-k1": pub},
	})
	defer mgr.Close()

	makeESMSrc := func(id string) string {
		src := t.TempDir()
		_ = os.MkdirAll(filepath.Join(src, "ui"), 0o755)
		_ = os.WriteFile(filepath.Join(src, "ui", "entry.js"), []byte(`export function activate() {}`), 0o644)
		m := Manifest{
			SchemaVersion: 1,
			ID:            id,
			Name:          "ESM " + id,
			Version:       "1.0.0",
			Backend:       BackendSpec{Type: BackendTypeNone},
			Frontend: &FrontendSpec{
				Mode:  FrontendModeESM,
				Entry: "ui/entry.js",
			},
		}
		raw, _ := json.MarshalIndent(m, "", "  ")
		_ = os.WriteFile(filepath.Join(src, "plugin.json"), raw, 0o644)
		return src
	}

	// 1. Unsigned external ESM plugin in office mode:
	unsignedSrc := makeESMSrc("unsigned-esm")
	unsignedView, err := mgr.InstallFromDir(ctx, unsignedSrc)
	if err != nil {
		t.Fatalf("install unsigned-esm: %v", err)
	}
	if unsignedView.SignatureVerified || unsignedView.Enabled {
		t.Fatalf("expected unsigned disabled view, got %+v", unsignedView)
	}
	if _, _, err := mgr.SetEnabled(ctx, "unsigned-esm", true, []string{PermUISlots}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied enabling unsigned external ESM in office mode, got %v", err)
	}
	// Policy rejection MUST NOT persist grant_permissions into ext:permissions!
	if persisted, err := mgr.storage.GetGrantedPermissions(ctx, "unsigned-esm"); err != nil || len(persisted) != 0 {
		t.Fatalf("expected zero persisted permissions after 403 policy rejection, got %v (err=%v)", persisted, err)
	}
	// Even if someone forces plugins:enabled=true in SQLite, _esm MUST still return 403 in office mode!
	_ = mgr.storage.SetEnabled(ctx, "unsigned-esm", true)
	_ = mgr.storage.SetGrantedPermissions(ctx, "unsigned-esm", []string{PermUISlots})
	rec := httptest.NewRecorder()
	mgr.ServeHTTP(rec, authRequest(t, http.MethodGet, "/api/v1/ext/unsigned-esm/_esm/ui/entry.js", nil, ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 serving unsigned external ESM in office mode, got %d %s", rec.Code, rec.Body.String())
	}

	// 2. Ed25519-signed external ESM plugin IS allowed in office mode!
	signedSrc := makeESMSrc("signed-esm")
	sig, err := SignPluginDir(signedSrc, "trusted-k1", priv)
	if err != nil {
		t.Fatalf("SignPluginDir: %v", err)
	}
	sm, _ := LoadManifestFile(filepath.Join(signedSrc, "plugin.json"))
	sm.Signature = sig
	smBytes, _ := json.MarshalIndent(sm, "", "  ")
	_ = os.WriteFile(filepath.Join(signedSrc, "plugin.json"), smBytes, 0o644)

	signedView, err := mgr.InstallFromDir(ctx, signedSrc)
	if err != nil {
		t.Fatalf("install signed-esm: %v", err)
	}
	if !signedView.SignatureVerified {
		t.Fatalf("expected SignatureVerified=true for Ed25519-signed plugin")
	}
	if _, _, err := mgr.SetEnabled(ctx, "signed-esm", true, nil); err != nil {
		t.Fatalf("enable signed-esm in office mode: %v", err)
	}
	signedRec := httptest.NewRecorder()
	mgr.ServeHTTP(signedRec, authRequest(t, http.MethodGet, "/api/v1/ext/signed-esm/_esm/ui/entry.js", nil, ""))
	if signedRec.Code != http.StatusOK || signedRec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("expected 200 JS for signed external ESM in office mode, got %d ct=%q body=%s", signedRec.Code, signedRec.Header().Get("Content-Type"), signedRec.Body.String())
	}

	// Tampering with signed plugin file invalidates signature and immediately yields 403!
	_ = os.WriteFile(filepath.Join(pluginsDir, "signed-esm", "ui", "entry.js"), []byte(`alert("tampered")`), 0o644)
	tamperRec := httptest.NewRecorder()
	mgr.ServeHTTP(tamperRec, authRequest(t, http.MethodGet, "/api/v1/ext/signed-esm/_esm/ui/entry.js", nil, ""))
	if tamperRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 after tampering with signed external ESM, got %d %s", tamperRec.Code, tamperRec.Body.String())
	}

	// 3. Developer mode: unsigned external ESM requires explicit ui:slots grant, then serves 200;
	// explicit security policy allow_unsigned_esm_plugins=false still blocks it!
	if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "developer"); err != nil {
		t.Fatal(err)
	}
	_ = mgr.storage.SetEnabled(ctx, "unsigned-esm", false)
	_ = mgr.storage.SetGrantedPermissions(ctx, "unsigned-esm", nil)

	_, pending, err := mgr.SetEnabled(ctx, "unsigned-esm", true, nil)
	if !errors.Is(err, ErrPermissionGrantRequired) || len(pending) != 1 || pending[0] != PermUISlots {
		t.Fatalf("expected ErrPermissionGrantRequired([ui:slots]) in developer mode, got pending=%v err=%v", pending, err)
	}
	if _, _, err := mgr.SetEnabled(ctx, "unsigned-esm", true, []string{PermUISlots}); err != nil {
		t.Fatalf("enable unsigned-esm in developer mode with ui:slots grant: %v", err)
	}
	devRec := httptest.NewRecorder()
	mgr.ServeHTTP(devRec, authRequest(t, http.MethodGet, "/api/v1/ext/unsigned-esm/_esm/ui/entry.js", nil, ""))
	if devRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for granted unsigned ESM in developer mode, got %d %s", devRec.Code, devRec.Body.String())
	}

	// Security policy override blocks even in developer mode.
	if err := st.SavePluginConfigValue(ctx, "plugin-security", "allow_unsigned_esm_plugins", false); err != nil {
		t.Fatal(err)
	}
	blockedRec := httptest.NewRecorder()
	mgr.ServeHTTP(blockedRec, authRequest(t, http.MethodGet, "/api/v1/ext/unsigned-esm/_esm/ui/entry.js", nil, ""))
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when allow_unsigned_esm_plugins=false, got %d", blockedRec.Code)
	}
}

func TestBundledOfficialThreeViewerManifestAndESMServing(t *testing.T) {
	officialDistDir := filepath.Join("..", "..", "..", "plugins", "official-dist")
	manifestPath := filepath.Join(officialDistDir, "three-viewer", "plugin.json")
	if _, err := os.Stat(manifestPath); errors.Is(err, os.ErrNotExist) {
		t.Skipf("official-dist/three-viewer not present at %s", manifestPath)
	}

	m, err := LoadManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifestFile(three-viewer): %v", err)
	}
	if m.ID != "three-viewer" || !IsFrontendESM(m) || m.Frontend.Entry != "ui/entry.js" {
		t.Fatalf("unexpected three-viewer manifest: %+v", m)
	}
	if EffectiveDefaultEnabled(SourceOfficial, m) {
		t.Fatalf("expected three-viewer default_enabled=false")
	}

	st := newTestStore(t)
	ctx := context.Background()
	mgr := NewManager(Options{
		Store:              st,
		Authorizer:         cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
		OfficialPluginsDir: officialDistDir,
	})
	defer mgr.Close()

	view, err := mgr.GetPlugin(ctx, "three-viewer")
	if err != nil {
		t.Fatalf("GetPlugin(three-viewer): %v", err)
	}
	if view.Source != SourceOfficial || view.Enabled || view.ESMBaseURL != "/api/v1/ext/three-viewer/_esm" || view.ContentDigest == "" {
		t.Fatalf("unexpected three-viewer view: %+v", view)
	}

	if _, _, err := mgr.SetEnabled(ctx, "three-viewer", true, nil); err != nil {
		t.Fatalf("enable three-viewer: %v", err)
	}

	entryRec := httptest.NewRecorder()
	mgr.ServeHTTP(entryRec, authRequest(t, http.MethodGet, "/api/v1/ext/three-viewer/_esm/ui/entry.js?v="+view.ContentDigest, nil, ""))
	if entryRec.Code != http.StatusOK || entryRec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("GET three-viewer ui/entry.js: status=%d ct=%q", entryRec.Code, entryRec.Header().Get("Content-Type"))
	}
	cssRec := httptest.NewRecorder()
	mgr.ServeHTTP(cssRec, authRequest(t, http.MethodGet, "/api/v1/ext/three-viewer/_esm/ui/entry.css?v="+view.ContentDigest, nil, ""))
	if cssRec.Code != http.StatusOK || cssRec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("GET three-viewer ui/entry.css: status=%d ct=%q", cssRec.Code, cssRec.Header().Get("Content-Type"))
	}
}

type stubSkillBuiltinPlugin struct {
	stubBuiltinPlugin
	skills []PluginSkillEntry
}

func (p *stubSkillBuiltinPlugin) Skills() []PluginSkillEntry {
	return p.skills
}

func TestHybridBuiltinOfficialESMOrderIndependentServingReloadAndIsolation(t *testing.T) {
	newHybridBuiltin := func() *stubSkillBuiltinPlugin {
		return &stubSkillBuiltinPlugin{
			stubBuiltinPlugin: stubBuiltinPlugin{
				manifest: Manifest{
					SchemaVersion: 1,
					ID:            "gitlab-ci-viewer",
					Name:          "GitLab CI 流水线",
					Version:       "1.0.0",
					Category:      "development",
					Description:   "Builtin GitLab CI backend",
					Author:        "Astrorder",
					Permissions: []string{
						PermNetworkOutbound,
						PermWorkspaceGitRead,
						PermStorageSecrets,
						PermAgentCapabilities,
						PermUISlots,
						PermSignalsProvide,
					},
					Backend:  BackendSpec{Type: BackendTypeBuiltin},
					Settings: SettingsSpec{Mode: SettingsModeCustomUI},
					Contributes: ContributesSpec{
						UISlots: []UISlotContribution{
							{Slot: "chat.composer.status_bar", ID: "pipeline-pill", Renderer: "declarative_pill", PollEndpoint: "/status-pill"},
						},
						SidecarPanels: []SidecarPanelContribution{
							{ID: "ci-panel", Title: "GitLab CI", MediaType: "application/x-gitlab-ci"},
						},
						AgentCapabilities: []AgentCapabilityContribution{
							{ID: "pipelines", CLIAlias: []string{"gitlab", "pipelines"}, Summary: "List pipelines"},
						},
						SignalProviders: []SignalProviderContribution{
							{Kind: "workspace.failure_signals"},
						},
					},
				},
			},
			skills: []PluginSkillEntry{
				{
					PluginID:    "gitlab-ci-viewer",
					Name:        "astrorder-gitlab-ci",
					DisplayName: "GitLab CI",
					Content:     "# GitLab CI Skill",
				},
			},
		}
	}

	writeOfficialHybrid := func(t *testing.T, root, officialDir, externalDir string) (officialPluginDir string) {
		t.Helper()
		officialPluginDir = filepath.Join(officialDir, "gitlab-ci-viewer")
		if err := os.MkdirAll(filepath.Join(officialPluginDir, "ui"), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(officialPluginDir, "plugin.json"), []byte(`{
			"schema_version": 1,
			"id": "gitlab-ci-viewer",
			"name": "GitLab CI/CD 流水线",
			"version": "1.0.0",
			"category": "development",
			"default_enabled": true,
			"backend": {"type": "builtin"},
			"frontend": {
				"mode": "esm",
				"entry": "ui/entry.js",
				"styles": ["ui/entry.css"],
				"sdk_version": "^1.0"
			},
			"contributes": {
				"viewers": [
					{
						"id": "gitlab-ci-viewer",
						"title": "GitLab CI",
						"mime_types": ["application/x-gitlab-ci"],
						"hide_in_office_mode": true
					}
				],
				"pages": [
					{
						"id": "pipelines-page",
						"title": "CI Pipelines",
						"path": "/gitlab-ci",
						"hide_in_office_mode": true
					}
				]
			}
		}`), 0o644)
		_ = os.WriteFile(filepath.Join(officialPluginDir, "ui", "entry.js"), []byte(`export function activate(){ return "official-v1"; }`), 0o644)
		_ = os.WriteFile(filepath.Join(officialPluginDir, "ui", "entry.css"), []byte(`.gitlab-ci{color:#e24329}`), 0o644)
		_ = os.WriteFile(filepath.Join(officialPluginDir, ".env"), []byte("SECRET=top"), 0o644)

		outside := filepath.Join(root, "outside.js")
		_ = os.WriteFile(outside, []byte(`console.log("escaped")`), 0o644)
		_ = os.Symlink(outside, filepath.Join(officialPluginDir, "ui", "escape.js"))
		_ = os.Symlink(filepath.Join("..", ".env"), filepath.Join(officialPluginDir, "ui", "hidden-alias.js"))

		// Seed colliding external directory (same ID) AND external directory spoofing backend.type="builtin".
		evilCollideDir := filepath.Join(externalDir, "gitlab-ci-viewer")
		_ = os.MkdirAll(filepath.Join(evilCollideDir, "ui"), 0o755)
		_ = os.WriteFile(filepath.Join(evilCollideDir, "plugin.json"), []byte(`{
			"schema_version": 1,
			"id": "gitlab-ci-viewer",
			"name": "Evil Hijack",
			"version": "9.9.9",
			"backend": {"type": "none"},
			"frontend": {"mode": "esm", "entry": "ui/entry.js"}
		}`), 0o644)
		_ = os.WriteFile(filepath.Join(evilCollideDir, "ui", "entry.js"), []byte(`export function activate(){ return "EVIL-HIJACK"; }`), 0o644)

		evilBuiltinDir := filepath.Join(externalDir, "spoof-builtin-disk")
		_ = os.MkdirAll(filepath.Join(evilBuiltinDir, "ui"), 0o755)
		_ = os.WriteFile(filepath.Join(evilBuiltinDir, "plugin.json"), []byte(`{
			"schema_version": 1,
			"id": "spoof-builtin-disk",
			"name": "Spoof Builtin",
			"version": "1.0.0",
			"backend": {"type": "builtin"},
			"frontend": {"mode": "esm", "entry": "ui/entry.js"}
		}`), 0o644)
		_ = os.WriteFile(filepath.Join(evilBuiltinDir, "ui", "entry.js"), []byte(`export function activate(){}`), 0o644)
		return officialPluginDir
	}

	for _, order := range []string{"scan_then_register", "register_then_reload"} {
		t.Run(order, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			// Verify hybrid official ESM works in office mode as well as developer mode.
			if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "office"); err != nil {
				t.Fatalf("set office profile: %v", err)
			}

			root := t.TempDir()
			officialDir := filepath.Join(root, "official")
			externalDir := filepath.Join(root, "external")

			var officialPluginDir string
			if order == "scan_then_register" {
				officialPluginDir = writeOfficialHybrid(t, root, officialDir, externalDir)
			}

			var evMu sync.Mutex
			var events []ExtensionChangeEvent
			mgr := NewManager(Options{
				Store:              st,
				Authorizer:         cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
				PluginsDir:         externalDir,
				OfficialPluginsDir: officialDir,
				OnChange: func(ev ExtensionChangeEvent) {
					evMu.Lock()
					events = append(events, ev)
					evMu.Unlock()
				},
			})
			defer mgr.Close()

			if err := mgr.RegisterBuiltin(newHybridBuiltin()); err != nil {
				t.Fatalf("RegisterBuiltin: %v", err)
			}

			if order == "register_then_reload" {
				// Before official dir exists, pure builtin has no ESM frontend.
				preView, err := mgr.GetPlugin(ctx, "gitlab-ci-viewer")
				if err != nil {
					t.Fatalf("GetPlugin before official dir: %v", err)
				}
				if preView.Frontend != nil || preView.ESMBaseURL != "" {
					t.Fatalf("expected no ESM frontend before official dir exists, got %+v", preView)
				}
				officialPluginDir = writeOfficialHybrid(t, root, officialDir, externalDir)
				if err := mgr.Reload(ctx); err != nil {
					t.Fatalf("Reload after writing official dir: %v", err)
				}
			}

			// 1. ListPlugins returns a single merged builtin entry; spoof-builtin-disk is rejected.
			views, err := mgr.ListPlugins(ctx)
			if err != nil {
				t.Fatalf("ListPlugins: %v", err)
			}
			gitlabCount := 0
			for _, v := range views {
				if v.ID == "gitlab-ci-viewer" {
					gitlabCount++
				}
				if v.ID == "spoof-builtin-disk" {
					t.Fatalf("external plugin declaring backend.type=builtin MUST be rejected during scan")
				}
			}
			if gitlabCount != 1 {
				t.Fatalf("expected exactly 1 merged gitlab-ci-viewer entry in ListPlugins, got %d (%+v)", gitlabCount, views)
			}

			// 2. GetPlugin merges builtin backend metadata/capabilities with official ESM frontend.
			view, err := mgr.GetPlugin(ctx, "gitlab-ci-viewer")
			if err != nil {
				t.Fatalf("GetPlugin: %v", err)
			}
			if view.Source != SourceBuiltin || view.BackendType != BackendTypeBuiltin || view.RuntimeState != RuntimeStateBuiltin {
				t.Fatalf("unexpected builtin identity: %+v", view)
			}
			if !view.Enabled || !view.DefaultEnabled || !view.PermissionsGranted || !view.SignatureVerified {
				t.Fatalf("unexpected builtin enabled/permission state: %+v", view)
			}
			if view.Frontend == nil || view.Frontend.Mode != FrontendModeESM || view.Frontend.Entry != "ui/entry.js" || len(view.Frontend.Styles) != 1 || view.Frontend.Styles[0] != "ui/entry.css" || view.Frontend.SDKVersion != "^1.0" {
				t.Fatalf("unexpected merged frontend spec: %+v", view.Frontend)
			}
			if len(view.ContentDigest) != 16 || view.ESMBaseURL != "/api/v1/ext/gitlab-ci-viewer/_esm" || view.APIBaseURL != "/api/v1/ext/gitlab-ci-viewer" {
				t.Fatalf("unexpected digest/urls: digest=%q esm=%q api=%q", view.ContentDigest, view.ESMBaseURL, view.APIBaseURL)
			}
			if len(view.Contributes.AgentCapabilities) != 1 || view.Contributes.AgentCapabilities[0].ID != "pipelines" {
				t.Fatalf("expected backend agent_capabilities preserved, got %+v", view.Contributes.AgentCapabilities)
			}
			if len(view.Contributes.SignalProviders) != 1 || view.Contributes.SignalProviders[0].Kind != "workspace.failure_signals" {
				t.Fatalf("expected backend signal_providers preserved, got %+v", view.Contributes.SignalProviders)
			}
			if len(view.Contributes.UISlots) != 1 || len(view.Contributes.SidecarPanels) != 1 {
				t.Fatalf("expected backend ui_slots & sidecar_panels preserved, got slots=%+v panels=%+v", view.Contributes.UISlots, view.Contributes.SidecarPanels)
			}
			if len(view.Contributes.Viewers) != 1 || view.Contributes.Viewers[0].ID != "gitlab-ci-viewer" || !view.Contributes.Viewers[0].HideInOfficeMode {
				t.Fatalf("expected official frontend viewers merged, got %+v", view.Contributes.Viewers)
			}
			if len(view.Contributes.Pages) != 1 || view.Contributes.Pages[0].ID != "pipelines-page" {
				t.Fatalf("expected official frontend pages merged, got %+v", view.Contributes.Pages)
			}
			initialDigest := view.ContentDigest

			// 3. Backend APIs, capabilities, signals, and skills remain served by Go BuiltinPlugin.
			apiRec := httptest.NewRecorder()
			mgr.ServeHTTP(apiRec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/status-pill", nil, ""))
			if apiRec.Code != http.StatusOK || !strings.Contains(apiRec.Body.String(), `"builtin":true`) {
				t.Fatalf("builtin ServeExtension failed: status=%d body=%s", apiRec.Code, apiRec.Body.String())
			}
			if res, err := mgr.InvokeAgentCapability(ctx, "gitlab-ci-viewer", "pipelines", map[string]any{"ref": "main"}); err != nil {
				t.Fatalf("InvokeAgentCapability failed: %v", err)
			} else if m, ok := res.(map[string]any); !ok || m["builtin_cap"] != "pipelines" {
				t.Fatalf("unexpected InvokeAgentCapability result: %#v", res)
			}
			if sigs := mgr.CollectSignals(ctx, "workspace.failure_signals", SignalQuery{Workspace: "/repo"}); len(sigs) != 1 || sigs[0].SourcePlugin != "gitlab-ci-viewer" {
				t.Fatalf("unexpected CollectSignals result: %+v", sigs)
			}
			if skills := mgr.ListEnabledSkills(ctx); len(skills) != 1 || skills[0].Name != "astrorder-gitlab-ci" {
				t.Fatalf("unexpected ListEnabledSkills result: %+v", skills)
			}

			// 4. ESM static assets served with auth, explicit MIME, cache headers, and security containment.
			unauthRec := httptest.NewRecorder()
			mgr.ServeHTTP(unauthRec, httptest.NewRequest(http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.js", nil))
			if unauthRec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 without auth, got %d", unauthRec.Code)
			}

			jsReq := httptest.NewRequest(http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.js?v="+initialDigest, nil)
			jsReq.AddCookie(&http.Cookie{Name: auth.CookieName, Value: testBrowserToken})
			jsRec := httptest.NewRecorder()
			mgr.ServeHTTP(jsRec, jsReq)
			if jsRec.Code != http.StatusOK {
				t.Fatalf("GET hybrid entry.js status=%d body=%s", jsRec.Code, jsRec.Body.String())
			}
			if !strings.Contains(jsRec.Body.String(), "official-v1") || strings.Contains(jsRec.Body.String(), "EVIL-HIJACK") {
				t.Fatalf("expected official ESM bundle, got: %s", jsRec.Body.String())
			}
			if ct := jsRec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
				t.Fatalf("hybrid entry.js Content-Type=%q", ct)
			}
			if cc := jsRec.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
				t.Fatalf("hybrid entry.js matching ?v= Cache-Control=%q", cc)
			}
			if jsRec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("missing nosniff header")
			}

			cssRec := httptest.NewRecorder()
			mgr.ServeHTTP(cssRec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.css", nil, ""))
			if cssRec.Code != http.StatusOK || cssRec.Header().Get("Content-Type") != "text/css; charset=utf-8" || cssRec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("hybrid entry.css status=%d ct=%q cc=%q", cssRec.Code, cssRec.Header().Get("Content-Type"), cssRec.Header().Get("Cache-Control"))
			}

			for _, sec := range []struct {
				name   string
				path   string
				status int
			}{
				{"traversal", "/api/v1/ext/gitlab-ci-viewer/_esm/ui/../../outside.js", http.StatusBadRequest},
				{"hidden", "/api/v1/ext/gitlab-ci-viewer/_esm/.env", http.StatusForbidden},
				{"symlink_escape", "/api/v1/ext/gitlab-ci-viewer/_esm/ui/escape.js", http.StatusForbidden},
				{"symlink_hidden", "/api/v1/ext/gitlab-ci-viewer/_esm/ui/hidden-alias.js", http.StatusForbidden},
			} {
				rec := httptest.NewRecorder()
				mgr.ServeHTTP(rec, authRequest(t, http.MethodGet, sec.path, nil, ""))
				if rec.Code != sec.status {
					t.Fatalf("security %s: got %d want %d body=%s", sec.name, rec.Code, sec.status, rec.Body.String())
				}
			}

			// 5. Disabling builtin immediately returns 403 on BOTH /_esm/* and backend endpoints; re-enabling restores 200.
			if _, _, err := mgr.SetEnabled(ctx, "gitlab-ci-viewer", false, nil); err != nil {
				t.Fatalf("disable gitlab-ci-viewer: %v", err)
			}
			disESMRec := httptest.NewRecorder()
			mgr.ServeHTTP(disESMRec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.js", nil, ""))
			if disESMRec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 on /_esm/* when hybrid builtin disabled, got %d %s", disESMRec.Code, disESMRec.Body.String())
			}
			disAPIRec := httptest.NewRecorder()
			mgr.ServeHTTP(disAPIRec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/status-pill", nil, ""))
			if disAPIRec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 on backend endpoint when hybrid builtin disabled, got %d", disAPIRec.Code)
			}

			if _, _, err := mgr.SetEnabled(ctx, "gitlab-ci-viewer", true, nil); err != nil {
				t.Fatalf("re-enable gitlab-ci-viewer: %v", err)
			}
			reESMRec := httptest.NewRecorder()
			mgr.ServeHTTP(reESMRec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.js", nil, ""))
			if reESMRec.Code != http.StatusOK {
				t.Fatalf("expected 200 on /_esm/* after re-enabling hybrid builtin, got %d", reESMRec.Code)
			}

			// 6. Hot reload: editing official ui/entry.js + Reload() refreshes ContentDigest & emits extension.changed!
			_ = os.WriteFile(filepath.Join(officialPluginDir, "ui", "entry.js"), []byte(`export function activate(){ return "official-v2"; }`), 0o644)
			if err := mgr.Reload(ctx); err != nil {
				t.Fatalf("Reload after edit: %v", err)
			}
			reloadedView, err := mgr.GetPlugin(ctx, "gitlab-ci-viewer")
			if err != nil {
				t.Fatalf("GetPlugin after reload: %v", err)
			}
			if reloadedView.ContentDigest == initialDigest || len(reloadedView.ContentDigest) != 16 {
				t.Fatalf("expected refreshed ContentDigest after Reload, old=%q new=%q", initialDigest, reloadedView.ContentDigest)
			}
			v2Rec := httptest.NewRecorder()
			mgr.ServeHTTP(v2Rec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.js?v="+reloadedView.ContentDigest, nil, ""))
			if v2Rec.Code != http.StatusOK || !strings.Contains(v2Rec.Body.String(), "official-v2") || v2Rec.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
				t.Fatalf("expected v2 content with immutable cache, status=%d cc=%q body=%s", v2Rec.Code, v2Rec.Header().Get("Cache-Control"), v2Rec.Body.String())
			}

			evMu.Lock()
			foundReloaded := false
			for _, ev := range events {
				if ev.Action == "reloaded" && ev.PluginID == "gitlab-ci-viewer" && ev.Source == SourceBuiltin && ev.ContentDigest == reloadedView.ContentDigest {
					foundReloaded = true
				}
			}
			evMu.Unlock()
			if !foundReloaded {
				t.Fatalf("expected reloaded OnChange event for hybrid builtin with new digest %q, got %+v", reloadedView.ContentDigest, events)
			}

			// 7. External installers CANNOT overwrite builtin ID or declare backend.type="builtin".
			collideZip := makeZipBytes(t, map[string]string{
				"plugin.json": `{"schema_version":1,"id":"gitlab-ci-viewer","name":"Overwrite","version":"2.0.0","backend":{"type":"none"}}`,
			})
			if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(collideZip), int64(len(collideZip))); !errors.Is(err, ErrConflict) {
				t.Fatalf("expected ErrConflict overwriting hybrid builtin via zip, got %v", err)
			}
			spoofZip := makeZipBytes(t, map[string]string{
				"plugin.json": `{"schema_version":1,"id":"spoof-builtin-zip","name":"Spoof","version":"1.0.0","backend":{"type":"builtin"}}`,
			})
			if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(spoofZip), int64(len(spoofZip))); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected ErrInvalid installing external plugin with backend.type=builtin via zip, got %v", err)
			}
			spoofDir := t.TempDir()
			_ = os.WriteFile(filepath.Join(spoofDir, "plugin.json"), []byte(`{"schema_version":1,"id":"spoof-builtin-dir","name":"Spoof","version":"1.0.0","backend":{"type":"builtin"}}`), 0o644)
			if _, err := mgr.InstallFromDir(ctx, spoofDir); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected ErrInvalid installing external plugin with backend.type=builtin via dir, got %v", err)
			}
			if err := mgr.Uninstall(ctx, "gitlab-ci-viewer"); !errors.Is(err, ErrConflict) {
				t.Fatalf("expected ErrConflict uninstalling hybrid builtin, got %v", err)
			}
		})
	}
}

func TestExternalDirectoryCannotHijackBuiltinESMFrontend(t *testing.T) {
	ctx := context.Background()
	for _, order := range []string{"scan_then_register", "register_then_reload"} {
		t.Run(order, func(t *testing.T) {
			st := newTestStore(t)
			root := t.TempDir()
			officialDir := filepath.Join(root, "official")
			externalDir := filepath.Join(root, "external")
			_ = os.MkdirAll(officialDir, 0o755)

			evilDir := filepath.Join(externalDir, "gitlab-ci-viewer")
			_ = os.MkdirAll(filepath.Join(evilDir, "ui"), 0o755)
			_ = os.WriteFile(filepath.Join(evilDir, "plugin.json"), []byte(`{
				"schema_version": 1,
				"id": "gitlab-ci-viewer",
				"name": "Evil Hijack",
				"version": "1.0.0",
				"backend": {"type": "none"},
				"frontend": {"mode": "esm", "entry": "ui/entry.js"}
			}`), 0o644)
			_ = os.WriteFile(filepath.Join(evilDir, "ui", "entry.js"), []byte(`export function activate(){ alert("pwned"); }`), 0o644)

			mgr := NewManager(Options{
				Store:              st,
				Authorizer:         cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
				PluginsDir:         externalDir,
				OfficialPluginsDir: officialDir,
			})
			defer mgr.Close()

			if err := mgr.RegisterBuiltin(&stubBuiltinPlugin{manifest: Manifest{
				SchemaVersion: 1,
				ID:            "gitlab-ci-viewer",
				Name:          "GitLab CI",
				Version:       "1.0.0",
				Backend:       BackendSpec{Type: BackendTypeBuiltin},
			}}); err != nil {
				t.Fatalf("RegisterBuiltin: %v", err)
			}
			if order == "register_then_reload" {
				if err := mgr.Reload(ctx); err != nil {
					t.Fatalf("Reload: %v", err)
				}
			}

			view, err := mgr.GetPlugin(ctx, "gitlab-ci-viewer")
			if err != nil {
				t.Fatalf("GetPlugin: %v", err)
			}
			if view.Frontend != nil || view.ESMBaseURL != "" || view.ContentDigest != "" {
				t.Fatalf("external plugin MUST NEVER bind as builtin ESM frontend, got %+v", view)
			}
			rec := httptest.NewRecorder()
			mgr.ServeHTTP(rec, authRequest(t, http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/_esm/ui/entry.js", nil, ""))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404 when only external dir matches builtin id, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestValidateManifestForSourceRules(t *testing.T) {
	m, err := ParseManifest([]byte(`{
		"schema_version": 1,
		"id": "hybrid-demo",
		"name": "Hybrid Demo",
		"version": "1.0.0",
		"backend": {"type": "builtin"},
		"frontend": {"mode": "esm", "entry": "ui/entry.js", "styles": ["ui/entry.css"], "sdk_version": "^1.0"}
	}`))
	if err != nil {
		t.Fatalf("ParseManifest builtin+esm: %v", err)
	}
	if err := ValidateManifestForSource(SourceOfficial, m); err != nil {
		t.Fatalf("expected SourceOfficial to allow backend.type=builtin, got %v", err)
	}
	if err := ValidateManifestForSource(SourceBuiltin, m); err != nil {
		t.Fatalf("expected SourceBuiltin to allow backend.type=builtin, got %v", err)
	}
	if err := ValidateManifestForSource(SourceExternal, m); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected SourceExternal to reject backend.type=builtin with ErrInvalid, got %v", err)
	}
}

func TestBundledOfficialPluginsManifestAndHybridServing(t *testing.T) {
	officialDistDir := filepath.Join("..", "..", "..", "plugins", "official-dist")
	if _, err := os.Stat(officialDistDir); errors.Is(err, os.ErrNotExist) {
		t.Skipf("official-dist not present at %s", officialDistDir)
	}

	expected := []struct {
		id               string
		backendType      string
		defaultEnabled   bool
		isHybrid         bool
		hideInOfficeMode bool
	}{
		{"three-viewer", BackendTypeNone, false, false, false},
		{"excalidraw-viewer", BackendTypeNone, true, false, false},
		{"drawio-viewer", BackendTypeNone, true, false, false},
		{"gitlab-ci-viewer", BackendTypeBuiltin, true, true, true},
		{"dbx-viewer", BackendTypeNone, true, false, true},
	}

	presentIDs := map[string]bool{}
	for _, spec := range expected {
		manifestPath := filepath.Join(officialDistDir, spec.id, "plugin.json")
		if _, err := os.Stat(manifestPath); errors.Is(err, os.ErrNotExist) {
			continue
		}
		presentIDs[spec.id] = true
		m, err := LoadManifestFile(manifestPath)
		if err != nil {
			t.Fatalf("LoadManifestFile(%s): %v", spec.id, err)
		}
		if err := ValidateManifestForSource(SourceOfficial, m); err != nil {
			t.Fatalf("ValidateManifestForSource(official, %s): %v", spec.id, err)
		}
		if m.ID != spec.id || m.Backend.Type != spec.backendType || !IsFrontendESM(m) {
			t.Fatalf("unexpected manifest for %s: %+v", spec.id, m)
		}
		if !spec.isHybrid && EffectiveDefaultEnabled(SourceOfficial, m) != spec.defaultEnabled {
			t.Fatalf("%s default_enabled=%v want %v", spec.id, EffectiveDefaultEnabled(SourceOfficial, m), spec.defaultEnabled)
		}
		if len(m.Contributes.Viewers) == 0 || m.Contributes.Viewers[0].HideInOfficeMode != spec.hideInOfficeMode {
			t.Fatalf("%s viewer hide_in_office_mode mismatch: %+v", spec.id, m.Contributes.Viewers)
		}
	}
	if len(presentIDs) == 0 {
		t.Skip("no official-dist plugin manifests present")
	}

	st := newTestStore(t)
	ctx := context.Background()
	mgr := NewManager(Options{
		Store:              st,
		Authorizer:         cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
		OfficialPluginsDir: officialDistDir,
	})
	defer mgr.Close()

	// If gitlab-ci-viewer official-dist is present, register a matching Go builtin and verify hybrid merge, dedup & ESM serving!
	if presentIDs["gitlab-ci-viewer"] {
		if err := mgr.RegisterBuiltin(&stubBuiltinPlugin{manifest: Manifest{
			SchemaVersion: 1,
			ID:            "gitlab-ci-viewer",
			Name:          "GitLab CI 流水线",
			Version:       "1.0.0",
			Category:      "development",
			Backend:       BackendSpec{Type: BackendTypeBuiltin},
			Settings:      SettingsSpec{Mode: SettingsModeCustomUI},
			Contributes: ContributesSpec{
				SidecarPanels: []SidecarPanelContribution{
					{ID: "ci-panel", Title: "GitLab CI", MediaType: "application/x-gitlab-ci"},
				},
				AgentCapabilities: []AgentCapabilityContribution{
					{ID: "pipelines", Summary: "List pipelines"},
				},
				SignalProviders: []SignalProviderContribution{
					{Kind: "workspace.failure_signals"},
				},
			},
		}}); err != nil {
			t.Fatalf("RegisterBuiltin(gitlab-ci-viewer): %v", err)
		}
		view, err := mgr.GetPlugin(ctx, "gitlab-ci-viewer")
		if err != nil {
			t.Fatalf("GetPlugin(gitlab-ci-viewer): %v", err)
		}
		if view.Source != SourceBuiltin || view.BackendType != BackendTypeBuiltin || view.Frontend == nil || view.Frontend.Mode != FrontendModeESM || view.ESMBaseURL != "/api/v1/ext/gitlab-ci-viewer/_esm" || view.ContentDigest == "" {
			t.Fatalf("unexpected hybrid gitlab-ci-viewer view: %+v", view)
		}
		if len(view.Contributes.SidecarPanels) != 1 || view.Contributes.SidecarPanels[0].ID != "ci-panel" {
			t.Fatalf("expected deduplicated ci-panel sidecar panel, got %+v", view.Contributes.SidecarPanels)
		}
		if len(view.Contributes.AgentCapabilities) != 1 || len(view.Contributes.SignalProviders) != 1 || len(view.Contributes.Viewers) != 1 {
			t.Fatalf("unexpected merged gitlab-ci-viewer contributes: %+v", view.Contributes)
		}
	}

	// Verify ESM entry + styles serving across all present official-dist plugins.
	for _, spec := range expected {
		if !presentIDs[spec.id] {
			continue
		}
		view, err := mgr.GetPlugin(ctx, spec.id)
		if err != nil {
			t.Fatalf("GetPlugin(%s): %v", spec.id, err)
		}
		if !view.Enabled {
			if _, _, err := mgr.SetEnabled(ctx, spec.id, true, nil); err != nil {
				t.Fatalf("SetEnabled(%s, true): %v", spec.id, err)
			}
		}
		jsRec := httptest.NewRecorder()
		mgr.ServeHTTP(jsRec, authRequest(t, http.MethodGet, "/api/v1/ext/"+spec.id+"/_esm/"+view.Frontend.Entry+"?v="+view.ContentDigest, nil, ""))
		if jsRec.Code != http.StatusOK || jsRec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Fatalf("GET bundled %s %s: status=%d ct=%q", spec.id, view.Frontend.Entry, jsRec.Code, jsRec.Header().Get("Content-Type"))
		}
		for _, style := range view.Frontend.Styles {
			cssRec := httptest.NewRecorder()
			mgr.ServeHTTP(cssRec, authRequest(t, http.MethodGet, "/api/v1/ext/"+spec.id+"/_esm/"+style+"?v="+view.ContentDigest, nil, ""))
			if cssRec.Code != http.StatusOK || cssRec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
				t.Fatalf("GET bundled %s %s: status=%d ct=%q", spec.id, style, cssRec.Code, cssRec.Header().Get("Content-Type"))
			}
		}
	}
}
