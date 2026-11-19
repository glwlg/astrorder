package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestHelperBinaryEntryPoint(t *testing.T) {
	if os.Getenv("ASTRORDER_MODEL_HELPER_SUBPROC") != "1" {
		return
	}
	os.Args = append([]string{os.Args[0]}, helperExtraArgs()...)
	main()
	os.Exit(0)
}

func helperExtraArgs() []string {
	if sub := os.Getenv("ASTRORDER_MODEL_HELPER_SUBCMD"); sub != "" {
		return []string{sub}
	}
	return nil
}

func runHelperSubproc(t *testing.T, home, subcmd, stdinJSON string) (map[string]any, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestHelperBinaryEntryPoint")
	cmd.Env = append(os.Environ(),
		"ASTRORDER_MODEL_HELPER_SUBPROC=1",
		"ASTRORDER_MODEL_HELPER_SUBCMD="+subcmd,
		"HOME="+home,
		"USERPROFILE="+home,
	)
	cmd.Stdin = bytes.NewBufferString(stdinJSON)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("helper subprocess run error: %v", err)
		}
	}
	var parsed map[string]any
	if decErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &parsed); decErr != nil {
		t.Fatalf("invalid helper JSON stdout %q: %v", out.String(), decErr)
	}
	return parsed, code
}

func TestModelHelperBackwardCompatibilityAndAntigravitySubcommand(t *testing.T) {
	home := t.TempDir()

	// 1. Legacy modelconfig mode (no subcommand) must remain 100% compatible.
	planResp, code := runHelperSubproc(t, home, "", `{"action":"plan"}`)
	if code != 0 || planResp["ok"] != true {
		t.Fatalf("legacy modelconfig plan failed (code=%d): %+v", code, planResp)
	}

	// 2. Unknown subcommand rejected with before_dispatch=true.
	badSub, code := runHelperSubproc(t, home, "unknown-sub", `{}`)
	if code == 0 || badSub["ok"] != false || badSub["before_dispatch"] != true {
		t.Fatalf("expected unknown subcommand rejection: code=%d %+v", code, badSub)
	}

	// 3. Antigravity subcommand: session.list, delete_unbound, and strict rejection of loopback native_rpc.
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(cliDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cid := "77777777-7777-4777-8777-777777777777"
	db, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE conversation_summaries(conversation_id TEXT,title TEXT,last_modified_time TEXT,workspace_uris TEXT,not_fully_idle NUMERIC,killed NUMERIC);
		INSERT INTO conversation_summaries VALUES(?,'Helper Title','2026-11-17 16:00:00Z','["file:///home/luwei/ws"]',0,0)`, cid); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	listReq, _ := json.Marshal(map[string]any{"action": "session.list", "agent_id": "ssh-antigravity-wsl", "connection_id": "wsl"})
	listResp, code := runHelperSubproc(t, home, "antigravity", string(listReq))
	if code != 0 || listResp["ok"] != true {
		t.Fatalf("antigravity session.list failed (code=%d): %+v", code, listResp)
	}
	items := listResp["result"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != cid {
		t.Fatalf("unexpected session.list result: %+v", listResp)
	}

	// Loopback native_rpc is permanently removed for Google ACP compliance.
	badRPCReq, _ := json.Marshal(map[string]any{
		"action":          "native_rpc",
		"conversation_id": cid,
		"method":          "GetUserStatus",
	})
	badRPCResp, code := runHelperSubproc(t, home, "antigravity", string(badRPCReq))
	if code == 0 || badRPCResp["before_dispatch"] != true {
		t.Fatalf("expected native_rpc compliance rejection before dispatch: code=%d %+v", code, badRPCResp)
	}

	delReq, _ := json.Marshal(map[string]any{"action": "delete_unbound", "conversation_id": cid})
	delResp, code := runHelperSubproc(t, home, "antigravity", string(delReq))
	if code != 0 || delResp["ok"] != true {
		t.Fatalf("antigravity delete_unbound failed (code=%d): %+v", code, delResp)
	}
}
