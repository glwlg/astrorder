package antigravity

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

const realUsageJSONSample = `{"conversation_id":"","status":"SUCCESS","response":"","duration_seconds":0,"num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0},"command":{"name":"usage","data":{"description":"Within each group, models share a weekly limit and a 5-hour limit.","groups":[{"name":"Gemini Models","description":"Models within this group: Gemini 4 Neon, Gemini 4 Hydrogen, Orchestrator (Neon), Gemini 3.8 Flash","buckets":[{"id":"gemini-weekly","name":"Weekly Limit Remaining","description":"weekly","window":"weekly","remaining_fraction":0.3160819709300995,"reset_time":"2026-11-26T14:00:55Z"},{"id":"gemini-5h","name":"Five Hour Limit Remaining","description":"5h","window":"5h","remaining_fraction":0.8560883402824402,"reset_time":"2026-11-21T06:30:22Z"}]},{"name":"Claude and GPT models","description":"Models within this group: Claude Opus 5.5, Claude Sonnet 5.5, GPT-OSS 120B","buckets":[{"id":"3p-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":1,"reset_time":"1970-01-01T00:00:00Z"},{"id":"3p-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":1,"reset_time":"1970-01-01T00:00:00Z"}]},{"name":"Gemini 4 Argon","description":"For paid Pro plans, Argon quota is separate.","buckets":[{"id":"gemini-argon-wtu-weekly","name":"Gemini 4 Argon Weekly Limit","window":"weekly","remaining_fraction":0,"reset_time":"2026-11-27T06:46:15Z"}]},{"name":"Zero Omitted","description":"Omitted remaining_fraction","buckets":[{"id":"omitted-weekly","name":"Weekly","window":"weekly","reset_time":"2026-11-28T00:00:00Z"}]}]}}}`

func TestQuotaHelperProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_QUOTA_HELPER") != "1" {
		return
	}
	if countFile := os.Getenv("ASTRORDER_QUOTA_COUNT_FILE"); countFile != "" {
		f, err := os.OpenFile(countFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = f.WriteString("1\n")
			_ = f.Close()
		}
	}
	if os.Getenv("ASTRORDER_QUOTA_FAIL") == "1" {
		_, _ = fmt.Fprintln(os.Stderr, "usage command failed")
		os.Exit(2)
	}
	time.Sleep(40 * time.Millisecond)
	fmt.Println("warning: benign notice before json")
	fmt.Println(realUsageJSONSample)
	os.Exit(0)
}

func TestParseUsageCLIOutput(t *testing.T) {
	groups, desc, err := parseUsageCLIOutput([]byte(realUsageJSONSample))
	if err != nil {
		t.Fatal(err)
	}
	if desc == "" || len(groups) != 4 {
		t.Fatalf("unexpected parsed groups: desc=%q len=%d", desc, len(groups))
	}

	geminiBuckets := groups[0]["buckets"].([]map[string]any)
	if geminiBuckets[0]["used_percent"] != 68.4 {
		t.Fatalf("weekly used_percent=%v want 68.4", geminiBuckets[0]["used_percent"])
	}
	if _, ok := geminiBuckets[0]["reset_at"].(int64); !ok {
		t.Fatalf("expected int64 reset_at on gemini weekly, got %#v", geminiBuckets[0]["reset_at"])
	}
	if geminiBuckets[1]["used_percent"] != 14.4 {
		t.Fatalf("5h used_percent=%v want 14.4", geminiBuckets[1]["used_percent"])
	}

	// 1970-01-01T00:00:00Z must NOT produce reset_at.
	claudeBuckets := groups[1]["buckets"].([]map[string]any)
	if claudeBuckets[0]["used_percent"] != 0.0 {
		t.Fatalf("claude used_percent=%v want 0", claudeBuckets[0]["used_percent"])
	}
	if _, exists := claudeBuckets[0]["reset_at"]; exists {
		t.Fatalf("1970 sentinel reset_time must be omitted, got %#v", claudeBuckets[0]["reset_at"])
	}

	// Explicit 0 and omitted remaining_fraction both report 100% used.
	argonBuckets := groups[2]["buckets"].([]map[string]any)
	if argonBuckets[0]["used_percent"] != 100.0 {
		t.Fatalf("zero remaining_fraction used_percent=%v want 100", argonBuckets[0]["used_percent"])
	}
	omittedBuckets := groups[3]["buckets"].([]map[string]any)
	if omittedBuckets[0]["used_percent"] != 100.0 {
		t.Fatalf("omitted remaining_fraction used_percent=%v want 100", omittedBuckets[0]["used_percent"])
	}
}

