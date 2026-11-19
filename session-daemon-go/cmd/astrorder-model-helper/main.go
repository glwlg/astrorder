package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/runtime/antigravity"
)

const maxHelperInputBytes = 16_000_000

func main() {
	// Clean native environment to avoid leaking daemon IPC credentials
	cleanedEnv := process.NativeEnvironment()
	os.Clearenv()
	for _, kv := range cleanedEnv {
		if k, v, ok := stringsCut(kv, "="); ok {
			os.Setenv(k, v)
		}
	}

	subcommand := ""
	if len(os.Args) > 1 {
		subcommand = os.Args[1]
		if len(os.Args) > 2 || subcommand != "antigravity" {
			out, _ := json.Marshal(map[string]any{"ok": false, "detail": "unsupported helper subcommand", "before_dispatch": true})
			fmt.Println(string(out))
			os.Exit(2)
		}
	}

	reader := io.LimitReader(os.Stdin, maxHelperInputBytes+1)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	var req map[string]any
	if err := decoder.Decode(&req); err != nil {
		out, _ := json.Marshal(map[string]any{"ok": false, "detail": "invalid stdin json", "before_dispatch": true})
		fmt.Println(string(out))
		os.Exit(2)
	}

	// Ensure exactly one JSON object and no trailing non-whitespace data
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		out, _ := json.Marshal(map[string]any{"ok": false, "detail": "unexpected trailing data", "before_dispatch": true})
		fmt.Println(string(out))
		os.Exit(2)
	}

	domain, _ := req["domain"].(string)
	if subcommand == "antigravity" || domain == "antigravity" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		result, err := antigravity.ExecuteHelper(ctx, req)
		cancel()
		if err != nil {
			resp := map[string]any{"ok": false, "detail": err.Error()}
			var rhe *antigravity.RemoteHelperError
			if errors.As(err, &rhe) && rhe.BeforeDispatch {
				resp["before_dispatch"] = true
			}
			out, _ := json.Marshal(resp)
			fmt.Println(string(out))
			os.Exit(2)
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		fmt.Println(string(out))
		return
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
