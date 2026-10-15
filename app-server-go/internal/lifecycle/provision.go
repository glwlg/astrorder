package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/credentials"
)

const (
	configName      = "production.json"
	credentialsName = "production.credentials.dpapi"
)

var requiredCredentialKeys = []string{
	"ASTRORDER_BROWSER_SECRET",
	"ASTRORDER_CONNECTOR_SECRET",
	"ASTRORDER_SESSION_DAEMON_SECRET",
	"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL",
}

// Options contains explicit installation paths and optional first-install
// public settings. Provision never infers a production directory.
type Options struct {
	RootDir           string
	StateDir          string
	DataDir           string
	AppExecutable     string
	GRPCTarget        string
	PublicEnvironment map[string]string
	DesktopOrigin     bool
}

// ProvisionResult identifies the files created or merged by Provision.
type ProvisionResult struct {
	ConfigPath      string
	CredentialsPath string
	DataDir         string
}

// Provision atomically fills missing production settings and credentials.
// Existing settings, credentials, data, and unknown envelope fields survive.
func Provision(options Options) (ProvisionResult, error) {
	paths, err := validateProvisionPaths(options)
	if err != nil {
		return ProvisionResult{}, err
	}
	return withProvisionInputs(options, paths)
}

func withProvisionInputs(options Options, paths provisionPaths) (ProvisionResult, error) {
	for key, value := range options.PublicEnvironment {
		if err := validatePublicSetting(key, value); err != nil {
			return ProvisionResult{}, err
		}
	}
	if options.GRPCTarget != "" {
		if err := validateGRPCTarget(options.GRPCTarget); err != nil {
			return ProvisionResult{}, err
		}
	}
	unlock, err := acquireLifecycleLock(context.Background(), paths.stateDir)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("acquire provision lifecycle lock: %w", err)
	}
	defer unlock()
	paths, err = validateProvisionPaths(options)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("revalidate provision paths under lifecycle lock: %w", err)
	}
	return provisionLocked(options, paths)
}