func TestQuotaReadSingleflightAndCache(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	countFile := t.TempDir() + "/calls.txt"
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestQuotaHelperProcess", "--"},
		Environment: []string{"ASTRORDER_QUOTA_HELPER=1", "ASTRORDER_QUOTA_COUNT_FILE=" + countFile},
		Workspace:   t.TempDir(),
	})
	a.setTierInfo(map[string]any{"name": "Google AI Pro", "id": "pro"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	results := make([]map[string]any, 5)
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = a.quotaRead(ctx)
		}(i)
	}
	wg.Wait()

	for i := range results {
		if errs[i] != nil {
			t.Fatalf("call %d failed: %v", i, errs[i])
		}
		if results[i]["plan"] != "Google AI Pro" || results[i]["source"] != "agy_cli_usage" {
			t.Fatalf("call %d unexpected response: %+v", i, results[i])
		}
		groups, _ := results[i]["groups"].([]map[string]any)
		if len(groups) != 4 {
			t.Fatalf("call %d expected 4 groups, got %+v", i, results[i])
		}
	}

	// Next call within TTL must hit cache without spawning a second process.
	cachedRes, err := a.quotaRead(ctx)
	if err != nil || len(cachedRes["groups"].([]map[string]any)) != 4 {
		t.Fatalf("cached call unexpected: %+v err=%v", cachedRes, err)
	}

	rawCount, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(rawCount), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("expected singleflight + cache to invoke helper exactly once, got %d times", len(lines))
	}
}

func TestQuotaReadDegradesGracefullyOnCLIFailure(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestQuotaHelperProcess", "--"},
		Environment: []string{"ASTRORDER_QUOTA_HELPER=1", "ASTRORDER_QUOTA_FAIL=1"},
		Workspace:   t.TempDir(),
	})
	a.setTierInfo(map[string]any{"name": "Google AI Pro", "id": "pro"})

	res, err := a.quotaRead(context.Background())
	if err != nil {
		t.Fatalf("expected graceful degradation without error, got %v", err)
	}
	if res["plan"] != "Google AI Pro" {
		t.Fatalf("expected fallback tier plan preserved, got %+v", res)
	}
	if _, hasGroups := res["groups"]; hasGroups {
		t.Fatalf("failed CLI should not attach stale/invalid groups: %+v", res)
	}
}

type mockQuotaSSHSession struct {
	stdinW io.WriteCloser
	stdout io.ReadCloser
	exit   ssh.ExitStatus
}

func (m *mockQuotaSSHSession) Stdin() io.WriteCloser        { return m.stdinW }
func (m *mockQuotaSSHSession) Stdout() io.ReadCloser        { return m.stdout }
func (m *mockQuotaSSHSession) Stderr() []byte               { return nil }
func (m *mockQuotaSSHSession) Wait() (ssh.ExitStatus, error) { return m.exit, nil }
func (m *mockQuotaSSHSession) Close() error                 { return nil }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestQuotaReadRemoteSSHBypassACPArgAndParsesGroups(t *testing.T) {
	var seenArgs []string
	var calls int32
	a := NewWithSSH(Config{
		Executable:   "/home/luwei/.local/bin/agy",
		Arguments:    []string{"--profile", "work", "acp"},
		Workspace:    "/home/luwei/project",
		Allowed:      []string{"/"},
		ConnectionID: "ssh-debian",
	}, func(_ context.Context, spec ssh.CommandSpec) (SSHSession, error) {
		atomic.AddInt32(&calls, 1)
		seenArgs = append([]string(nil), spec.Args...)
		return &mockQuotaSSHSession{
			stdinW: nopWriteCloser{io.Discard},
			stdout: io.NopCloser(bytes.NewBufferString(realUsageJSONSample)),
			exit:   ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0},
		}, nil
	}, nil)
	a.setTierInfo(map[string]any{"name": "Google AI Ultra"})

	res, err := a.quotaRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--profile", "work", "-p", "/usage", "--output-format", "json"}
	if fmt.Sprintf("%q", seenArgs) != fmt.Sprintf("%q", wantArgs) {
		t.Fatalf("remote args=%q want %q", seenArgs, wantArgs)
	}
	groups, _ := res["groups"].([]map[string]any)
	if atomic.LoadInt32(&calls) != 1 || len(groups) != 4 || res["plan"] != "Google AI Ultra" {
		t.Fatalf("unexpected remote quota result: calls=%d res=%+v", calls, res)
	}
}
