package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/process"
)

const maxHelperInputBytes = 16_000_000

func main() {
	// Clean native environment to avoid leaking daemon IPC credentials
	cleanedEnv := process.NativeEnvironment()
	os.Clearenv()
	for _, kv := range cleanedEnv {
		// restore cleaned environment
		if k, v, ok := stringsCut(kv, "="); ok {
			os.Setenv(k, v)
		}
	}

	reader := io.LimitReader(os.Stdin, maxHelperInputBytes+1)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	var req map[string]any
	if err := decoder.Decode(&req); err != nil {
		out, _ := json.Marshal(map[string]any{"ok": false, "detail": "invalid stdin json"})
		fmt.Println(string(out))
		os.Exit(2)
	}

	// Ensure exactly one JSON object and no trailing non-whitespace data
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		out, _ := json.Marshal(map[string]any{"ok": false, "detail": "unexpected trailing data"})
		fmt.Println(string(out))
		os.Exit(2)
	}

	result, err := modelconfig.ExecuteHelper(req)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"ok": false, "detail": err.Error()})
		fmt.Println(string(out))
		os.Exit(2)
	}

	out, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	fmt.Println(string(out))
}

func stringsCut(s, sep string) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