func provisionLocked(options Options, paths provisionPaths) (ProvisionResult, error) {
	if err := privateDir(paths.stateDir); err != nil {
		return ProvisionResult{}, fmt.Errorf("create state directory: %w", err)
	}
	if err := privateDir(paths.dataDir); err != nil {
		return ProvisionResult{}, fmt.Errorf("create data directory: %w", err)
	}
	if err := privateDir(filepath.Join(paths.dataDir, "attachments")); err != nil {
		return ProvisionResult{}, fmt.Errorf("create attachments directory: %w", err)
	}

	configPath := filepath.Join(paths.stateDir, configName)
	credentialPath := filepath.Join(paths.stateDir, credentialsName)
	envelope, environment, configExists, err := readPublicEnvelope(configPath)
	if err != nil {
		return ProvisionResult{}, err
	}
	rawEnvironment, _ := envelope["environment"].(map[string]any)
	if rawEnvironment == nil {
		rawEnvironment = make(map[string]any)
		envelope["environment"] = rawEnvironment
	}
	defaults := map[string]string{
		"ASTRORDER_HOST":               "127.0.0.1",
		"ASTRORDER_PORT":               "30001",
		"ASTRORDER_DATABASE_URL":       "sqlite:///" + filepath.ToSlash(filepath.Join(paths.dataDir, "astrorder.sqlite3")),
		"ASTRORDER_ATTACHMENTS_DIR":    filepath.Join(paths.dataDir, "attachments"),
		"ASTRORDER_STATIC_DIR":         paths.staticDir,
		"ASTRORDER_ALLOWED_ORIGINS":    "http://127.0.0.1:30001,http://localhost:30001",
		"ASTRORDER_ALLOWED_WORKSPACES": paths.rootDir,
		"ASTRORDER_APP_EXECUTABLE":     paths.appExecutable,
	}
	for key, value := range options.PublicEnvironment {
		if err := validatePublicSetting(key, value); err != nil {
			return ProvisionResult{}, err
		}
		defaults[key] = value
	}
	// If explicit GRPCTarget provided or existing in environment, preserve it.
	// If Desktop/user provided GRPCTarget, validate and persist.
	if options.GRPCTarget != "" {
		if err := validateGRPCTarget(options.GRPCTarget); err != nil {
			return ProvisionResult{}, err
		}
		defaults["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"] = options.GRPCTarget
	} else if envTarget, ok := environment["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"]; ok && envTarget != "" {
		defaults["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"] = envTarget
	} else {
		port, err := selectSafeLoopbackPort(0)
		if err != nil {
			return ProvisionResult{}, fmt.Errorf("allocate daemon gRPC listener: %w", err)
		}
		defaults["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}

	configChanged := !configExists
	for key, value := range defaults {
		if _, exists := environment[key]; exists {
			continue
		}
		environment[key] = value
		rawEnvironment[key] = value
		configChanged = true
	}

	if options.DesktopOrigin {
		origins := strings.Split(environment["ASTRORDER_ALLOWED_ORIGINS"], ",")
		found := false
		for _, origin := range origins {
			if strings.TrimSpace(origin) == "http://wails.localhost" {
				found = true
			}
		}
		if !found {
			origins = append(origins, "http://wails.localhost")
			environment["ASTRORDER_ALLOWED_ORIGINS"] = strings.Join(origins, ",")
			rawEnvironment["ASTRORDER_ALLOWED_ORIGINS"] = environment["ASTRORDER_ALLOWED_ORIGINS"]
			configChanged = true
		}
	}
	if _, err := EnsureDaemonConfig(paths.stateDir, paths.rootDir); err != nil {
		return ProvisionResult{}, fmt.Errorf("ensure session-daemon.json: %w", err)
	}

	secretValues, encryptedExisting, credentialsChanged, err := readOrCreateCredentials(credentialPath)
	if err != nil {
		return ProvisionResult{}, err
	}
	var configBody []byte
	if configChanged {
		configBody, err = json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return ProvisionResult{}, fmt.Errorf("encode public production settings: %w", err)
		}
		configBody = append(configBody, '\n')
	}
	var credentialsBody []byte
	if credentialsChanged {
		plain, marshalErr := json.Marshal(secretValues)
		if marshalErr != nil {
			return ProvisionResult{}, fmt.Errorf("encode production credentials: %w", marshalErr)
		}
		credentialsBody, err = credentials.Encrypt(plain)
		if err != nil {
			return ProvisionResult{}, fmt.Errorf("protect production credentials: %w", err)
		}
	} else if len(encryptedExisting) == 0 {
		return ProvisionResult{}, errors.New("production credential store is empty")
	}

	// Publish credentials before public settings so a failed first provision can
	// never expose a ready-looking production.json without its credential store.
	if credentialsChanged {
		if err := atomicWrite(credentialPath, credentialsBody, 0o600); err != nil {
			return ProvisionResult{}, fmt.Errorf("write production credentials: %w", err)
		}
	}
	if err := privateFile(credentialPath); err != nil {
		return ProvisionResult{}, fmt.Errorf("protect production credential file ACL: %w", err)
	}
	if configChanged {
		if err := atomicWrite(configPath, configBody, 0o600); err != nil {
			return ProvisionResult{}, fmt.Errorf("write public production settings: %w", err)
		}
	}
	if err := privateFile(configPath); err != nil {
		return ProvisionResult{}, fmt.Errorf("protect public production settings ACL: %w", err)
	}
	return ProvisionResult{ConfigPath: configPath, CredentialsPath: credentialPath, DataDir: paths.dataDir}, nil
}

// OpenEnvironment reads and combines public settings with current-user DPAPI
// credentials. It does not mutate the process environment.
func OpenEnvironment(stateDir string) (map[string]string, error) {
	if err := requireAbsolute("state directory", stateDir); err != nil {
		return nil, err
	}
	_, environment, exists, err := readPublicEnvelope(filepath.Join(filepath.Clean(stateDir), configName))
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("public production settings do not exist")
	}
	encrypted, err := os.ReadFile(filepath.Join(filepath.Clean(stateDir), credentialsName))
	if err != nil {
		return nil, fmt.Errorf("read production credentials: %w", err)
	}
	plain, err := credentials.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("unprotect production credentials: %w", err)
	}
	var secretValues map[string]string
	if err := json.Unmarshal(plain, &secretValues); err != nil {
		return nil, fmt.Errorf("decode production credentials: %w", err)
	}
	if secretValues == nil {
		return nil, errors.New("decode production credentials: credential object is required")
	}
	for _, key := range requiredCredentialKeys {
		if secretValues[key] == "" {
			return nil, fmt.Errorf("production credential %s is missing", key)
		}
	}
	combined := make(map[string]string, len(environment)+len(secretValues))
	for key, value := range environment {
		combined[key] = value
	}
	for key, value := range secretValues {
		if key != strings.ToUpper(key) || !strings.HasPrefix(key, "ASTRORDER_") || value == "" {
			return nil, errors.New("production credential store contains an invalid entry")
		}
		if _, public := combined[key]; public {
			return nil, fmt.Errorf("credential %s also appears in public production settings", key)
		}
		combined[key] = value
	}
	return combined, nil
}

