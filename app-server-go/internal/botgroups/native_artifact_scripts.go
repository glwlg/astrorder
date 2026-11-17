package botgroups

import (
	"path"
	"strings"

	"astrorder.dev/app-server/internal/artifacts"
)

// ScriptSources are immutable native patches preceding this command, newest
// first. Reading today's script from disk would not prove its historical code.
type NativeScriptChange struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Diff string `json:"diff"`
}

func commandPathText(command string) string {
	text := strings.ReplaceAll(command, "\\", "/")
	// Native Windows shell wrappers escape backslashes in their display
	// command. Collapse them only for literal matching; retain the original
	// command for the classifier.
	for strings.Contains(text, "//") {
		text = strings.ReplaceAll(text, "//", "/")
	}
	return text
}

func bindNativeScripts(operations []NativeFileOperation, workspace string) []NativeFileOperation {
	for i := range operations {
		command := &operations[i]
		if command.Kind != "commandExecution" {
			continue
		}
		cwd := command.Cwd
		if cwd == "" {
			cwd = workspace
		}
		cwd = artifacts.Normalize(cwd)
		text := commandPathText(command.Command)
		windows := len(cwd) > 2 && cwd[1] == ':'
		if windows {
			text, cwd = strings.ToLower(text), strings.ToLower(cwd)
		}
		// Operations are newest first: only earlier native patches may
		// describe the version of a script this command executed.
		for _, earlier := range operations[i+1:] {
			if earlier.Kind != "fileChange" || earlier.scriptDiff == "" || len(earlier.scriptDiff) > 64*1024 {
				continue
			}
			switch strings.ToLower(path.Ext(artifacts.Normalize(earlier.Path))) {
			case ".py", ".js", ".mjs", ".cjs", ".ts", ".ps1", ".sh", ".bash", ".zsh", ".rb", ".pl":
			default:
				continue
			}
			scriptPath := artifacts.Normalize(earlier.Path)
			if !artifacts.Absolute(scriptPath) {
				scriptPath = artifacts.Normalize(workspace + "/" + scriptPath)
			}
			matched := scriptPath
			if windows {
				matched = strings.ToLower(matched)
			}
			relative := strings.TrimPrefix(matched, strings.TrimRight(cwd, "/")+"/")
			if strings.Contains(text, matched) || relative != matched && strings.Contains(text, relative) {
				command.ScriptSources = append(command.ScriptSources, NativeScriptChange{ID: earlier.ID, Path: scriptPath, Diff: earlier.scriptDiff})
			}
		}
	}
	return operations
}
