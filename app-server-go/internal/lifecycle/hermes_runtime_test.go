package lifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestHermesRuntimeResolvesItsOwnInterpreter(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "hermes")
	python := filepath.Join(root, "python")
	if runtime.GOOS == "windows" {
		launcher += ".exe"
		python += ".exe"
	}
	for _, path := range []string{launcher, python} {
		if err := os.WriteFile(path, []byte("private fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	executable, args, err := resolveHermesRuntime(launcher, func(command string, arguments []string) ([]byte, error) {
		calls = append(calls, command+" "+strings.Join(arguments, " "))
		if command == launcher {
			return nil, errors.New("unsupported launcher flag")
		}
		if command != python || len(arguments) < 2 || arguments[0] != "-I" {
			t.Fatalf("probe escaped Hermes interpreter: %s %v", command, arguments)
		}
		return json.Marshal([]string{python, "-u", "-m", "tui_gateway.entry"})
	})
	if err != nil || executable != python || !reflect.DeepEqual(args, []string{"-u", "-m", "tui_gateway.entry"}) {
		t.Fatalf("wrong Hermes stdio runtime: %q %v %v", executable, args, err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected launcher discovery and module probe, got %d", len(calls))
	}
}

func TestHermesRuntimeHonorsLauncherReportedInterpreter(t *testing.T) {
	root := t.TempDir()
	launcher, python := filepath.Join(root, "hermes"), filepath.Join(root, "managed-python")
	if err := os.WriteFile(python, []byte("private fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	argv := []string{python, "-I", "-c", "native Hermes bootstrap with spaces"}
	executable, arguments, err := resolveHermesRuntime(launcher, func(command string, args []string) ([]byte, error) {
		if command == launcher {
			if !reflect.DeepEqual(args, []string{"--print-runtime-command", "--module", "tui_gateway.entry"}) {
				t.Fatal("discovery did not select gateway module", args)
			}
			return json.Marshal(argv)
		}
		if command != python {
			t.Fatal("ignored launcher-reported interpreter")
		}
		return nil, nil
	})
	if err != nil || executable != python || !reflect.DeepEqual(arguments, argv[1:]) {
		t.Fatal("managed interpreter resolution failed", err)
	}
}

func TestHermesRuntimeUsesInstallationGeneratedBootstrap(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "hermes")
	legacy := filepath.Join(root, "python")
	managed := filepath.Join(root, "managed-python")
	if runtime.GOOS == "windows" {
		legacy += ".exe"
	}
	for _, file := range []string{legacy, managed} {
		if err := os.WriteFile(file, []byte("private fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	argv := []string{managed, "-I", "-c", "native installation bootstrap"}
	executable, args, err := resolveHermesRuntime(launcher, func(command string, arguments []string) ([]byte, error) {
		if command == launcher {
			return nil, errors.New("legacy launcher lacks introspection")
		}
		if command != legacy || !strings.Contains(strings.Join(arguments, " "), "runtime_command") {
			t.Fatal("missing native installation command discovery")
		}
		return json.Marshal(argv)
	})
	if err != nil || executable != managed || !reflect.DeepEqual(args, argv[1:]) {
		t.Fatal("legacy interpreter captured instead of native bootstrap", executable, args, err)
	}
}

func TestHermesRuntimeMissingGatewayIsExplicit(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "hermes")
	python := filepath.Join(root, "python")
	if runtime.GOOS == "windows" {
		python += ".exe"
	}
	if err := os.WriteFile(python, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, err := resolveHermesRuntime(launcher, func(string, []string) ([]byte, error) { return nil, errors.New("gateway absent") })
	if err == nil || !strings.Contains(err.Error(), "Hermes") {
		t.Fatal("missing Hermes gateway silently accepted", err)
	}
}