type provisionPaths struct {
	rootDir, stateDir, dataDir, staticDir, appExecutable string
}

func validateProvisionPaths(options Options) (provisionPaths, error) {
	for label, path := range map[string]string{
		"root directory":  options.RootDir,
		"state directory": options.StateDir,
		"data directory":  options.DataDir,
		"App executable":  options.AppExecutable,
	} {
		if err := requireAbsolute(label, path); err != nil {
			return provisionPaths{}, err
		}
	}
	paths := provisionPaths{
		rootDir: filepath.Clean(options.RootDir), stateDir: filepath.Clean(options.StateDir),
		dataDir: filepath.Clean(options.DataDir), appExecutable: filepath.Clean(options.AppExecutable),
	}
	for label, path := range map[string]string{"root directory": paths.rootDir, "state directory": paths.stateDir, "data directory": paths.dataDir} {
		if filepath.Clean(path) == filepath.Clean(filepath.VolumeName(path)+string(filepath.Separator)) {
			return provisionPaths{}, fmt.Errorf("%s must not be a volume root", label)
		}
	}
	if !pathWithin(paths.stateDir, paths.dataDir) || samePath(paths.stateDir, paths.dataDir) {
		return provisionPaths{}, errors.New("data directory must be a child of the state directory")
	}
	if !pathWithin(paths.rootDir, paths.appExecutable) {
		return provisionPaths{}, errors.New("App executable must be inside the installation root")
	}
	rootInfo, err := os.Stat(paths.rootDir)
	if err != nil || !rootInfo.IsDir() {
		return provisionPaths{}, errors.New("root directory must be an existing directory")
	}
	executableInfo, err := os.Stat(paths.appExecutable)
	if err != nil || !executableInfo.Mode().IsRegular() {
		return provisionPaths{}, errors.New("App executable must be an existing regular file")
	}
	paths.staticDir = filepath.Join(paths.rootDir, "frontend", "dist")
	indexInfo, err := os.Stat(filepath.Join(paths.staticDir, "index.html"))
	if err != nil || !indexInfo.Mode().IsRegular() {
		return provisionPaths{}, errors.New("root directory must contain frontend/dist/index.html")
	}
	resolvedRoot, err := canonicalExistingPath(paths.rootDir)
	if err != nil {
		return provisionPaths{}, fmt.Errorf("resolve installation root: %w", err)
	}
	resolvedExecutable, err := canonicalExistingPath(paths.appExecutable)
	if err != nil {
		return provisionPaths{}, fmt.Errorf("resolve App executable: %w", err)
	}
	if !pathWithin(resolvedRoot, resolvedExecutable) {
		return provisionPaths{}, errors.New("resolved App executable must be inside the resolved installation root")
	}
	resolvedStaticDir, err := canonicalExistingPath(paths.staticDir)
	if err != nil {
		return provisionPaths{}, fmt.Errorf("resolve static directory: %w", err)
	}
	if !pathWithin(resolvedRoot, resolvedStaticDir) {
		return provisionPaths{}, errors.New("resolved static directory must be inside the resolved installation root")
	}
	if err := verifyDataPathConfinement(paths.stateDir, paths.dataDir); err != nil {
		return provisionPaths{}, err
	}
	return paths, nil
}

