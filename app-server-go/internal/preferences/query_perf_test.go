package preferences

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func explainPlan(t *testing.T, st *store.Store, query string, args ...any) []string {
	t.Helper()
	rows, err := st.DB().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v\nquery=%s", err, query)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan explain row: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows err: %v", err)
	}
	return lines
}

func TestReadAndSelectiveQueriesUsePrimaryKeyIndexWithoutTableScan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "index_plan.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Seed hundreds of non-P02 rows plus P02 keys to ensure planner evaluates realistic data.
	if err := st.SetPreference(ctx, "appearance:proj-a", map[string]any{"color": "blue", "icon": "folder"}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "meetings:records:v1", []map[string]any{{"id": "m1", "transcript": strings.Repeat("word ", 50000)}}, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if err := st.SetPreference(ctx, fmt.Sprintf("bot_group_msgs:grp-%03d", i), map[string]any{"i": i}, true); err != nil {
			t.Fatal(err)
		}
	}

	assertIndexedPlan := func(label, query string, args ...any) {
		t.Helper()
		plan := explainPlan(t, st, query, args...)
		joined := strings.Join(plan, "\n")
		if strings.Contains(joined, "SCAN workspace_preferences") {
			t.Fatalf("%s performed full table scan:\n%s\nquery=%s", label, joined, query)
		}
		if !strings.Contains(joined, "SEARCH workspace_preferences USING INDEX") {
			t.Fatalf("%s did not use workspace_preferences primary key index:\n%s\nquery=%s", label, joined, query)
		}
	}

	p02Query, p02Args := buildIndexedFilterQuery(p02ExactKeys, p02Prefixes)
	assertIndexedPlan("Read(P02)", p02Query, p02Args...)

	keyQuery, keyArgs := buildIndexedFilterQuery([]string{"meetings:records:v1"}, nil)
	assertIndexedPlan("Query(explicit key)", keyQuery, keyArgs...)

	prefixQuery, prefixArgs := buildIndexedFilterQuery(nil, []string{"meetings:"})
	assertIndexedPlan("Query(explicit prefix)", prefixQuery, prefixArgs...)

	assertIndexedPlan("ReadKey", `SELECT "key",value FROM workspace_preferences WHERE "key"=?`, "meetings:records:v1")
}

