package botgroups

import (
	"encoding/json"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/artifacts"
)

func TestExecutedNativeScriptCarriesItsHistoricalWriteCode(t *testing.T) {
	root := "C:/work"
	script := `C:\work\tmp\generate.py`
	code := "+ROOT = Path(r'C:\\work\\docs')\n+image = ROOT / '架构图.png'\n+shutil.copy2(generated, image)\n+print('Copied architecture image')"
	command := `"C:\\Python\\python.exe" "C:\\work\\tmp\\generate.py"`
	execution := nativeFileOperations(map[string]any{
		"id": "run-script", "type": "commandExecution", "status": "failed", "exitCode": json.Number("1"),
		"command": command, "cwd": `C:\work`, "aggregatedOutput": "Copied architecture image\nLater render step failed",
	})
	patch := nativeFileOperations(map[string]any{
		"id": "create-script", "type": "fileChange", "status": "completed",
		"changes": []any{map[string]any{"path": script, "kind": map[string]any{"type": "add"}, "diff": code}},
	})
	if len(execution) != 1 || execution[0].ExitCode == nil || *execution[0].ExitCode != 1 {
		t.Fatalf("lost aggregate failure and native substep receipt: %+v", execution)
	}
	operations := bindNativeScripts(append(execution, patch...), root)
	if len(operations[0].ScriptSources) != 1 || operations[0].ScriptSources[0].ID != "create-script" {
		t.Fatalf("script execution lost its original native code: %+v", operations[0])
	}
	file := artifacts.Reference(artifacts.File{Workspace: root}, "docs/架构图.png")
	matches := evidenceForFile(file, operations)
	if len(matches) != 1 || matches[0].ID != "run-script" || !strings.Contains(matches[0].Output, "Later render step failed") {
		t.Fatalf("script output evidence missing: %+v", matches)
	}
	// A patch alone is not proof that the script was executed.
	if got := evidenceForFile(file, patch); len(got) != 0 {
		t.Fatalf("unexecuted script became write evidence: %+v", got)
	}
	// A script edited after execution cannot establish what ran earlier.
	reversed := bindNativeScripts(append(patch, execution...), root)
	if len(reversed[1].ScriptSources) != 0 {
		t.Fatal("a later script edit was used as historical execution evidence")
	}
}

func TestWindowsEscapedCommandKeepsItsExplicitOutputPath(t *testing.T) {
	file := artifacts.Reference(artifacts.File{Workspace: "C:/work"}, "docs/交接.md")
	operations := []NativeFileOperation{{ID: "handoff", Kind: "commandExecution",
		Cwd: `C:\work`, Command: `[IO.File]::WriteAllText("C:\\work\\docs\\交接.md", $body)`}}
	matches := evidenceForFile(file, operations)
	if len(matches) != 1 || matches[0].ID != "handoff" {
		t.Fatalf("native shell escaping hid a literal output path: %+v", matches)
	}
}

func TestExactNativeChangesAvoidUnrelatedCommandOutput(t *testing.T) {
	file := artifacts.Reference(artifacts.File{Workspace: "/work"}, "docs/result.md")
	operations := []NativeFileOperation{
		{ID: "check", Kind: "commandExecution", Command: "cat /work/docs/result.md", Output: strings.Repeat("reference content", 10000)},
		{ID: "patch", Kind: "fileChange", Path: "docs/result.md"},
	}
	matches := evidenceForFile(file, operations)
	if len(matches) != 1 || matches[0].ID != "patch" {
		t.Fatalf("incidental commands diluted explicit write evidence: %+v", matches)
	}
	success := nativeFileOperations(map[string]any{
		"id": "test", "type": "commandExecution", "status": "completed", "exitCode": json.Number("0"),
		"command": "check docs/result.md", "aggregatedOutput": strings.Repeat("test output", 10000),
	})
	if len(success) != 1 || success[0].Output != "" {
		t.Fatal("successful read/test output bloated the classifier request")
	}
}
