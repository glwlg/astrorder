package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

type resolverFixture struct {
	targets []Target
	err     error
}

func (r resolverFixture) ResolveTargets(context.Context) ([]Target, error) { return r.targets, r.err }
func (r resolverFixture) SelectTargets(_ context.Context, ids []string) ([]Target, error) {
	byID := make(map[string]Target, len(r.targets))
	for _, target := range r.targets {
		byID[target.ID] = target
	}
	selected := make([]Target, 0, len(ids))
	for _, id := range ids {
		target, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("target %q does not exist", id)
		}
		selected = append(selected, target)
	}
	return selected, r.err
}

type memoryFS struct {
	mu             sync.Mutex
	files          map[string][]byte
	corruptRead    map[string]bool
	failWriteAfter map[string]int
	writes         map[string]int
}

func newMemoryFS() *memoryFS {
	return &memoryFS{files: map[string][]byte{}, corruptRead: map[string]bool{}, failWriteAfter: map[string]int{}, writes: map[string]int{}}
}

func (f *memoryFS) key(target Target, filename string) string {
	return target.ID + ":" + filepath.ToSlash(filename)
}
func (f *memoryFS) ReadFile(_ context.Context, target Target, filename string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(target, filename)
	raw, ok := f.files[key]
	if !ok {
		return nil, ErrTargetFileNotFound
	}
	if f.corruptRead[key] && f.writes[key] == 1 {
		return []byte(`{"url":"corrupt"}`), nil
	}
	return append([]byte(nil), raw...), nil
}
func (f *memoryFS) WriteFileAtomic(_ context.Context, target Target, filename string, raw []byte, _ uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(target, filename)
	f.writes[key]++
	if threshold := f.failWriteAfter[key]; threshold > 0 && f.writes[key] >= threshold {
		return errors.New("fixture write failure")
	}
	f.files[key] = append([]byte(nil), raw...)
	return nil
}

type runnerFixture struct {
	mu            sync.Mutex
	commands      []NativeCommand
	failFor       string
	hermes        map[string]string
	codexVersion  string
	codexFeatures string
}

func (r *runnerFixture) Run(_ context.Context, command NativeCommand) (NativeCommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	joined := command.Name + " " + strings.Join(command.Args, " ")
	fixtureSecret := strings.Join([]string{"fixture", "secret"}, "-")
	if r.failFor != "" && strings.Contains(joined, r.failFor) {
		return NativeCommandResult{Stderr: "failed with " + fixtureSecret, ExitCode: 7}, nil
	}
	if command.Name == "hermes" {
		if r.hermes == nil {
			r.hermes = map[string]string{}
		}
		commandArgs := command.Args
		if len(commandArgs) >= 2 && commandArgs[0] == "--profile" {
			commandArgs = commandArgs[2:]
		}
		if len(commandArgs) >= 4 && commandArgs[0] == "config" && commandArgs[1] == "set" {
			r.hermes[command.Target.ID+":"+commandArgs[2]] = commandArgs[3]
		}
		switch {
		case len(commandArgs) >= 3 && commandArgs[0] == "plugins" && commandArgs[1] == "list":
			return NativeCommandResult{Stdout: `[{"name":"openviking","status":"enabled"}]`}, nil
		case len(commandArgs) >= 4 && commandArgs[0] == "config" && commandArgs[1] == "get":
			return NativeCommandResult{Stdout: fmt.Sprintf("%q\n", r.hermes[command.Target.ID+":"+commandArgs[2]])}, nil
		}
	}
	if command.Name == "codex" {
		switch strings.Join(command.Args, " ") {
		case "--version":
			version := r.codexVersion
			if version == "" {
				version = "0.161.0"
			}
			return NativeCommandResult{Stdout: "codex-cli " + version}, nil
		case "features list":
			features := r.codexFeatures
			if features == "" {
				features = "plugins stable true\nhooks stable true\nplugin_hooks removed false\n"
			}
			return NativeCommandResult{Stdout: features}, nil
		case "plugin list --json":
			return NativeCommandResult{Stdout: `{"installed":[{"name":"openviking-memory","pluginId":"openviking-memory@openviking","enabled":true}],"available":[]}`}, nil
		}
	}
	return NativeCommandResult{}, nil
}

