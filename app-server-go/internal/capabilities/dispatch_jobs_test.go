package capabilities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackgroundDispatchJobsMultiSkillAndClear(t *testing.T) {
	env := newEnv("local")
	env.Files[env.P.Codex+"/skills/custom-a/SKILL.md"] = []byte("---\nname: custom-a\ndescription: Skill A\n---\nBody A\n")
	env.Files[env.P.Codex+"/skills/custom-b/SKILL.md"] = []byte("---\nname: custom-b\ndescription: Skill B\n---\nBody B\n")
	env.Packed[env.P.Codex+"/skills/custom-a"] = []byte("archive-a")
	env.Packed[env.P.Codex+"/skills/custom-b"] = []byte("archive-b")
	svc := serviceFor(env)
	h := NewHandler(svc, nil)

	reqBody := `{
		"capability_type": "skill",
		"source_target_id": "local",
		"items": [
			{"item_id": "custom-a", "source_agent": "codex", "label": "Skill A"},
			{"item_id": "custom-b", "source_agent": "codex", "label": "Skill B"}
		],
		"target_matrix": [
			{"target_id": "local", "agent": "hermes"},
			{"target_id": "local", "agent": "antigravity"}
		]
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capabilities/dispatch/jobs", strings.NewReader(reqBody))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"reports":null`) {
		t.Fatalf("expected non-null reports JSON array, got: %s", rec.Body.String())
	}

	var created DispatchJob
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if created.ID == "" || created.TotalSteps != 4 {
		t.Fatalf("unexpected created job: %+v", created)
	}

	deadline := time.Now().Add(5 * time.Second)
	var latest DispatchJob
	for time.Now().Before(deadline) {
		pollRec := httptest.NewRecorder()
		h.ServeHTTP(pollRec, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities/dispatch/jobs/"+created.ID, nil))
		if pollRec.Code != http.StatusOK {
			t.Fatalf("poll job status %d: %s", pollRec.Code, pollRec.Body.String())
		}
		if err := json.Unmarshal(pollRec.Body.Bytes(), &latest); err != nil {
			t.Fatalf("decode polled job: %v", err)
		}
		if latest.Status != "running" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	if latest.Status != "completed" || latest.CompletedSteps != 4 || latest.SuccessCount != 4 || latest.FailedCount != 0 {
		t.Fatalf("expected 4/4 completed job, got %+v", latest)
	}
	if len(latest.Reports) != 4 {
		t.Fatalf("expected 4 reports, got %d", len(latest.Reports))
	}

	if _, ok := env.Unpacks[env.P.Hermes+"/skills/custom-a"]; !ok {
		t.Fatalf("expected custom-a unpacked in hermes: %v", env.Unpacks)
	}
	if _, ok := env.Unpacks[env.P.Antigravity+"/skills/custom-b"]; !ok {
		t.Fatalf("expected custom-b unpacked in antigravity: %v", env.Unpacks)
	}

	clearRec := httptest.NewRecorder()
	h.ServeHTTP(clearRec, httptest.NewRequest(http.MethodPost, "/api/v1/capabilities/dispatch/jobs/clear", nil))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear jobs status %d: %s", clearRec.Code, clearRec.Body.String())
	}
	var cleared struct {
		Cleared int           `json:"cleared"`
		Jobs    []DispatchJob `json:"jobs"`
	}
	if err := json.Unmarshal(clearRec.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Cleared != 1 || len(cleared.Jobs) != 0 {
		t.Fatalf("unexpected clear result: %+v", cleared)
	}
}

func TestBackgroundDispatchJobHandlesPartialFailure(t *testing.T) {
	env := newEnv("local")
	env.Files[env.P.Codex+"/skills/valid-skill/SKILL.md"] = []byte("---\nname: valid-skill\ndescription: OK\n---\n")
	env.Packed[env.P.Codex+"/skills/valid-skill"] = []byte("archive-valid")
	svc := serviceFor(env)

	job, err := svc.StartDispatchJob(
		context.Background(),
		"skill",
		"local",
		[]DispatchJobItem{
			{ItemID: "valid-skill", SourceAgent: "codex"},
			{ItemID: "missing-skill", SourceAgent: "codex"},
		},
		[]DispatchTarget{{TargetID: "local", Agent: "hermes"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, ok := svc.GetDispatchJob(job.ID)
		if !ok {
			t.Fatal("job disappeared")
		}
		if cur.Status != "running" {
			if cur.Status != "partial" || cur.SuccessCount != 1 || cur.FailedCount != 1 {
				t.Fatalf("expected partial (1 ok, 1 fail), got %+v", cur)
			}
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out waiting for partial job")
}
