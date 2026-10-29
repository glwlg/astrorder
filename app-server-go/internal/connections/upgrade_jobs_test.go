package connections_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/store"
)

type controlledUpgrade struct {
	started chan string
	finish  chan int
}

func (u *controlledUpgrade) Upgrade(ctx context.Context, kind, _ string, _ map[string]any, init func(string), output io.Writer) (int, error) {
	init(kind + " update")
	fmt.Fprintln(output, "live "+kind+" output")
	u.started <- kind
	select {
	case code := <-u.finish:
		return code, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func TestUpgradeJobSurvivesRequestAndResumesBatchObservation(t *testing.T) {
	for _, cancelJob := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelJob), func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			for _, kind := range []string{"codex", "hermes", "grok"} {
				_, err = st.UpsertAgent(context.Background(), map[string]any{"id": kind, "name": kind, "kind": kind, "status": "ready", "connection_id": "local"})
				if err != nil {
					t.Fatal(err)
				}
			}
			executor := &controlledUpgrade{started: make(chan string, 3), finish: make(chan int)}
			h, err := connections.NewHandler(connections.Options{Store: st, UpgradeExecutor: executor})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close(context.Background())
			ctx, disconnect := context.WithCancel(context.Background())
			defer disconnect()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents/upgrade/jobs", strings.NewReader(`{"agent_ids":["codex","hermes","grok"],"title":"batch"}`)).WithContext(ctx))
			if w.Code != http.StatusAccepted {
				t.Fatalf("start: %d %s", w.Code, w.Body.String())
			}
			var job map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
				t.Fatal(err)
			}
			id := job["id"].(string)
			disconnect()
			waitStart := func(want string) {
				t.Helper()
				select {
				case got := <-executor.started:
					if got != want {
						t.Fatalf("started %s, want %s", got, want)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("no start for %s", want)
				}
			}
			readJob := func() map[string]any {
				t.Helper()
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/agents/upgrade/jobs", nil))
				var result struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
					t.Fatalf("list: %s (%v)", w.Body.String(), err)
				}
				return result.Items[0]
			}
			waitStart("codex")
			if restored := readJob(); restored["status"] != "running" || !strings.Contains(restored["log"].(string), "live codex output") {
				t.Fatalf("request disconnect lost live task: %+v", restored)
			}
			// A failed first target must not stop the rest of the batch.
			executor.finish <- 7
			waitStart("hermes")
			wantStatus := "failed"
			if cancelJob {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents/upgrade/jobs/"+id+"/cancel", nil))
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancelled":true`) {
					t.Fatalf("cancel: %s", w.Body.String())
				}
				wantStatus = "cancelled"
			} else {
				executor.finish <- 0
				waitStart("grok")
				executor.finish <- 0
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				job = readJob()
				if job["status"] != "running" || time.Now().After(deadline) {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if job["status"] != wantStatus || !strings.Contains(job["log"].(string), "退出码: 7") {
				t.Fatalf("incorrect final result: %+v", job)
			}
			if cancelJob && len(executor.started) != 0 {
				t.Fatal("cancelled batch started another target")
			}
		})
	}
}
