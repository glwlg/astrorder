package parity

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	CommandPythonAppBaseline = "python-app-baseline-test"
	CommandGoPackageTest     = "go-package-test"
	CommandGoDaemonTest      = "go-daemon-package-test"
	CommandGoAppIsolated     = "go-app-process-isolated"

	GoAppIsolatedTestPath = "app-server-go/internal/acceptance/isolated.go"
	GoAppIsolatedTestName = "GoAppProcessIsolatedV1"
)

var (
	goTestNamePattern     = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)
	pythonTestNamePattern = regexp.MustCompile(`^test_[A-Za-z0-9_]+$`)
	safePathPattern       = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
)

// CommandSpec is a code-owned command definition. Manifest command text is
// validated against this definition and is never evaluated by a shell.
type CommandSpec struct {
	ID            string
	WorkingDir    string
	Argv          []string
	Display       string
	FinalEligible bool
}

// ResolveTestCommand maps a test reference to a fixed command shape. It keeps
// the transitional Python baseline form readable, but marks it ineligible for
// final Go evidence.
func ResolveTestCommand(test TestRef) (CommandSpec, error) {
	if err := validateReferencePath(test.Path); err != nil {
		return CommandSpec{}, err
	}
	switch test.CommandID {
	case CommandPythonAppBaseline:
		if !strings.HasPrefix(test.Path, "backend/tests/") || !strings.HasSuffix(test.Path, ".py") || !pythonTestNamePattern.MatchString(test.Name) {
			return CommandSpec{}, errors.New("Python baseline test path or symbol is outside the approved shape")
		}
		display := "backend/.venv/Scripts/python.exe -m pytest " + test.Path + " -q"
		if test.Command != display {
			return CommandSpec{}, errors.New("Python baseline command does not match the code-owned template")
		}
		return CommandSpec{
			ID: test.CommandID, WorkingDir: ".",
			Argv:    []string{"backend/.venv/Scripts/python.exe", "-m", "pytest", test.Path, "-q"},
			Display: display, FinalEligible: false,
		}, nil
	case CommandGoPackageTest, CommandGoDaemonTest:
		module := "app-server-go"
		if test.CommandID == CommandGoDaemonTest {
			module = "session-daemon-go"
		}
		if !strings.HasPrefix(test.Path, module+"/") || !strings.HasSuffix(test.Path, "_test.go") || !goTestNamePattern.MatchString(test.Name) {
			return CommandSpec{}, errors.New("Go test path or symbol is outside the approved shape")
		}
		modulePath := strings.TrimPrefix(test.Path, module+"/")
		packagePath := path.Dir(modulePath)
		if packagePath == "." || (!strings.HasPrefix(packagePath, "internal/") && !strings.HasPrefix(packagePath, "cmd/")) {
			return CommandSpec{}, errors.New("Go test package is outside the approved module paths")
		}
		packageArg := "./" + packagePath
		display := "cd " + module + " && go test " + packageArg + " -count=1"
		if test.Command != display {
			return CommandSpec{}, errors.New("Go package command does not match the code-owned template")
		}
		return CommandSpec{
			ID: test.CommandID, WorkingDir: module,
			Argv:    []string{"go", "test", packageArg, "-count=1"},
			Display: display, FinalEligible: true,
		}, nil
	case CommandGoAppIsolated:
		if test.Path != GoAppIsolatedTestPath || test.Name != GoAppIsolatedTestName {
			return CommandSpec{}, errors.New("isolated App command must map to its code-owned acceptance definition")
		}
		var argv []string
		if err := json.Unmarshal([]byte(test.Command), &argv); err != nil {
			return CommandSpec{}, errors.New("isolated App command must be a JSON argv array")
		}
		if err := validateIsolatedArgv(argv); err != nil {
			return CommandSpec{}, err
		}
		canonical, _ := json.Marshal(argv)
		if test.Command != string(canonical) {
			return CommandSpec{}, errors.New("isolated App command is not canonical JSON argv")
		}
		return CommandSpec{ID: test.CommandID, WorkingDir: argv[4], Argv: argv, Display: test.Command, FinalEligible: true}, nil
	default:
		return CommandSpec{}, fmt.Errorf("unknown command_id %q", test.CommandID)
	}
}

func validateReferencePath(value string) error {
	if value == "" || filepath.IsAbs(value) || strings.ContainsAny(value, `\\:`) || !safePathPattern.MatchString(value) || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return errors.New("test path must be a clean repository-relative slash path")
	}
	return nil
}

func validateIsolatedArgv(argv []string) error {
	if len(argv) != 11 || argv[1] != "verify" || argv[2] != "--isolated" || argv[3] != "--root" || argv[5] != "--app-executable" || argv[7] != "--output-dir" || argv[9] != "--candidate-revision" {
		return errors.New("isolated App command does not match the code-owned argv template")
	}
	if !filepath.IsAbs(argv[0]) {
		return errors.New("isolated App command requires the exact absolute astrorderctl path")
	}
	base := strings.ToLower(filepath.Base(argv[0]))
	if base != "astrorderctl" && base != "astrorderctl.exe" {
		return errors.New("isolated App command executable must be astrorderctl")
	}
	for _, index := range []int{4, 6, 8} {
		if !filepath.IsAbs(argv[index]) || strings.ContainsRune(argv[index], '\x00') {
			return errors.New("isolated App command paths must be clean absolute paths")
		}
	}
	if !revisionPattern.MatchString(argv[10]) {
		return errors.New("isolated App command candidate revision must be a full lowercase Git SHA")
	}
	return nil
}
