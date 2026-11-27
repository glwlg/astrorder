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
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
)

func envLookupCI(env []string, key string) (string, bool) {
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

func TestBuildPluginEnvStrictWhitelistAndSecretIsolation(t *testing.T) {
	// Populate host secrets and sensitive variables that MUST NEVER leak.
	t.Setenv("ASTRORDER_HOST_TOKEN", "leaked-host-token-999")
	t.Setenv("ASTRORDER_BROWSER_SECRET", "leaked-browser-secret-xyz")
	t.Setenv("ASTRORDER_CONNECTOR_SECRET", "leaked-connector-secret")
	t.Setenv("ASTRORDER_SESSION_DAEMON_SECRET", "leaked-daemon-secret")
	t.Setenv("ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL", "leaked-grpc-cred-abc")
	t.Setenv("ASTRORDER_CUSTOM_INTERNAL", "leaked-internal-flag")
	t.Setenv("OPENAI_API_KEY", "sk-secret-openai-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-super-secret")
	t.Setenv("RANDOM_UNLISTED_VAR", "should-not-pass")

	// Populate safe whitelisted OS runtime vars.
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TEMP", "/tmp/test-temp")

	// Populate proxy vars to verify conditional pass-through.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:7890")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "isolated-demo",
		Name:          "Isolated Demo",
		Version:       "1.0.0",
		Permissions:   []string{PermStorageKV},
		Backend: BackendSpec{
			Type:    BackendTypeProcess,
			Command: []string{"node", "server.mjs"},
			Env: map[string]string{
				"SAFE_PLUGIN_OPT":                      "enabled",
				"ASTRORDER_BROWSER_SECRET":             "spoofed-browser-secret",
				"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL": "spoofed-grpc",
				"ASTRORDER_HOST_TOKEN":                 "spoofed-host-token",
				"ASTRORDER_PLUGIN_ID":                  "spoofed-id",
				"HTTP_PROXY":                           "http://evil.proxy:8080",
			},
		},
	}

	// Case 1: without hostToken & without network:outbound
	env := buildPluginEnv(manifest, "/plugins/isolated-demo", 43210, "http://127.0.0.1:30001", "", []string{PermStorageKV})

	// Forbidden secrets MUST NOT appear at all.
	for _, forbidden := range []string{
		"ASTRORDER_HOST_TOKEN",
		"ASTRORDER_BROWSER_SECRET",
		"ASTRORDER_CONNECTOR_SECRET",
		"ASTRORDER_SESSION_DAEMON_SECRET",
		"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL",
		"ASTRORDER_CUSTOM_INTERNAL",
		"OPENAI_API_KEY",
		"AWS_SECRET_ACCESS_KEY",
		"RANDOM_UNLISTED_VAR",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"ALL_PROXY",
	} {
		if val, ok := envLookupCI(env, forbidden); ok {
			t.Fatalf("forbidden env var %q leaked into plugin env with value %q", forbidden, val)
		}
	}

	// Whitelisted base vars & explicit safe plugin vars MUST exist.
	if v, ok := envLookupCI(env, "LANG"); !ok || v != "en_US.UTF-8" {
		t.Fatalf("expected whitelisted LANG, got %q (ok=%v)", v, ok)
	}
	if v, ok := envLookupCI(env, "NO_COLOR"); !ok || v != "1" {
		t.Fatalf("expected whitelisted NO_COLOR, got %q (ok=%v)", v, ok)
	}
	if v, ok := envLookupCI(env, "TEMP"); !ok || v != "/tmp/test-temp" {
		t.Fatalf("expected whitelisted TEMP, got %q (ok=%v)", v, ok)
	}
	if v, ok := envLookupCI(env, "SAFE_PLUGIN_OPT"); !ok || v != "enabled" {
		t.Fatalf("expected manifest non-reserved env var SAFE_PLUGIN_OPT, got %q (ok=%v)", v, ok)
	}
	if v, _ := envLookupCI(env, "ASTRORDER_PLUGIN_ID"); v != "isolated-demo" {
		t.Fatalf("expected ASTRORDER_PLUGIN_ID=isolated-demo, got %q", v)
	}
	if v, _ := envLookupCI(env, "ASTRORDER_PLUGIN_NETWORK_OUTBOUND"); v != "0" {
		t.Fatalf("expected ASTRORDER_PLUGIN_NETWORK_OUTBOUND=0, got %q", v)
	}
	if v, _ := envLookupCI(env, "ASTRORDER_PLUGIN_NETWORK_POLICY"); v != NetworkPolicyBlocked {
		t.Fatalf("expected ASTRORDER_PLUGIN_NETWORK_POLICY=%s, got %q", NetworkPolicyBlocked, v)
	}

	// Case 2: per-plugin ephemeral token is passed ONLY as ephemeral token, never host env value.
	envWithTok := buildPluginEnv(manifest, "/plugins/isolated-demo", 43210, "http://127.0.0.1:30001", "ephemeral-plugin-token-123", []string{PermStorageKV})
	if v, _ := envLookupCI(envWithTok, "ASTRORDER_PLUGIN_HOST_TOKEN"); v != "ephemeral-plugin-token-123" {
		t.Fatalf("expected per-plugin ephemeral token, got %q", v)
	}
	for _, entry := range envWithTok {
		if strings.Contains(entry, "leaked-host-token-999") || strings.Contains(entry, "leaked-browser-secret-xyz") || strings.Contains(entry, "leaked-grpc-cred-abc") {
			t.Fatalf("host secret value leaked into env entry: %q", entry)
		}
	}

	// Case 3: declared network:outbound WITHOUT user grant MUST still block proxy passthrough!
	manifestWithNet := manifest
	manifestWithNet.Permissions = []string{PermStorageKV, PermNetworkOutbound}
	envDeclaredOnly := buildPluginEnv(manifestWithNet, "/plugins/isolated-demo", 43210, "http://127.0.0.1:30001", "", []string{PermStorageKV})
	for _, proxyKey := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if val, ok := envLookupCI(envDeclaredOnly, proxyKey); ok {
			t.Fatalf("proxy env %q leaked when network:outbound was declared but NOT granted: %q", proxyKey, val)
		}
	}
	if v, _ := envLookupCI(envDeclaredOnly, "ASTRORDER_PLUGIN_NETWORK_OUTBOUND"); v != "0" {
		t.Fatalf("expected network outbound blocked until granted, got %q", v)
	}

	// Case 4: BOTH declared AND granted network:outbound -> proxy vars pass through!
	envGrantedNet := buildPluginEnv(manifestWithNet, "/plugins/isolated-demo", 43210, "http://127.0.0.1:30001", "", []string{PermStorageKV, PermNetworkOutbound})
	if v, ok := envLookupCI(envGrantedNet, "HTTPS_PROXY"); !ok || v != "http://127.0.0.1:7890" {
		t.Fatalf("expected HTTPS_PROXY passthrough when network:outbound granted, got %q (ok=%v)", v, ok)
	}
	if v, ok := envLookupCI(envGrantedNet, "NO_PROXY"); !ok || v != "localhost,127.0.0.1" {
		t.Fatalf("expected NO_PROXY passthrough when network:outbound granted, got %q (ok=%v)", v, ok)
	}
	if v, _ := envLookupCI(envGrantedNet, "ASTRORDER_PLUGIN_NETWORK_OUTBOUND"); v != "1" {
		t.Fatalf("expected ASTRORDER_PLUGIN_NETWORK_OUTBOUND=1, got %q", v)
	}
	if v, _ := envLookupCI(envGrantedNet, "ASTRORDER_PLUGIN_NETWORK_POLICY"); v != NetworkPolicyAllowed {
		t.Fatalf("expected ASTRORDER_PLUGIN_NETWORK_POLICY=%s, got %q", NetworkPolicyAllowed, v)
	}
}

