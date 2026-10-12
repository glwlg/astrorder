package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestRestartSuccessRetainsLegacyStatus(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	h.RestartDaemon = func(context.Context, bool) error { return nil }
	w := call(h, "POST", "/api/v1/services/daemon/restart", `{"confirm_active":true}`)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	var receipt map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		w = call(h, "GET", "/api/v1/services/daemon/restart/"+receipt["id"], "")
		var result map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["status"] == "success" {
			return
		}
		if result["status"] != "queued" && result["status"] != "running" {
			t.Fatalf("incompatible status: %v", result)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("restart did not succeed")
}

func TestRestartRequiresConfirmationAndSerializesOperations(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	entered := make(chan struct{})
	release := make(chan struct{})
	h.RestartDaemon = func(ctx context.Context, confirmed bool) error {
		if !confirmed {
			t.Error("confirmation was lost")
		}
		close(entered)
		select {
		case <-release:
			return errors.New("fixture failure")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, body := range []string{`{}`, `{"confirm_active":false}`, `{"confirm_active":"true"}`} {
		if w := call(h, "POST", "/api/v1/services/daemon/restart", body); w.Code != 422 {
			t.Fatal(w.Code)
		}
	}
	w := call(h, "POST", "/api/v1/services/daemon/restart", `{"confirm_active":true}`)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	var receipt map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("restart not started")
	}
	if w := call(h, "POST", "/api/v1/services/daemon/restart", `{"confirm_active":true}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	close(release)
	deadline := time.After(time.Second)
	for {
		w := call(h, "GET", "/api/v1/services/daemon/restart/"+receipt["id"], "")
		var result map[string]string
		json.Unmarshal(w.Body.Bytes(), &result)
		if result["status"] == "failed" {
			if result["detail"] != "fixture failure" || result["error"] != "fixture failure" {
				t.Fatal(result)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("restart did not finish")
		case <-time.After(time.Millisecond):
		}
	}
	if w := call(h, "GET", "/api/v1/services/daemon/restart/missing", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
