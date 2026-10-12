package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type runtimeProbe func(string, []string) ([]byte, error)

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