func verifyDataPathConfinement(stateDir, dataDir string) error {
	resolvedState, _, err := resolveExistingPath(stateDir)
	if err != nil {
		return fmt.Errorf("resolve state directory: %w", err)
	}
	if !samePath(stateDir, resolvedState) {
		return errors.New("state directory must not traverse a reparse point")
	}
	resolvedData, _, err := resolveExistingPath(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	if !samePath(dataDir, resolvedData) {
		return errors.New("data directory must not traverse a reparse point")
	}
	if !pathWithin(resolvedState, resolvedData) || samePath(resolvedState, resolvedData) {
		return errors.New("resolved data directory must be a child of the resolved state directory")
	}
	resolvedAttachments, _, err := resolveExistingPath(filepath.Join(dataDir, "attachments"))
	if err != nil {
		return fmt.Errorf("resolve attachments directory: %w", err)
	}
	if !pathWithin(resolvedData, resolvedAttachments) || samePath(resolvedData, resolvedAttachments) {
		return errors.New("resolved attachments directory must be a child of the resolved data directory")
	}
	for _, name := range []string{configName, credentialsName, processMetadataName, "app-lifecycle.lock"} {
		resolvedTarget, _, err := resolveExistingPath(filepath.Join(stateDir, name))
		if err != nil {
			return fmt.Errorf("resolve state file destination %s: %w", name, err)
		}
		if !pathWithin(resolvedState, resolvedTarget) {
			return fmt.Errorf("resolved state file destination %s must be inside the resolved state directory", name)
		}
	}
	return nil
}

func resolveExistingPath(path string) (string, bool, error) {
	resolved, err := canonicalExistingPath(path)
	if err == nil {
		return filepath.Clean(resolved), true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	current := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		resolved, err = canonicalExistingPath(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), false, nil
}

func readPublicEnvelope(path string) (map[string]any, map[string]string, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, map[string]string{}, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("read public production settings: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var envelope map[string]any
	if err := decoder.Decode(&envelope); err != nil {
		return nil, nil, true, fmt.Errorf("decode public production settings: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, nil, true, errors.New("decode public production settings: trailing JSON value")
		}
		return nil, nil, true, fmt.Errorf("decode public production settings: trailing content: %w", err)
	}
	rawEnvironment, ok := envelope["environment"].(map[string]any)
	if !ok {
		return nil, nil, true, errors.New("decode public production settings: environment object is required")
	}
	environment := make(map[string]string, len(rawEnvironment))
	for key, raw := range rawEnvironment {
		value, err := publicValue(raw)
		if err != nil {
			return nil, nil, true, fmt.Errorf("decode public production setting %s: %w", key, err)
		}
		if err := validatePublicSetting(key, value); err != nil {
			return nil, nil, true, err
		}
		environment[key] = value
	}
	return envelope, environment, true, nil
}

func publicValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case json.Number:
		return typed.String(), nil
	default:
		return "", errors.New("value must be a string, number, or boolean")
	}
}

func validatePublicSetting(key, _ string) error {
	if key != strings.ToUpper(key) || !strings.HasPrefix(key, "ASTRORDER_") || strings.HasSuffix(key, "SECRET") {
		return fmt.Errorf("invalid public production setting %q", key)
	}
	return nil
}

func readOrCreateCredentials(path string) (map[string]string, []byte, bool, error) {
	encrypted, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		values := make(map[string]string, len(requiredCredentialKeys))
		for _, key := range requiredCredentialKeys {
			value, err := randomSecret()
			if err != nil {
				return nil, nil, false, err
			}
			values[key] = value
		}
		return values, nil, true, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("read production credentials: %w", err)
	}
	plain, err := credentials.Decrypt(encrypted)
	if err != nil {
		return nil, nil, false, fmt.Errorf("unprotect production credentials: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, nil, false, fmt.Errorf("decode production credentials: %w", err)
	}
	if values == nil {
		return nil, nil, false, errors.New("decode production credentials: credential object is required")
	}
	for key, value := range values {
		if key != strings.ToUpper(key) || !strings.HasPrefix(key, "ASTRORDER_") || value == "" {
			return nil, nil, false, errors.New("decode production credentials: invalid credential entry")
		}
	}
	changed := false
	for _, key := range requiredCredentialKeys {
		if values[key] != "" {
			continue
		}
		value, err := randomSecret()
		if err != nil {
			return nil, nil, false, err
		}
		values[key] = value
		changed = true
	}
	return values, encrypted, changed, nil
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate production credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// SelectSafeLoopbackPort attempts to bind to preferred port; if unavailable, binds to :0 to get an unbound loopback port.
func SelectSafeLoopbackPort(preferred int) (int, error) {
	return selectSafeLoopbackPort(preferred)
}

func selectSafeLoopbackPort(preferred int) (int, error) {
	if preferred > 0 && preferred <= 65535 {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
		if err == nil {
			_ = ln.Close()
			return preferred, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen loopback: %w", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("cannot determine allocated loopback port")
	}
	return addr.Port, nil
}

func validateGRPCTarget(target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil || port == "" {
		return errors.New("gRPC target must be an explicit loopback host:port")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return errors.New("gRPC target port must be between 1 and 65535")
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("gRPC target must use a loopback address")
		}
	}
	return nil
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func requireAbsolute(label, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", label)
	}
	return nil
}