func TestReadIgnoresGiantMeetingsRecordsAndCorruptUnrelatedDomains(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "giant_meetings.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Seed valid P02 preferences.
	if _, err := Apply(ctx, st, Patch{
		Appearance:     map[string]any{"proj-main": map[string]any{"color": "cyan", "icon": "rocket"}},
		SessionPins:    map[string]any{"agent-1:sess-1": true},
		PinnedProjects: list("proj-main"),
		ProjectOrder:   list("proj-main", "proj-docs"),
	}); err != nil {
		t.Fatalf("Apply P02: %v", err)
	}

	// Insert multi-MB meetings:records:v1 and meetings:records payloads.
	hugeTranscript := strings.Repeat("这是一段非常长的会议转录文本用于回归测试。", 40000) // ~2.4 MB
	giantMeetings := []map[string]any{
		{"id": "mtg_001", "title": "季度架构评审会", "transcript": hugeTranscript},
		{"id": "mtg_002", "title": "产品路线图对齐", "transcript": strings.Repeat("讨论纪要内容。", 20000)},
	}
	if err := st.SetPreference(ctx, "meetings:records:v1", giantMeetings, true); err != nil {
		t.Fatalf("seed giant meetings:records:v1: %v", err)
	}
	if err := st.SetPreference(ctx, "meetings:records", giantMeetings[:1], true); err != nil {
		t.Fatalf("seed giant meetings:records: %v", err)
	}

	// Insert intentionally malformed non-JSON values in unrelated domains (including
	// uppercase lookalike "APPEARANCE:fake"). If Read scanned or decoded them, it would error!
	for key, badJSON := range map[string]string{
		"unrelated:corrupt":   "{{{NOT-VALID-JSON",
		"APPEARANCE:bad-case": "invalid-json-upper",
		"appearance_other:x":  "invalid-json-sibling",
		"meetings:corrupt":    "{} trailing-garbage",
	} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?)`, key, badJSON); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}

	// 1. Direct Read must succeed without scanning/decoding meetings:records:v1 or unrelated corrupt rows.
	state, err := Read(ctx, st)
	if err != nil {
		t.Fatalf("Read failed (likely scanned unrelated row): %v", err)
	}
	if len(state.Appearance) != 1 || state.SessionPins["agent-1:sess-1"] != true {
		t.Fatalf("unexpected P02 state: %#v", state)
	}

	// 2. HTTP GET /api/v1/preferences must succeed cleanly and return only P02 state.
	handler := NewHandler(Options{
		Store:      st,
		Authorizer: auth.New(config.Config{BrowserSecret: "pref-secret"}, nil),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/preferences", nil)
	req.Header.Set("Authorization", "Bearer pref-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/preferences status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "mtg_001") || strings.Contains(rec.Body.String(), "季度架构评审会") {
		t.Fatalf("GET /api/v1/preferences leaked meeting records")
	}

	// 3. Apply and Import must also succeed without touching unrelated corrupt/giant rows.
	updated, err := Apply(ctx, st, Patch{SessionPins: map[string]any{"agent-1:sess-2": true}})
	if err != nil {
		t.Fatalf("Apply failed with unrelated giant/corrupt rows: %v", err)
	}
	if updated.SessionPins["agent-1:sess-2"] != true {
		t.Fatalf("Apply did not persist pin: %#v", updated)
	}
}

func TestDefaultQueryExcludesHeavyKeysAndExplicitLookupReturnsFullMeetings(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "selective_query.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	hugeTranscript := strings.Repeat("会议纪要全文段落内容-", 30000)
	meetingsPayload := []map[string]any{
		{"id": "mtg_alpha", "title": "年度规划会议", "transcript": hugeTranscript},
	}
	oversizedOther := map[string]any{"blob": strings.Repeat("X", DefaultMaxValueBytes+1024)}

	if err := st.SetPreference(ctx, "meetings:records:v1", meetingsPayload, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "meetings:records", meetingsPayload, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "meetings:settings", map[string]any{"model_source": "global"}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "other:giant_blob", oversizedOther, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "appearance:proj-1", map[string]any{"color": "green", "icon": "code"}, true); err != nil {
		t.Fatal(err)
	}

	// Default unfiltered Query skips meetings:records:v1, meetings:records, and oversized blobs.
	defaultRows, err := Query(ctx, st, QueryOptions{})
	if err != nil {
		t.Fatalf("default Query: %v", err)
	}
	if _, found := defaultRows["meetings:records:v1"]; found {
		t.Fatalf("default Query must exclude meetings:records:v1")
	}
	if _, found := defaultRows["meetings:records"]; found {
		t.Fatalf("default Query must exclude meetings:records")
	}
	if _, found := defaultRows["other:giant_blob"]; found {
		t.Fatalf("default Query must exclude oversized blob")
	}
	if _, found := defaultRows["meetings:settings"]; !found {
		t.Fatalf("default Query should retain small meetings:settings")
	}
	if _, found := defaultRows["appearance:proj-1"]; !found {
		t.Fatalf("default Query should retain appearance:proj-1")
	}

	// Explicit ReadKey returns complete meetings:records:v1 intact.
	val, exists, err := ReadKey(ctx, st, "meetings:records:v1")
	if err != nil || !exists {
		t.Fatalf("ReadKey(meetings:records:v1) = (exists=%v, err=%v)", exists, err)
	}
	assertMeetingTranscriptIntact(t, val, hugeTranscript)

	// Explicit Query by Keys returns complete meetings:records:v1 intact.
	byKey, err := Query(ctx, st, QueryOptions{Keys: []string{"meetings:records:v1"}})
	if err != nil || len(byKey) != 1 {
		t.Fatalf("Query(Keys=[meetings:records:v1]) len=%d err=%v", len(byKey), err)
	}
	assertMeetingTranscriptIntact(t, byKey["meetings:records:v1"], hugeTranscript)

	// Explicit Query by Prefix ("meetings:") returns full meetings:records:v1 + meetings:records + meetings:settings.
	byPrefix, err := Query(ctx, st, QueryOptions{
		Keys:     []string{"meetings:records:v1", "appearance:proj-1"}, // overlaps with prefix to verify deduplication
		Prefixes: []string{"meetings:", "meetings:records:"},
	})
	if err != nil {
		t.Fatalf("Query(overlapping keys+prefixes): %v", err)
	}
	if len(byPrefix) != 4 {
		t.Fatalf("expected 4 rows (3 meetings:* + 1 appearance:proj-1), got %d: %v", len(byPrefix), mapKeys(byPrefix))
	}
	assertMeetingTranscriptIntact(t, byPrefix["meetings:records:v1"], hugeTranscript)

	// Store-level GetPreference (used directly by internal/meetings and internal/learning) remains 100% intact.
	pref, err := st.GetPreference(ctx, "meetings:records:v1")
	if err != nil || !pref.Exists {
		t.Fatalf("store.GetPreference(meetings:records:v1) = (exists=%v, err=%v)", pref.Exists, err)
	}
	assertMeetingTranscriptIntact(t, pref.Value, hugeTranscript)
}

func TestPrefixBoundsAndSpecialCharacters(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "prefix_edge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Verify prefixUpperBound handles normal, trailing-0xFF, and all-0xFF inputs.
	if upper, ok := prefixUpperBound("appearance:"); !ok || upper != "appearance;" {
		t.Fatalf("prefixUpperBound(appearance:) = (%q, %v)", upper, ok)
	}
	if upper, ok := prefixUpperBound("a\xff"); !ok || upper != "b" {
		t.Fatalf("prefixUpperBound(a\\xff) = (%q, %v)", upper, ok)
	}
	if _, ok := prefixUpperBound("\xff\xff"); ok {
		t.Fatalf("prefixUpperBound(\\xff\\xff) expected ok=false")
	}

	// Verify LIKE/GLOB wildcards in prefix (%, _, *, ?) are matched literally, not as wildcards.
	fixtures := map[string]any{
		"ns%1_a:one": true,
		"nsX1Ya:two": true, // would falsely match LIKE 'ns%1_a:%'
		"ns*b?:one":  true,
		"nsZZZ:two":  true, // would falsely match GLOB 'ns*b?:*'
	}
	for k, v := range fixtures {
		if err := st.SetPreference(ctx, k, v, true); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Query(ctx, st, QueryOptions{Prefixes: []string{"ns%1_a:", "ns*b?:"}})
	if err != nil {
		t.Fatalf("Query wildcard prefixes: %v", err)
	}
	if len(got) != 2 || got["ns%1_a:one"] != true || got["ns*b?:one"] != true {
		t.Fatalf("wildcard prefix query returned unexpected keys: %#v", got)
	}
}

func assertMeetingTranscriptIntact(t *testing.T, val any, wantTranscript string) {
	t.Helper()
	raw, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("marshal meeting value: %v", err)
	}
	var items []struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal meeting items: %v", err)
	}
	if len(items) != 1 || items[0].ID != "mtg_alpha" || items[0].Transcript != wantTranscript {
		t.Fatalf("meeting payload mismatch: len=%d id=%q transcriptLen=%d wantLen=%d", len(items), items[0].ID, len(items[0].Transcript), len(wantTranscript))
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