func TestTargetSyncHermesUsesNativeCLIWithExactProfileAndSecretArg(t *testing.T) {
	t.Parallel()
	target := Target{ID: "ssh:7", Name: "Remote Hermes", Kind: TargetSSH, Agents: []string{TargetAppHermes}, Home: "/home/fixture", HermesProfile: "team-red"}
	runner := &runnerFixture{}
	syncer, err := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{target}}, FS: newMemoryFS(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	fixtureSecret := strings.Join([]string{"fixture", "secret"}, "-")
	result, err := syncer.Apply(context.Background(), Config{URL: "https://ov.example.test:1933", APIKey: fixtureSecret})
	if err != nil || result.Applied != 1 || result.Failed != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var observed []string
	for _, command := range runner.commands {
		observed = append(observed, strings.Join(command.Args, " "))
		if len(command.Env) != 0 {
			t.Fatalf("unexpected environment: %#v", command.Env)
		}
	}
	joined := strings.Join(observed, "\n")
	for _, expected := range []string{
		"--profile team-red plugins install " + HermesOpenVikingPluginSource + " --ref " + HermesOpenVikingPluginRef + " --no-enable",
		"--profile team-red plugins enable openviking",
		"--profile team-red config set memory.provider openviking",
		"--profile team-red config set OPENVIKING_ENDPOINT https://ov.example.test:1933",
		"--profile team-red config set OPENVIKING_API_KEY " + fixtureSecret,
		"--profile team-red plugins list --json",
		"--profile team-red config get OPENVIKING_ENDPOINT --json --raw",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing command %q in\n%s", expected, joined)
		}
	}
}

func TestTargetSyncCodexPreservesUnknownConfigAndInspectsEffectiveState(t *testing.T) {
	t.Parallel()
	home := filepath.ToSlash(t.TempDir())
	target := Target{ID: "local", Name: "Local Codex", Kind: TargetLocal, Agents: []string{TargetAppCodex}, Home: home}
	fixtureFS := newMemoryFS()
	filename := filepath.ToSlash(filepath.Join(home, ".openviking", "ovcli.conf"))
	fixtureFS.files[fixtureFS.key(target, filename)] = []byte(`{"url":"http://old","api_key":"old","future":{"large":9007199254740993}}`)
	runner := &runnerFixture{}
	syncer, err := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{target}}, FS: fixtureFS, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	config := Config{URL: "https://next.example.test", APIKey: "fixture-secret"}
	result, err := syncer.Apply(context.Background(), config)
	if err != nil || result.Applied != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	raw, _ := fixtureFS.ReadFile(context.Background(), target, filename)
	if !strings.Contains(string(raw), `"large": 9007199254740993`) || verifyJSONFields(raw, config) != nil {
		t.Fatalf("effective config = %s", raw)
	}
	var commands []string
	for _, command := range runner.commands {
		commands = append(commands, command.Name+" "+strings.Join(command.Args, " "))
	}
	if !containsExact(commands, "codex plugin marketplace add "+CodexOpenVikingMarketplace) || !containsExact(commands, "codex plugin add "+CodexOpenVikingPlugin) {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestTargetSyncRollbackAndPartialFailureAreVisibleAndSanitized(t *testing.T) {
	t.Parallel()
	good := Target{ID: "local", Kind: TargetLocal, Agents: []string{TargetAppCodex}, Home: "/fixture/good"}
	bad := Target{ID: "wsl:fixture", Kind: TargetWSL, Agents: []string{TargetAppCodex}, Home: "/fixture/bad"}
	fixtureFS := newMemoryFS()
	for _, target := range []Target{good, bad} {
		filename := filepath.ToSlash(filepath.Join(target.Home, ".openviking", "ovcli.conf"))
		fixtureFS.files[fixtureFS.key(target, filename)] = []byte(`{"url":"http://old","api_key":"old","keep":true}`)
	}
	badFilename := filepath.ToSlash(filepath.Join(bad.Home, ".openviking", "ovcli.conf"))
	badKey := fixtureFS.key(bad, badFilename)
	fixtureFS.corruptRead[badKey] = true
	syncer, _ := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{good, bad}}, FS: fixtureFS, Runner: &runnerFixture{}})
	result, err := syncer.Apply(context.Background(), Config{URL: "https://new.test", APIKey: "fixture-secret"})
	if err != nil || result.Applied != 1 || result.Failed != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, status := range result.Targets {
		if strings.Contains(status.Detail, "fixture-secret") {
			t.Fatalf("secret leaked: %#v", status)
		}
	}
	raw, _ := fixtureFS.ReadFile(context.Background(), bad, badFilename)
	if !strings.Contains(string(raw), `"keep":true`) || fixtureFS.writes[badKey] != 2 {
		t.Fatalf("rollback raw=%s writes=%d", raw, fixtureFS.writes[badKey])
	}
}

func TestTargetSyncUnknownTargetKindFailsExplicitly(t *testing.T) {
	t.Parallel()
	syncer, _ := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{{ID: "mystery", Kind: "alternate", Agents: []string{TargetAppCodex}, Home: "/x"}}}, FS: newMemoryFS(), Runner: &runnerFixture{}})
	_, err := syncer.Apply(context.Background(), Config{URL: "http://localhost:1933"})
	if err == nil || !strings.Contains(err.Error(), "unsupported memory target kind") {
		t.Fatalf("error = %v", err)
	}
}

