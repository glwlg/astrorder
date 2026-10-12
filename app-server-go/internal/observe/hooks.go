package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// HookMarker is the statusMessage Astrorder puts on every hook it owns. Only
// handlers carrying it are ever rewritten.
const HookMarker = "Astrorder passive observation"

// PluginID is the Codex plugin that carries Astrorder's hooks.
const PluginID = "astrorder@astrorder-local"

// HookCommand returns the shell command a Codex hook runs. Codex runs hooks
// through PowerShell on Windows and a POSIX shell elsewhere, and a quoted
// executable followed by arguments does not parse in PowerShell. So a path made
// only of safe characters is written bare, which both shells accept; other
// paths are single-quoted on POSIX and rejected on Windows rather than guessed.
func HookCommand(helper, spool string) (string, error) {
	helper, spool = filepath.ToSlash(helper), filepath.ToSlash(spool)
	if helper == "" || spool == "" || strings.ContainsAny(helper+spool, "\x00\r\n") {
		return "", errors.New("hook command paths are invalid")
	}
	if safeShellWord(helper) && safeShellWord(spool) {
		return helper + " observe --spool " + spool, nil
	}
	if runtime.GOOS == "windows" {
		return "", errors.New("hook command paths contain characters that PowerShell and Bash parse differently")
	}
	return shellQuote(helper) + " observe --spool " + shellQuote(spool), nil
}

func safeShellWord(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) && r < unicode.MaxASCII || unicode.IsDigit(r) && r < unicode.MaxASCII {
			continue
		}
		if !strings.ContainsRune(":/._-", r) {
			return false
		}
	}
	return value != ""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// HookFiles lists the hooks.json files that carry Astrorder's hooks: every
// installed plugin-cache version, plus the marketplace source they came from.
func HookFiles(home string) []string {
	files, _ := filepath.Glob(filepath.Join(home, "plugins", "cache", "astrorder-local", "astrorder", "*", "hooks", "hooks.json"))
	source := filepath.Join(home, "astrorder-observer", "marketplace", "plugins", "astrorder", "hooks", "hooks.json")
	if _, err := os.Stat(source); err == nil {
		files = append(files, source)
	}
	return files
}

// RewriteResult reports what RewriteHooks changed.
type RewriteResult struct {
	Files    int `json:"files"`
	Handlers int `json:"handlers"`
}

// RewriteHooks points every Astrorder hook handler at command. Files that are
// already current are left untouched; changed files are backed up first and
// replaced atomically. It never touches handlers without HookMarker.
func RewriteHooks(home, command string) (RewriteResult, error) {
	files := HookFiles(home)
	if len(files) == 0 {
		return RewriteResult{}, errors.New("Astrorder Codex plugin hooks were not found; install the plugin first")
	}
	var result RewriteResult
	for index, path := range files {
		changed, err := rewriteHookFile(home, path, command, index)
		if err != nil {
			return result, fmt.Errorf("%s: %w", filepath.Base(filepath.Dir(filepath.Dir(path))), err)
		}
		if changed > 0 {
			result.Files++
			result.Handlers += changed
		}
	}
	return result, nil
}

func rewriteHookFile(home, path, command string, index int) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return 0, fmt.Errorf("hooks.json is invalid: %w", err)
	}
	events, _ := document["hooks"].(map[string]any)
	changed := 0
	for _, groups := range events {
		list, _ := groups.([]any)
		for _, group := range list {
			entry, _ := group.(map[string]any)
			handlers, _ := entry["hooks"].([]any)
			for _, handler := range handlers {
				item, _ := handler.(map[string]any)
				if item == nil || item["statusMessage"] != HookMarker {
					continue
				}
				if current, _ := item["command"].(string); current != command {
					item["command"] = command
					changed++
				}
			}
		}
	}
	if changed == 0 {
		return 0, nil
	}
	backupDir := filepath.Join(home, "astrorder-observer", "hooks-backup")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return 0, err
	}
	backup := filepath.Join(backupDir, fmt.Sprintf("%d-%d-hooks.json", time.Now().UnixNano(), index))
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return 0, err
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return 0, err
	}
	temporary := path + ".astrorder.tmp"
	if err := os.WriteFile(temporary, encoded.Bytes(), 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	return changed, nil
}