func TestOfficeModeBlocksUnsignedProcessPluginsAndAllowsSignedOrDeveloper(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Set global appProfile to office mode via the exact frontend preference key.
	if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "office"); err != nil {
		t.Fatalf("set office profile: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	pluginsDir := filepath.Join(t.TempDir(), "plugins", "external")
	mgr := NewManager(Options{
		Store:             st,
		Authorizer:        stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir:        pluginsDir,
		TrustedPublicKeys: map[string]ed25519.PublicKey{"official-v1": pub},
	})
	defer mgr.Close()

	if prof := mgr.EffectiveAppProfile(ctx); prof != AppProfileOffice {
		t.Fatalf("expected EffectiveAppProfile=office, got %q", prof)
	}

	selfBin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	makeProcessSrc := func(id string) string {
		src := t.TempDir()
		m := Manifest{
			SchemaVersion: 1,
			ID:            id,
			Name:          "Proc " + id,
			Version:       "1.0.0",
			Permissions:   []string{PermStorageKV},
			Backend: BackendSpec{
				Type:    BackendTypeProcess,
				Command: []string{selfBin},
				Args:    []string{"-test.run=^TestHelperPluginProcess$"},
				Env:     map[string]string{"ASTRORDER_TEST_HELPER_PLUGIN": "1"},
				Health:  "/health",
			},
		}
		raw, _ := json.MarshalIndent(m, "", "  ")
		_ = os.WriteFile(filepath.Join(src, "plugin.json"), raw, 0o644)
		_ = os.WriteFile(filepath.Join(src, "README.txt"), []byte("hello"), 0o644)
		return src
	}

	// 1. Installing unsigned process plugin from dir MUST fail with ErrPermissionDenied (403).
	unsignedSrc := makeProcessSrc("unsigned-proc")
	if _, err := mgr.InstallFromDir(ctx, unsignedSrc); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied installing unsigned process plugin in office mode, got %v", err)
	}

	// 2. Installing unsigned process plugin from zip MUST also fail with ErrPermissionDenied.
	unsignedZip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"unsigned-zip-proc","name":"Unsigned Zip","version":"1.0.0","backend":{"type":"process","command":["node","server.mjs"]}}`,
	})
	if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(unsignedZip), int64(len(unsignedZip))); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied installing unsigned zip process plugin in office mode, got %v", err)
	}

	// 3. Forged signature string MUST NOT bypass office mode!
	forgedSrc := makeProcessSrc("forged-proc")
	forgedManifest, _ := LoadManifestFile(filepath.Join(forgedSrc, "plugin.json"))
	forgedManifest.Signature = &PluginSignature{Algorithm: "ed25519", KeyID: "official-v1", Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="}
	forgedBytes, _ := json.MarshalIndent(forgedManifest, "", "  ")
	_ = os.WriteFile(filepath.Join(forgedSrc, "plugin.json"), forgedBytes, 0o644)
	if _, err := mgr.InstallFromDir(ctx, forgedSrc); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected forged signature to be rejected in office mode, got %v", err)
	}

	// 4. Cryptographically signed plugin with trusted key IS allowed in office mode!
	signedSrc := makeProcessSrc("signed-proc")
	sig, err := SignPluginDir(signedSrc, "official-v1", priv)
	if err != nil {
		t.Fatalf("SignPluginDir: %v", err)
	}
	signedManifest, _ := LoadManifestFile(filepath.Join(signedSrc, "plugin.json"))
	signedManifest.Signature = sig
	signedBytes, _ := json.MarshalIndent(signedManifest, "", "  ")
	_ = os.WriteFile(filepath.Join(signedSrc, "plugin.json"), signedBytes, 0o644)
	if _, err := mgr.InstallFromDir(ctx, signedSrc); err != nil {
		t.Fatalf("expected legitimately signed process plugin to install in office mode, got %v", err)
	}

	// Tampering with a file in the signed plugin directory MUST invalidate the signature.
	_ = os.WriteFile(filepath.Join(pluginsDir, "signed-proc", "README.txt"), []byte("tampered!"), 0o644)
	if _, _, err := mgr.SetEnabled(ctx, "signed-proc", true, []string{PermStorageKV}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected tampered signed plugin enable to fail in office mode, got %v", err)
	}

	// 5. Switch to developer mode -> unsigned process plugin installs & starts!
	if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "developer"); err != nil {
		t.Fatalf("set developer profile: %v", err)
	}
	ts := httptest.NewServer(mgr)
	defer ts.Close()
	mgr.SetHostBaseURL(ts.URL)

	if _, err := mgr.InstallFromDir(ctx, unsignedSrc); err != nil {
		t.Fatalf("expected unsigned process plugin to install in developer mode, got %v", err)
	}
	view, _, err := mgr.SetEnabled(ctx, "unsigned-proc", true, []string{PermStorageKV})
	if err != nil || view.RuntimeState != RuntimeStateReady {
		t.Fatalf("expected unsigned process plugin to start in developer mode, view=%+v err=%v", view, err)
	}

	// Verify runtime status records blocked outbound network boundary.
	stStatus := mgr.supervisor.Status("unsigned-proc")
	if stStatus.NetworkOutboundAllowed || stStatus.NetworkPolicy != NetworkPolicyBlocked {
		t.Fatalf("expected blocked network boundary recorded on runtime status, got %+v", stStatus)
	}

	// 6. Switch BACK to office mode -> Restart, ReloadExternal auto-start, and HTTP proxy MUST block & stop it!
	if err := st.SavePluginConfigValue(ctx, "app-profile", "mode", "office"); err != nil {
		t.Fatalf("switch back to office: %v", err)
	}
	if _, err := mgr.Restart(ctx, "unsigned-proc"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected Restart to return ErrPermissionDenied in office mode, got %v", err)
	}
	if err := mgr.ReloadExternal(ctx); err != nil {
		t.Fatalf("ReloadExternal: %v", err)
	}
	if got := mgr.supervisor.Status("unsigned-proc").State; got != RuntimeStateStopped {
		t.Fatalf("expected ReloadExternal in office mode to stop unsigned process plugin, got state=%q", got)
	}
}

func TestAtomicPluginUpgradeAndRollbackOnFailure(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	pluginsDir := filepath.Join(t.TempDir(), "plugins", "external")
	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir: pluginsDir,
	})
	defer mgr.Close()

	// Install v1.
	v1Zip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"atomic-demo","name":"Atomic Demo","version":"1.0.0","backend":{"type":"none"}}`,
		"data.txt":    "version-1-content",
	})
	v1View, err := mgr.InstallFromZip(ctx, bytes.NewReader(v1Zip), int64(len(v1Zip)))
	if err != nil || v1View.Version != "1.0.0" {
		t.Fatalf("install v1: view=%+v err=%v", v1View, err)
	}

	// Simulate failure ONLY when moving staging dir into targetDir (after .bak rename succeeds!).
	targetDir := filepath.Join(pluginsDir, "atomic-demo")
	var backupSeen string
	var rollbackSeen bool
	mgr.renameFn = func(oldPath, newPath string) error {
		if oldPath == targetDir && strings.Contains(filepath.Base(newPath), ".bak-") {
			backupSeen = newPath
			return os.Rename(oldPath, newPath)
		}
		if newPath == targetDir && strings.HasPrefix(filepath.Base(oldPath), ".staging-") {
			return errors.New("simulated filesystem swap error")
		}
		if oldPath == backupSeen && newPath == targetDir {
			rollbackSeen = true
			return os.Rename(oldPath, newPath)
		}
		return os.Rename(oldPath, newPath)
	}

	v2Zip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"atomic-demo","name":"Atomic Demo","version":"2.0.0","backend":{"type":"none"}}`,
		"data.txt":    "version-2-content",
	})
	if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(v2Zip), int64(len(v2Zip))); err == nil {
		t.Fatalf("expected simulated upgrade failure")
	}
	if backupSeen == "" {
		t.Fatalf("expected existing plugin directory to be renamed to .bak-<ts> before swap")
	}
	if !rollbackSeen {
		t.Fatalf("expected automatic rollback from backup dir %q to %q", backupSeen, targetDir)
	}

	// Verify v1 content is completely intact and no leftover .bak or .staging dirs remain!
	gotData, err := os.ReadFile(filepath.Join(targetDir, "data.txt"))
	if err != nil || string(gotData) != "version-1-content" {
		t.Fatalf("expected v1 content restored after failed upgrade, got %q err=%v", string(gotData), err)
	}
	if _, err := os.Stat(backupSeen); !os.IsNotExist(err) {
		t.Fatalf("expected backup dir moved back on rollback, stat err=%v", err)
	}

	// Now allow swap to succeed and verify v2 replaces v1 cleanly with .bak removed.
	mgr.renameFn = nil
	v2View, err := mgr.InstallFromZip(ctx, bytes.NewReader(v2Zip), int64(len(v2Zip)))
	if err != nil || v2View.Version != "2.0.0" {
		t.Fatalf("upgrade to v2 failed: view=%+v err=%v", v2View, err)
	}
	gotData2, err := os.ReadFile(filepath.Join(targetDir, "data.txt"))
	if err != nil || string(gotData2) != "version-2-content" {
		t.Fatalf("expected v2 content after successful upgrade, got %q err=%v", string(gotData2), err)
	}
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatalf("readdir pluginsDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") || strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("unexpected leftover temp/backup dir %q after successful upgrade", e.Name())
		}
	}
}

func TestDirectoryIsolationProtectsBuiltinR2T2AndSupportsLegacyExternal(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	rootPlugins := filepath.Join(t.TempDir(), "plugins")
	externalPlugins := filepath.Join(rootPlugins, "external")
	r2t2Dir := filepath.Join(rootPlugins, "r2t2")
	if err := os.MkdirAll(filepath.Join(r2t2Dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	r2t2Marker := filepath.Join(r2t2Dir, "models", "Confucius4-R2T2-Q8_0.gguf")
	if err := os.WriteFile(r2t2Marker, []byte("precious-model"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Even if someone drops a plugin.json inside r2t2/, ReloadExternal MUST skip it!
	_ = os.WriteFile(filepath.Join(r2t2Dir, "plugin.json"), []byte(`{"schema_version":1,"id":"r2t2","name":"Fake R2T2","version":"1.0.0"}`), 0o644)

	// Seed a pre-existing legacy external plugin directly under rootPlugins/legacy-demo/.
	legacyDemoDir := filepath.Join(rootPlugins, "legacy-demo")
	if err := os.MkdirAll(legacyDemoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(legacyDemoDir, "plugin.json"), []byte(`{"schema_version":1,"id":"legacy-demo","name":"Legacy Demo","version":"1.0.0","backend":{"type":"none"}}`), 0o644)

	mgr := NewManager(Options{
		Store:            st,
		Authorizer:       stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir:       externalPlugins,
		LegacyPluginsDir: rootPlugins,
	})
	defer mgr.Close()

	// 1. ReloadExternal discovers legacy-demo, MUST NOT load r2t2.
	views, err := mgr.ListPlugins(ctx)
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	foundLegacy := false
	for _, v := range views {
		if v.ID == "r2t2" {
			t.Fatalf("r2t2 directory MUST be skipped by external plugin scanner")
		}
		if v.ID == "legacy-demo" {
			foundLegacy = true
		}
	}
	if !foundLegacy {
		t.Fatalf("expected legacy external plugin to remain discoverable, got %+v", views)
	}

	// 2. Installing a plugin with reserved id "r2t2" MUST be rejected with ErrConflict and leave r2t2 untouched.
	evilR2T2Zip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"r2t2","name":"Overwrite R2T2","version":"9.9.9"}`,
	})
	if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(evilR2T2Zip), int64(len(evilR2T2Zip))); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict installing reserved id r2t2, got %v", err)
	}

	// 3. Uninstalling "r2t2" MUST be rejected with ErrConflict and NEVER delete r2t2 directory.
	if err := mgr.Uninstall(ctx, "r2t2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict uninstalling reserved id r2t2, got %v", err)
	}
	if raw, err := os.ReadFile(r2t2Marker); err != nil || string(raw) != "precious-model" {
		t.Fatalf("R2T2 model file was damaged or deleted: err=%v data=%q", err, string(raw))
	}

	// 4. New installs land inside isolated plugins/external/<id>/, not shared rootPlugins/.
	newZip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"fresh-ext","name":"Fresh Ext","version":"1.0.0","backend":{"type":"none"}}`,
	})
	if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(newZip), int64(len(newZip))); err != nil {
		t.Fatalf("install fresh-ext: %v", err)
	}
	if _, err := os.Stat(filepath.Join(externalPlugins, "fresh-ext", "plugin.json")); err != nil {
		t.Fatalf("expected fresh-ext installed inside isolated external dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootPlugins, "fresh-ext")); !os.IsNotExist(err) {
		t.Fatalf("fresh-ext should NOT be written directly into shared plugins root")
	}
}

func TestSupervisorCloseContextBlocksUntilSubprocessExits(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	pluginsDir := filepath.Join(t.TempDir(), "plugins", "external")
	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir: pluginsDir,
	})

	ts := httptest.NewServer(mgr)
	defer ts.Close()
	mgr.SetHostBaseURL(ts.URL)

	selfBin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	src := t.TempDir()
	m := Manifest{
		SchemaVersion: 1,
		ID:            "graceful-stop-demo",
		Name:          "Graceful Stop Demo",
		Version:       "1.0.0",
		Backend: BackendSpec{
			Type:    BackendTypeProcess,
			Command: []string{selfBin},
			Args:    []string{"-test.run=^TestHelperPluginProcess$"},
			Env:     map[string]string{"ASTRORDER_TEST_HELPER_PLUGIN": "1"},
			Health:  "/health",
		},
	}
	raw, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(src, "plugin.json"), raw, 0o644)

	if _, err := mgr.InstallFromDir(ctx, src); err != nil {
		t.Fatalf("install: %v", err)
	}
	view, _, err := mgr.SetEnabled(ctx, "graceful-stop-demo", true, nil)
	if err != nil || view.RuntimeState != RuntimeStateReady {
		t.Fatalf("enable: view=%+v err=%v", view, err)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mgr.CloseContext(closeCtx); err != nil {
		t.Fatalf("CloseContext returned error: %v", err)
	}
	if st := mgr.supervisor.Status("graceful-stop-demo").State; st != RuntimeStateStopped {
		t.Fatalf("expected stopped state after CloseContext, got %q", st)
	}

	// Calling CloseContext again is idempotent and returns immediately.
	if err := mgr.CloseContext(closeCtx); err != nil {
		t.Fatalf("second CloseContext failed: %v", err)
	}

	// Verify store preference key compatibility hasn't drifted.
	_ = store.PrefPluginsConfig
}

func TestRealSubprocessEnvIsolationAndNetworkBoundary(t *testing.T) {
	// Inject host secrets and proxy vars into parent process environment.
	t.Setenv("ASTRORDER_HOST_TOKEN", "parent-host-secret-should-not-leak")
	t.Setenv("ASTRORDER_BROWSER_SECRET", "parent-browser-secret-should-not-leak")
	t.Setenv("ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL", "parent-grpc-secret-should-not-leak")
	t.Setenv("OPENAI_API_KEY", "sk-parent-secret-should-not-leak")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:17890")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:17890")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	st := newTestStore(t)
	ctx := context.Background()
	pluginsDir := filepath.Join(t.TempDir(), "plugins", "external")
	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir: pluginsDir,
	})
	defer mgr.Close()

	ts := httptest.NewServer(mgr)
	defer ts.Close()
	mgr.SetHostBaseURL(ts.URL)

	selfBin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	installAndReadEnv := func(id string, perms []string) map[string]string {
		t.Helper()
		src := t.TempDir()
		m := Manifest{
			SchemaVersion: 1,
			ID:            id,
			Name:          id,
			Version:       "1.0.0",
			Permissions:   perms,
			Backend: BackendSpec{
				Type:    BackendTypeProcess,
				Command: []string{selfBin},
				Args:    []string{"-test.run=^TestHelperPluginProcess$"},
				Env:     map[string]string{"ASTRORDER_TEST_HELPER_PLUGIN": "1"},
				Health:  "/health",
			},
		}
		raw, _ := json.Marshal(m)
		_ = os.WriteFile(filepath.Join(src, "plugin.json"), raw, 0o644)
		if _, err := mgr.InstallFromDir(ctx, src); err != nil {
			t.Fatalf("install %s: %v", id, err)
		}
		view, _, err := mgr.SetEnabled(ctx, id, true, perms)
		if err != nil || view.RuntimeState != RuntimeStateReady {
			t.Fatalf("enable %s: view=%+v err=%v", id, view, err)
		}
		resp, err := http.DefaultClient.Do(authRequest(t, http.MethodGet, ts.URL+"/api/v1/ext/"+id+"/echo-env", nil, ""))
		if err != nil {
			t.Fatalf("echo-env %s: %v", id, err)
		}
		defer resp.Body.Close()
		var body struct {
			Env map[string]string `json:"env"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode echo-env %s: %v", id, err)
		}
		return body.Env
	}

	// 1. Subprocess WITHOUT network:outbound -> ZERO host secrets, ZERO proxy vars!
	noNetEnv := installAndReadEnv("subproc-no-net", []string{PermStorageKV})
	for _, secretKey := range []string{"ASTRORDER_BROWSER_SECRET", "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL", "OPENAI_API_KEY", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if val, exists := noNetEnv[secretKey]; exists {
			t.Fatalf("real subprocess without network:outbound received forbidden env %s=%q", secretKey, val)
		}
	}
	if noNetEnv["ASTRORDER_HOST_TOKEN"] == "parent-host-secret-should-not-leak" {
		t.Fatalf("parent ASTRORDER_HOST_TOKEN leaked into real subprocess!")
	}
	if noNetEnv["ASTRORDER_PLUGIN_NETWORK_OUTBOUND"] != "0" || noNetEnv["ASTRORDER_PLUGIN_NETWORK_POLICY"] != NetworkPolicyBlocked {
		t.Fatalf("expected blocked network boundary env in subprocess, got outbound=%q policy=%q", noNetEnv["ASTRORDER_PLUGIN_NETWORK_OUTBOUND"], noNetEnv["ASTRORDER_PLUGIN_NETWORK_POLICY"])
	}

	// 2. Subprocess WITH granted network:outbound -> STILL zero host secrets, proxy vars forwarded!
	netEnv := installAndReadEnv("subproc-with-net", []string{PermStorageKV, PermNetworkOutbound})
	for _, secretKey := range []string{"ASTRORDER_BROWSER_SECRET", "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL", "OPENAI_API_KEY"} {
		if val, exists := netEnv[secretKey]; exists {
			t.Fatalf("real subprocess with network:outbound leaked host secret %s=%q", secretKey, val)
		}
	}
	if netEnv["HTTPS_PROXY"] != "http://127.0.0.1:17890" || netEnv["NO_PROXY"] != "localhost,127.0.0.1" {
		t.Fatalf("expected proxy env forwarded with network:outbound, got HTTPS_PROXY=%q NO_PROXY=%q", netEnv["HTTPS_PROXY"], netEnv["NO_PROXY"])
	}
	if netEnv["ASTRORDER_PLUGIN_NETWORK_OUTBOUND"] != "1" || netEnv["ASTRORDER_PLUGIN_NETWORK_POLICY"] != NetworkPolicyAllowed {
		t.Fatalf("expected allowed network boundary env, got outbound=%q policy=%q", netEnv["ASTRORDER_PLUGIN_NETWORK_OUTBOUND"], netEnv["ASTRORDER_PLUGIN_NETWORK_POLICY"])
	}
}

func TestRemoteHTTPPluginRequiresNetworkOutboundPermission(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	pluginsDir := filepath.Join(t.TempDir(), "plugins", "external")
	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir: pluginsDir,
	})
	defer mgr.Close()

	remoteZip := makeZipBytes(t, map[string]string{
		"plugin.json": `{
			"schema_version": 1,
			"id": "remote-http-no-net",
			"name": "Remote HTTP No Net",
			"version": "1.0.0",
			"permissions": ["storage:kv"],
			"backend": {"type": "http", "url": "https://api.example.invalid"}
		}`,
	})
	if _, err := mgr.InstallFromZip(ctx, bytes.NewReader(remoteZip), int64(len(remoteZip))); err != nil {
		t.Fatalf("install remote http plugin: %v", err)
	}
	if _, _, err := mgr.SetEnabled(ctx, "remote-http-no-net", true, []string{PermStorageKV}); err != nil {
		t.Fatalf("enable remote http plugin: %v", err)
	}

	mgr.mu.RLock()
	ext := mgr.externals["remote-http-no-net"]
	mgr.mu.RUnlock()
	if _, err := mgr.resolveExternalBackendURL(ctx, ext); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for non-loopback HTTP plugin without network:outbound, got %v", err)
	}
}
