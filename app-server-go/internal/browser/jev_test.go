package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRunBrowserTaskRequiresRealPlanning(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	// When no planner is provided and no Jev key is configured, it should fail with missing config error
	_, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Click submit button and verify success",
		SessionKey: fixtureSessionKey,
	}, nil, nil)
	if err == nil {
		t.Fatalf("expected error when Jev key/planner is not configured, got nil")
	}

	// With planner that provides step actions before done
	stepCount := 0
	planner := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			stepCount++
			if stepCount == 1 {
				// Step 1: Click button
				return map[string]any{
					"answers": map[string]any{
						"operation":    map[string]any{"choice": "CLICK"},
						"click_target": map[string]any{"choice": "1"},
					},
				}, nil
			}
			// Step 2: DONE
			return map[string]any{
				"answers": map[string]any{
					"operation":      map[string]any{"choice": "DONE"},
					"goal_satisfied": map[string]any{"choice": "SATISFIED"},
				},
			}, nil
		},
		JevKey: "test-jev-key",
	}

	res, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Click submit button and verify success",
		SessionKey: fixtureSessionKey,
	}, nil, planner)
	if err != nil {
		t.Fatalf("RunBrowserTask failed with planner: %v", err)
	}

	if !res.OK || res.Status != "done" {
		t.Fatalf("expected status done, got %s (ok=%v)", res.Status, res.OK)
	}
	if len(res.Steps) < 2 {
		t.Fatalf("expected at least 2 steps executed by planner, got %d", len(res.Steps))
	}
}

func TestRunBrowserTaskExhaustsSteps(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	// Planner returns CLICK forever, never DONE or BLOCKED
	planner := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return map[string]any{
				"answers": map[string]any{
					"operation":    map[string]any{"choice": "CLICK"},
					"click_target": map[string]any{"choice": "1"},
				},
			}, nil
		},
		JevKey: "test-key",
	}

	res, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Exhaust maximum steps test",
		MaxSteps:   3,
		SessionKey: fixtureSessionKey,
	}, nil, planner)
	if err != nil {
		t.Fatalf("RunBrowserTask failed: %v", err)
	}

	if res.OK {
		t.Fatalf("expected task not to be OK on step limit exhaustion")
	}
	if res.Status != "step_limit" {
		t.Fatalf("expected status 'step_limit', got '%s'", res.Status)
	}
	// Step 1: navigate, Steps 2, 3, 4: clicks => total steps = 4
	if len(res.Steps) != 4 {
		t.Fatalf("expected 4 steps recorded, got %d: %#v", len(res.Steps), res.Steps)
	}
}

func TestRunBrowserTaskPlannerError(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	planner := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return nil, errors.New("upstream model gateway unavailable")
		},
		JevKey: "test-key",
	}

	_, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Failure propagation test",
		SessionKey: fixtureSessionKey,
	}, nil, planner)
	if err == nil {
		t.Fatalf("expected planner error, got nil")
	}
}

func TestRunBrowserTaskHttpPlannerInvocation(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	// Mock Jev HTTP endpoint verifying payload structure
	requestsReceived := 0
	jevServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsReceived++
		if r.Header.Get("Authorization") != "Bearer mock-auth-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var reqPayload struct {
			Model     string         `json:"model"`
			State     map[string]any `json:"state"`
			Questions map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if reqPayload.Model != "jev-latest" || reqPayload.State == nil || reqPayload.Questions == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"answers": {
				"operation": {"choice": "DONE"},
				"goal_satisfied": {"choice": "SATISFIED"}
			}
		}`))
	}))
	defer jevServer.Close()

	// Use transport rewrite to test InvokeJevHTTP actual serialization against mock server
	transportClient := jevServer.Client()
	origTransport := transportClient.Transport
	transportClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		u, _ := url.Parse(jevServer.URL)
		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
		if origTransport != nil {
			return origTransport.RoundTrip(req)
		}
		return http.DefaultTransport.RoundTrip(req)
	})

	planner := &JevPlanner{
		Client: transportClient,
		JevKey: "mock-auth-key",
	}

	res, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "HTTP planner integration test",
		SessionKey: fixtureSessionKey,
	}, nil, planner)
	if err != nil {
		t.Fatalf("RunBrowserTask failed: %v", err)
	}

	if !res.OK || res.Status != "done" {
		t.Fatalf("expected done status, got %s", res.Status)
	}
	if requestsReceived == 0 {
		t.Fatalf("expected at least 1 HTTP request received by Jev mock server")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMatchFieldText(t *testing.T) {
	inputs := map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"pass":     "secret123",
	}

	val, ok := MatchFieldText(inputs, "Username / Account")
	if !ok || val != "alice" {
		t.Fatalf("expected alice, got %q (ok=%v)", val, ok)
	}

	// Tie ambiguity should return false, no arbitrary choice
	tieInputs := map[string]string{
		"field_a": "value_a",
		"field_b": "value_b",
	}
	_, ok = MatchFieldText(tieInputs, "field")
	if ok {
		t.Fatalf("expected tie failure on ambiguous match")
	}

	// Missing field should return false
	_, ok = MatchFieldText(inputs, "Phone Number")
	if ok {
		t.Fatalf("expected false for missing field")
	}
}

func TestBrowserManagerRunTaskDelegation(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	handler, err := NewHandler(Options{
		Authorizer: testAuthenticator(),
		CDPURL:     fake.server.URL,
		NoAutoExec: true,
		PlannerInvoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return map[string]any{
				"answers": map[string]any{
					"operation":      map[string]any{"choice": "DONE"},
					"goal_satisfied": map[string]any{"choice": "SATISFIED"},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := handler.(*Handler)
	ctx := context.Background()

	resMap, err := h.RunTask(ctx, map[string]any{
		"session_key": fixtureSessionKey,
		"url":         "https://example.com/test",
		"goal":        "RunTask interface test",
	})
	if err != nil {
		t.Fatalf("RunTask failed: %v", err)
	}

	if resMap["status"] != "done" || resMap["ok"] != true {
		t.Fatalf("RunTask response mismatch: %#v", resMap)
	}
}
