package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestUsageSummaryAggregatesStepsBySlotAndCachesIncrementally(t *testing.T) {
	home := t.TempDir()
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	if err := os.MkdirAll(convDir, 0o700); err != nil {
		t.Fatal(err)
	}

	id := "66666666-6666-4666-8666-666666666666"
	sumDB, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sumDB.Exec(`
		CREATE TABLE conversation_summaries(conversation_id TEXT PRIMARY KEY, title TEXT, workspace_uris TEXT, killed INTEGER);
		INSERT INTO conversation_summaries VALUES(?, 'A2 操作审计迁移', '["file:///home/fixture/workspace/ExampleProject"]', 0);
	`, id); err != nil {
		t.Fatal(err)
	}
	sumDB.Close()

	logsDir := filepath.Join(cliDir, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tLine1, _ := json.Marshal(map[string]any{"step_index": 1, "type": "PLANNER_RESPONSE", "model": "gemini-4-carbon"})
	tLine2, _ := json.Marshal(map[string]any{"step_index": 2, "type": "PLANNER_RESPONSE", "model": "gemini-4-carbon-high"})
	if err := os.WriteFile(filepath.Join(logsDir, "transcript.jsonl"), append(append(tLine1, '\n'), append(tLine2, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}

	day1 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2026, 11, 18, 8, 30, 0, 0, time.UTC).Unix()

	makeStepMeta := func(unixSec int64, inTok, outTok, cacheRead, thinking uint64) []byte {
		ts := pbVarint(1, uint64(unixSec))
		u := append(pbVarint(2, inTok), pbVarint(3, outTok)...)
		u = append(u, pbVarint(5, cacheRead)...)
		u = append(u, pbVarint(10, thinking)...)
		return append(pbBytes(1, ts), pbBytes(9, u)...)
	}

	convDB, err := sql.Open("sqlite", filepath.Join(convDir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := convDB.Exec(`
		CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB);
		CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := convDB.Exec(`INSERT INTO steps VALUES(1,15,3,0,?,?)`, pbVarint(1, 15), makeStepMeta(day1, 2000, 300, 8000, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := convDB.Exec(`INSERT INTO steps VALUES(2,15,3,0,?,?)`, pbVarint(1, 15), makeStepMeta(day2, 5000, 700, 15000, 250)); err != nil {
		t.Fatal(err)
	}
	convDB.Close()

	// 1. All-time summary includes both steps across both models without double counting.
	allRes, err := ExecuteHelper(context.Background(), map[string]any{
		"domain": "antigravity",
		"action": "usage_summary",
		"home":   home,
	})
	if err != nil {
		t.Fatal(err)
	}
	slots := normalizeMapSlice(allRes["slots"])
	if len(slots) != 2 {
		t.Fatalf("expected 2 model/time slots, got %+v", slots)
	}
	sessions := normalizeMapSlice(allRes["sessions"])
	if len(sessions) != 1 || sessions[0]["title"] != "A2 操作审计迁移" || sessions[0]["total"] != int64(31000) || sessions[0]["calls"] != int64(2) {
		t.Fatalf("unexpected session summary: %+v", sessions)
	}

	// 2. Filtered summary (only day 2) uses cached buckets and returns only day 2's 20,700 tokens!
	sinceDay2 := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC).UnixMilli()
	day2Res, err := ExecuteHelper(context.Background(), map[string]any{
		"domain":   "antigravity",
		"action":   "usage_summary",
		"home":     home,
		"since_ms": sinceDay2,
	})
	if err != nil {
		t.Fatal(err)
	}
	day2Slots := normalizeMapSlice(day2Res["slots"])
	if len(day2Slots) != 1 || day2Slots[0]["model"] != "gemini-4-carbon-high" {
		t.Fatalf("expected only day 2 gemini-4-carbon-high slot, got %+v", day2Slots)
	}
	day2Sessions := normalizeMapSlice(day2Res["sessions"])
	if len(day2Sessions) != 1 || day2Sessions[0]["total"] != int64(20700) {
		t.Fatalf("expected 20700 tokens on day 2, got %+v", day2Sessions)
	}
}

func TestAdapterQueryReadsRequestParamsForUsageSummaryAndSessionUsage(t *testing.T) {
	home := t.TempDir()
	desktopDir := filepath.Join(home, ".gemini", "antigravity")
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	desktopConvDir := filepath.Join(desktopDir, "conversations")
	cliConvDir := filepath.Join(cliDir, "conversations")
	if err := os.MkdirAll(desktopConvDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cliConvDir, 0o700); err != nil {
		t.Fatal(err)
	}

	cliID := "77777777-7777-4777-8777-777777777771"
	desktopID := "88888888-8888-4888-8888-888888888882"

	// Desktop summary DB supplies metadata for desktop-only sessions, while CLI DB overrides duplicates.
	desktopSumDB, err := sql.Open("sqlite", filepath.Join(desktopDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := desktopSumDB.Exec(`CREATE TABLE conversation_summaries(conversation_id TEXT PRIMARY KEY, title TEXT, workspace_uris TEXT, killed INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopSumDB.Exec(`INSERT INTO conversation_summaries VALUES(?, 'Desktop 会话标题', '["file:///home/fixture/desktop-proj"]', 0)`, desktopID); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopSumDB.Exec(`INSERT INTO conversation_summaries VALUES(?, '将被 CLI 覆盖', '["file:///home/fixture/stale"]', 0)`, cliID); err != nil {
		t.Fatal(err)
	}
	desktopSumDB.Close()

	cliSumDB, err := sql.Open("sqlite", filepath.Join(cliDir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cliSumDB.Exec(`
		CREATE TABLE conversation_summaries(conversation_id TEXT PRIMARY KEY, title TEXT, workspace_uris TEXT, killed INTEGER);
		INSERT INTO conversation_summaries VALUES(?, 'CLI 会话标题', '["file:///home/fixture/cli-proj"]', 0);
	`, cliID); err != nil {
		t.Fatal(err)
	}
	cliSumDB.Close()

	makeStepMeta := func(unixSec int64, inTok, outTok, cacheRead uint64) []byte {
		ts := pbVarint(1, uint64(unixSec))
		u := append(pbVarint(2, inTok), pbVarint(3, outTok)...)
		u = append(u, pbVarint(5, cacheRead)...)
		return append(pbBytes(1, ts), pbBytes(9, u)...)
	}
	day1 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2026, 11, 18, 8, 30, 0, 0, time.UTC).Unix()

	cliDB, err := sql.Open("sqlite", filepath.Join(cliConvDir, cliID+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cliDB.Exec(`
		CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB);
		CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := cliDB.Exec(`INSERT INTO steps VALUES(1,15,3,0,?,?)`, pbVarint(1, 15), makeStepMeta(day1, 2000, 300, 8000)); err != nil {
		t.Fatal(err)
	}
	if _, err := cliDB.Exec(`INSERT INTO steps VALUES(2,15,3,0,?,?)`, pbVarint(1, 15), makeStepMeta(day2, 5000, 700, 15000)); err != nil {
		t.Fatal(err)
	}
	cliDB.Close()

	deskDB, err := sql.Open("sqlite", filepath.Join(desktopConvDir, desktopID+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deskDB.Exec(`
		CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB);
		CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER);
		INSERT INTO steps VALUES(1,15,3,0,?,?);
	`, pbVarint(1, 15), makeStepMeta(day2, 1000, 200, 3000)); err != nil {
		t.Fatal(err)
	}
	deskDB.Close()

	adapter := New(Config{Home: home})
	sinceDay2 := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC).UnixMilli()

	// Call Adapter.Query in the exact structure produced by grpcapi.translateNativeQuery.
	summaryRes, err := adapter.Query(context.Background(), core.Request{
		AgentType: "antigravity",
		Action:    "runtime.request",
		Fields: map[string]any{
			"method": "usage.summary",
			"request_params": map[string]any{
				"since_ms": sinceDay2,
			},
		},
	})
	if err != nil {
		t.Fatalf("Adapter.Query(usage.summary) failed: %v", err)
	}
	sessions := normalizeMapSlice(summaryRes["sessions"])
	if len(sessions) != 2 {
		t.Fatalf("expected 2 day-2 sessions (cli + desktop), got %+v", sessions)
	}
	byID := map[string]map[string]any{}
	for _, s := range sessions {
		byID[str(s, "session_id")] = s
	}
	if byID[cliID]["title"] != "CLI 会话标题" || byID[cliID]["total"] != int64(20700) {
		t.Fatalf("expected CLI session filtered to day-2 20700 tokens, got %+v", byID[cliID])
	}
	if byID[desktopID]["title"] != "Desktop 会话标题" || byID[desktopID]["total"] != int64(4200) {
		t.Fatalf("expected desktop fallback title & 4200 tokens, got %+v", byID[desktopID])
	}

	// Verify session.usage also honors request_params["conversation_id"] and request_params["readonly"].
	sessUsage, err := adapter.Query(context.Background(), core.Request{
		AgentType: "antigravity",
		Action:    "runtime.request",
		Fields: map[string]any{
			"method": "session.usage",
			"request_params": map[string]any{
				"conversation_id": cliID,
				"readonly":        false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Adapter.Query(session.usage) failed: %v", err)
	}
	if sessUsage["total_tokens"] != int64(31000) || sessUsage["context_available"] != true {
		t.Fatalf("unexpected session.usage via request_params: %+v", sessUsage)
	}
}
