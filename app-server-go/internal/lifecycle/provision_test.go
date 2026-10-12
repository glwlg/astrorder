package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/credentials"
)

func TestProvisionCreatesPrivateIdempotentEnvironmentWithoutDatabaseMigration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("provision requires current-user Windows DPAPI")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	staticDir := filepath.Join(root, "frontend", "dist")
	mustMkdir(t, staticDir)
	mustWrite(t, filepath.Join(staticDir, "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	dataDir := filepath.Join(stateDir, "data")

	result, err := Provision(Options{
		RootDir:       root,
		StateDir:      stateDir,
		DataDir:       dataDir,
		AppExecutable: appExecutable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigPath != filepath.Join(stateDir, "production.json") || result.CredentialsPath != filepath.Join(stateDir, "production.credentials.dpapi") {
		t.Fatalf("unexpected result paths: %#v", result)
	}

	body, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Environment map[string]string `json:"environment"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ASTRORDER_HOST":            "127.0.0.1",
		"ASTRORDER_PORT":            "30001",
		"ASTRORDER_STATIC_DIR":      staticDir,
		"ASTRORDER_ATTACHMENTS_DIR": filepath.Join(dataDir, "attachments"),
		"ASTRORDER_APP_EXECUTABLE":  appExecutable,
	}
	for key, value := range want {
		if envelope.Environment[key] != value {
			t.Fatalf("%s = %q, want %q", key, envelope.Environment[key], value)
		}
	}
	firstTarget := envelope.Environment["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"]
	if firstTarget == "" || validateGRPCTarget(firstTarget) != nil {
		t.Fatal("first install did not persist a valid loopback gRPC target")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "astrorder.sqlite3")); !os.IsNotExist(err) {
		t.Fatalf("provision created or touched the database: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dataDir, "attachments")); err != nil || !info.IsDir() {
		t.Fatalf("private attachments directory missing: %v", err)
	}

	firstCredentials, err := os.ReadFile(result.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	plainCredentials, err := credentials.Decrypt(firstCredentials)
	if err != nil {
		t.Fatal(err)
	}
	var secretValues map[string]string
	if err := json.Unmarshal(plainCredentials, &secretValues); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET", "ASTRORDER_SESSION_DAEMON_SECRET", "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL"} {
		if secretValues[key] == "" {
			t.Fatalf("credential %s was not generated", key)
		}
	}

	opened, err := OpenEnvironment(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if opened["ASTRORDER_BROWSER_SECRET"] != secretValues["ASTRORDER_BROWSER_SECRET"] || opened["ASTRORDER_STATIC_DIR"] != staticDir {
		t.Fatal("opened environment did not combine public settings and DPAPI credentials")
	}
	if _, exists := os.LookupEnv("ASTRORDER_BROWSER_SECRET"); exists {
		t.Fatal("OpenEnvironment modified process globals")
	}

	second, err := Provision(Options{
		RootDir:       root,
		StateDir:      stateDir,
		DataDir:       dataDir,
		AppExecutable: appExecutable,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondCredentials, err := os.ReadFile(second.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCredentials, secondCredentials) {
		t.Fatal("idempotent provision regenerated usable credentials")
	}
	_, secondEnvironment, _, err := readPublicEnvelope(second.ConfigPath)
	if err != nil || secondEnvironment["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"] != firstTarget {
		t.Fatal("idempotent provision changed the daemon listener address")
	}
}

func TestProvisionPreservesExistingConfigurationCredentialsAndUnknownFields(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("provision requires current-user Windows DPAPI")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	mustMkdir(t, stateDir)
	configPath := filepath.Join(stateDir, "production.json")
	original := []byte(`{
		"environment": {
			"ASTRORDER_HOST": "0.0.0.0",
			"ASTRORDER_PORT": 45678,
			"ASTRORDER_CUSTOM_FLAG": true
		},
		"user_metadata": {"keep": [1, 2, 3]}
	}`)
	mustWrite(t, configPath, original)
	oldSecrets := map[string]string{
		"ASTRORDER_BROWSER_SECRET": "existing-browser",
		"ASTRORDER_CUSTOM_SECRET":  "existing-custom",
	}
	plain, err := json.Marshal(oldSecrets)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := credentials.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(stateDir, "production.credentials.dpapi"), encrypted)

	_, err = Provision(Options{
		RootDir:       root,
		StateDir:      stateDir,
		DataDir:       filepath.Join(stateDir, "data"),
		AppExecutable: appExecutable,
		GRPCTarget:    "127.0.0.1:43127",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	environment := envelope["environment"].(map[string]any)
	if environment["ASTRORDER_HOST"] != "0.0.0.0" || environment["ASTRORDER_PORT"] != float64(45678) || environment["ASTRORDER_CUSTOM_FLAG"] != true {
		t.Fatalf("existing public values changed type or value: %#v", environment)
	}
	if envelope["user_metadata"].(map[string]any)["keep"] == nil {
		t.Fatal("unknown top-level configuration was lost")
	}
	if environment["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"] != "127.0.0.1:43127" {
		t.Fatal("explicit gRPC target was not persisted")
	}
	opened, err := OpenEnvironment(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if opened["ASTRORDER_BROWSER_SECRET"] != "existing-browser" || opened["ASTRORDER_CUSTOM_SECRET"] != "existing-custom" {
		t.Fatal("existing credentials were not preserved")
	}
	if opened["ASTRORDER_CONNECTOR_SECRET"] == "" || opened["ASTRORDER_SESSION_DAEMON_SECRET"] == "" {
		t.Fatal("missing credentials were not filled")
	}
}

func TestProvisionRejectsMixedCasePublicSecretBeforeWrites(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment names are case-insensitive")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")

	_, err := Provision(Options{
		RootDir:           root,
		StateDir:          stateDir,
		DataDir:           filepath.Join(stateDir, "data"),
		AppExecutable:     appExecutable,
		PublicEnvironment: map[string]string{"ASTRORDER_BROWSER_secret": "public-shadow-secret"},
	})
	if err == nil {
		t.Fatal("mixed-case public secret was accepted")
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("rejected public secret touched state directory: %v", statErr)
	}
}

func TestProvisionRejectsInvalidInputsWithoutReplacingExistingFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("provision requires current-user Windows DPAPI")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	mustMkdir(t, stateDir)
	configPath := filepath.Join(stateDir, "production.json")
	broken := []byte("{broken")
	mustWrite(t, configPath, broken)

	_, err := Provision(Options{
		RootDir: root, StateDir: stateDir, DataDir: filepath.Join(stateDir, "data"), AppExecutable: appExecutable,
	})
	if err == nil {
		t.Fatal("damaged existing configuration was silently replaced")
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil || !bytes.Equal(after, broken) {
		t.Fatal("damaged existing configuration changed")
	}
	if _, err := Provision(Options{
		RootDir: root, StateDir: filepath.Join(base, "other"), DataDir: filepath.Join(base, "other", "data"), AppExecutable: appExecutable,
		GRPCTarget: "127.0.0.1",
	}); err == nil {
		t.Fatal("gRPC target without an explicit port was accepted")
	}
	if _, err := Provision(Options{
		RootDir: root, StateDir: filepath.Join(base, "other"), DataDir: "relative-data", AppExecutable: appExecutable,
	}); err == nil {
		t.Fatal("relative data directory was accepted")
	}
}

func TestProvisionRejectsTrailingConfigurationContent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("provision requires current-user Windows DPAPI")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	mustMkdir(t, stateDir)
	configPath := filepath.Join(stateDir, "production.json")
	broken := []byte(`{"environment": {}} true`)
	mustWrite(t, configPath, broken)

	_, err := Provision(Options{
		RootDir: root, StateDir: stateDir, DataDir: filepath.Join(stateDir, "data"), AppExecutable: appExecutable,
	})
	if err == nil {
		t.Fatal("trailing configuration content was accepted")
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil || !bytes.Equal(after, broken) {
		t.Fatal("configuration with trailing content changed")
	}
}

func TestProvisionWaitsForStateDirectoryLifecycleLock(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("named lifecycle lock is Windows-specific")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	options := Options{RootDir: root, StateDir: stateDir, DataDir: filepath.Join(stateDir, "data"), AppExecutable: appExecutable}

	unlock, err := acquireLifecycleLock(context.Background(), stateDir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Provision(options)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Provision ignored lifecycle lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Provision did not continue after lifecycle lock release")
	}
}

func TestValidateProvisionPathsRejectsBroadOrMismatchedTargetsWithoutTouchingThem(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	insideExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, insideExecutable, []byte("fixture executable"))
	outsideExecutable := filepath.Join(base, "outside.exe")
	mustWrite(t, outsideExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	volumeRoot := filepath.Clean(filepath.VolumeName(base) + string(filepath.Separator))

	if _, err := validateProvisionPaths(Options{
		RootDir: volumeRoot, StateDir: stateDir, DataDir: filepath.Join(stateDir, "data"), AppExecutable: insideExecutable,
	}); err == nil {
		t.Fatal("volume root was accepted as installation root")
	}
	if _, err := validateProvisionPaths(Options{
		RootDir: root, StateDir: stateDir, DataDir: volumeRoot, AppExecutable: insideExecutable,
	}); err == nil {
		t.Fatal("volume root was accepted as private data directory")
	}
	if _, err := validateProvisionPaths(Options{
		RootDir: root, StateDir: stateDir, DataDir: filepath.Join(base, "outside-data"), AppExecutable: insideExecutable,
	}); err == nil {
		t.Fatal("data directory outside state directory was accepted")
	}
	if _, err := validateProvisionPaths(Options{
		RootDir: root, StateDir: stateDir, DataDir: filepath.Join(stateDir, "data"), AppExecutable: outsideExecutable,
	}); err == nil {
		t.Fatal("App executable outside installation root was accepted")
	}
}

func TestProvisionRejectsNullCredentialStoreWithoutChangingIt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("provision requires current-user Windows DPAPI")
	}
	base := t.TempDir()
	root := filepath.Join(base, "install")
	mustMkdir(t, filepath.Join(root, "frontend", "dist"))
	mustWrite(t, filepath.Join(root, "frontend", "dist", "index.html"), []byte("fixture"))
	appExecutable := filepath.Join(root, "astrorder-app.exe")
	mustWrite(t, appExecutable, []byte("fixture executable"))
	stateDir := filepath.Join(base, "state")
	mustMkdir(t, stateDir)
	encrypted, err := credentials.Encrypt([]byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(stateDir, "production.credentials.dpapi")
	mustWrite(t, credentialPath, encrypted)

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("invalid credential store caused panic: %v", recovered)
		}
	}()
	_, err = Provision(Options{
		RootDir: root, StateDir: stateDir, DataDir: filepath.Join(stateDir, "data"), AppExecutable: appExecutable,
	})
	if err == nil {
		t.Fatal("null credential store was silently accepted")
	}
	after, readErr := os.ReadFile(credentialPath)
	if readErr != nil || !bytes.Equal(after, encrypted) {
		t.Fatal("invalid credential store changed")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
}