func TestCodexConfigUsesAdvertisedHookFieldAndPreservesUnknownTOML(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		version    string
		features   string
		featureKey string
	}{
		{"current hooks", "0.161.0", "hooks stable true\nplugin_hooks removed false\n", "hooks"},
		{"legacy plugin hooks", "0.120.0", "plugin_hooks experimental false\n", "plugin_hooks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := Target{ID: "local", Kind: TargetLocal, Agents: []string{TargetAppCodex}, Home: "/private"}
			fixtureFS := newMemoryFS()
			filename := "/private/.codex/config.toml"
			fixtureFS.files[fixtureFS.key(target, filename)] = []byte("model = \"gpt-fixture\"\n\n[features]\nother = true\n\n[unrelated]\nlarge = 9007199254740993\n")
			runner := &runnerFixture{codexVersion: tc.version, codexFeatures: tc.features}
			syncer, _ := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{target}}, FS: fixtureFS, Runner: runner})
			if err := syncer.configureCodexHooks(context.Background(), target, Config{}, tc.version); err != nil {
				t.Fatal(err)
			}
			raw, _ := fixtureFS.ReadFile(context.Background(), target, filename)
			if !hasTOMLBoolean(raw, "features", tc.featureKey, true) || !strings.Contains(string(raw), "other = true") || !strings.Contains(string(raw), "large = 9007199254740993") {
				t.Fatalf("config = %s", raw)
			}
		})
	}
}

func TestHermesInspectionUsesDocumentedPluginStatusJSON(t *testing.T) {
	t.Parallel()
	target := Target{ID: "local", Kind: TargetLocal, Agents: []string{TargetAppHermes}}
	fixtureSecret := strings.Join([]string{"fixture", "secret"}, "-")
	runner := &runnerFixture{hermes: map[string]string{
		"local:memory.provider": "openviking", "local:OPENVIKING_ENDPOINT": "https://ov.test", "local:OPENVIKING_API_KEY": fixtureSecret,
	}}
	syncer, _ := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{target}}, FS: newMemoryFS(), Runner: runner})
	state := syncer.inspectHermesState(context.Background(), target, Config{URL: "https://ov.test", APIKey: fixtureSecret})
	if state.Error != nil || state.Status != "configured" || state.Endpoint != "https://ov.test" {
		t.Fatalf("state = %#v", state)
	}
}

func TestTargetInspectionAggregatesMachineAgents(t *testing.T) {
	t.Parallel()
	target := Target{ID: "local", Kind: TargetLocal, Agents: []string{TargetAppCodex, TargetAppHermes}, Home: "/private"}
	fixtureFS := newMemoryFS()
	config := Config{URL: "https://ov.test", APIKey: strings.Join([]string{"fixture", "secret"}, "-")}
	fixtureSecret := strings.Join([]string{"fixture", "secret"}, "-")
	fixtureFS.files[fixtureFS.key(target, "/private/.openviking/ovcli.conf")] = []byte(`{"url":"https://ov.test","api_key":"` + fixtureSecret + `"}`)
	fixtureFS.files[fixtureFS.key(target, "/private/.codex/config.toml")] = []byte("[features]\nhooks = true\n")
	syncer, _ := NewSyncer(SyncerOptions{Resolver: resolverFixture{targets: []Target{target}}, FS: fixtureFS, Runner: &runnerFixture{hermes: map[string]string{
		"local:memory.provider": "openviking", "local:OPENVIKING_ENDPOINT": "https://ov.test", "local:OPENVIKING_API_KEY": fixtureSecret,
	}}})
	statuses, err := syncer.Inspect(context.Background(), config)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses=%#v err=%v", statuses, err)
	}
	status := statuses[0]
	if status.OVCLIStatus != "configured" || status.TargetURL != config.URL || !status.HooksEnabled || !status.PluginInstalled || status.HermesStatus != "configured" || status.HermesEndpoint != config.URL || status.RawOutput != nil {
		t.Fatalf("status = %#v", status)
	}
}

func TestLocalTargetFSAtomicReadWriteOnPrivateHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := Target{ID: "local", Kind: TargetLocal, Home: home}
	filename := filepath.Join(home, ".openviking", "ovcli.conf")
	local := LocalTargetFS{}
	if err := local.WriteFileAtomic(context.Background(), target, filename, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := local.WriteFileAtomic(context.Background(), target, filename, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := local.ReadFile(context.Background(), target, filename)
	if err != nil || string(raw) != "second" {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestCompareSemver(t *testing.T) {
	t.Parallel()
	values := []string{"0.113.9", "0.114.0", "0.125.0", "0.161.0", "1.0.0"}
	sorted := append([]string(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return compareSemver(sorted[i], sorted[j]) < 0 })
	if !reflect.DeepEqual(values, sorted) {
		t.Fatalf("sorted = %#v", sorted)
	}
}

func containsExact(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
