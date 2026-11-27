package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type runtimeProbe func(string, []string) ([]byte, error)

// HermesRuntimeStatus records whether a local Hermes installation passed
// gateway probing without blocking overall provisioning or client startup.
type HermesRuntimeStatus struct {
	Available  bool     `json:"available"`
	Launcher   string   `json:"launcher,omitempty"`
	Executable string   `json:"executable,omitempty"`
	Arguments  []string `json:"arguments,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	CheckedAt  string   `json:"checked_at,omitempty"`
}

var (
	hermesStatusMu   sync.RWMutex
	lastHermesStatus *HermesRuntimeStatus
)

// LastHermesRuntimeStatus returns a copy of the most recent Hermes runtime probe result.
func LastHermesRuntimeStatus() *HermesRuntimeStatus {
	hermesStatusMu.RLock()
	defer hermesStatusMu.RUnlock()
	if lastHermesStatus == nil {
		return nil
	}
	cp := *lastHermesStatus
	cp.Arguments = append([]string(nil), lastHermesStatus.Arguments...)
	return &cp
}

func recordHermesRuntimeStatus(st HermesRuntimeStatus) {
	cp := st
	cp.Arguments = append([]string(nil), st.Arguments...)
	hermesStatusMu.Lock()
	lastHermesStatus = &cp
	hermesStatusMu.Unlock()
}

// ProbeHermesRuntime probes a discovered Hermes launcher fault-tolerantly.
// A damaged or incomplete Hermes installation never aborts provisioning or
// startup: it logs a structured warning and returns Available=false so all
// other engines and the workbench continue starting normally.
func ProbeHermesRuntime(launcher string, probe runtimeProbe, logger *slog.Logger) HermesRuntimeStatus {
	if logger == nil {
		logger = slog.Default()
	}
	if probe == nil {
		probe = probeNativeRuntime
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	trimmed := strings.TrimSpace(launcher)
	if trimmed == "" {
		st := HermesRuntimeStatus{
			Available: false,
			Reason:    "Hermes launcher path is empty",
			CheckedAt: checkedAt,
		}
		recordHermesRuntimeStatus(st)
		logger.Warn("hermes runtime unavailable; marking hermes disabled",
			"component", "lifecycle.hermes_runtime",
			"status", "unavailable",
			"reason", st.Reason,
		)
		return st
	}
	executable, arguments, err := resolveHermesRuntime(trimmed, probe)
	if err != nil {
		st := HermesRuntimeStatus{
			Available: false,
			Launcher:  trimmed,
			Reason:    err.Error(),
			CheckedAt: checkedAt,
		}
		recordHermesRuntimeStatus(st)
		logger.Warn("hermes runtime unavailable; skipping hermes so provisioning and other engines continue",
			"component", "lifecycle.hermes_runtime",
			"launcher", trimmed,
			"status", "unavailable",
			"error", err.Error(),
		)
		return st
	}
	st := HermesRuntimeStatus{
		Available:  true,
		Launcher:   trimmed,
		Executable: executable,
		Arguments:  arguments,
		CheckedAt:  checkedAt,
	}
	recordHermesRuntimeStatus(st)
	return st
}

func probeNativeRuntime(executable string, arguments []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = ProcessEnvironment(nil)
	command.Stdin = strings.NewReader("")
	command.Stderr = nil
	if runtime.GOOS == "windows" {
		if err := configureIndependentProcess(command); err != nil {
			return nil, err
		}
	}
	return command.Output()
}

// Resolve only the interpreter owned by Hermes. Never select a system Python
// or the removed Astrorder backend interpreter.
func resolveHermesRuntime(launcher string, probe runtimeProbe) (string, []string, error) {
	var candidates []string
	if data, err := probe(launcher, []string{"--print-runtime-command", "--module", "tui_gateway.entry"}); err == nil {
		if executable, arguments, valid := hermesRuntimeCommand(data); valid {
			return executable, arguments, nil
		}
	}
	name := "python"
	if runtime.GOOS == "windows" {
		name = "python.exe"
	}
	if resolved, err := filepath.EvalSymlinks(launcher); err == nil {
		launcher = resolved
	}
	directory := filepath.Dir(launcher)
	candidates = append(candidates, filepath.Join(directory, name), filepath.Join(directory, "venv", "bin", name), filepath.Join(filepath.Dir(directory), "hermes-agent", "venv", "bin", name))
	if runtime.GOOS == "windows" {
		candidates = append(candidates, filepath.Join(directory, "venv", "Scripts", name), filepath.Join(filepath.Dir(directory), "hermes-agent", "venv", "Scripts", name))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if info, err := os.Stat(candidate); err != nil || !info.Mode().IsRegular() {
			continue
		}
		// Hermes owns dependency selection and the Python ABI. A legacy venv
		// can discover its installation but must not execute a newer gateway
		// against the selected environment's incompatible native extensions.
		discovery := "import importlib.util, json, pathlib, sys; " +
			"spec = importlib.util.find_spec('tui_gateway.entry'); " +
			"assert spec is not None; root = pathlib.Path(spec.origin).parents[1]; " +
			"sys.path.insert(0, str(root)); " +
			"from hermes_cli._launchers import runtime_command; " +
			"print(json.dumps(runtime_command(root, module='tui_gateway.entry')))"
		data, err := probe(candidate, []string{"-I", "-c", discovery})
		if err != nil {
			continue
		}
		if executable, arguments, valid := hermesRuntimeCommand(data); valid {
			return executable, arguments, nil
		}
	}
	return "", nil, fmt.Errorf("Hermes stdio gateway is unavailable in its installed runtime; repair the Hermes installation")
}

func hermesRuntimeCommand(data []byte) (string, []string, bool) {
	var argv []string
	if json.Unmarshal(data, &argv) != nil || len(argv) < 2 || !filepath.IsAbs(argv[0]) {
		return "", nil, false
	}
	for _, argument := range argv {
		if argument == "" || strings.ContainsRune(argument, 0) {
			return "", nil, false
		}
	}
	if info, err := os.Stat(argv[0]); err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}
	return argv[0], argv[1:], true
}
