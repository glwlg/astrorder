package browser

import (
	"context"
	"strings"
	"testing"
)

func TestNegativePlannerActionsAndValidation(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	// Configure fake observe handler returning valid DOM elements
	fake.cdpHandlers.Store("Runtime.evaluate", func(method string, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		if strings.Contains(expr, "window.__astrorderObserve") {
			return map[string]any{
				"result": map[string]any{
					"value": []any{
						map[string]any{
							"id":    "1",
							"tag":   "BUTTON",
							"label": "Submit Form",
							"role":  "button",
							"rect": map[string]any{
								"x": 100.0, "y": 200.0, "w": 80.0, "h": 30.0,
							},
						},
					},
				},
			}, nil
		}
		if strings.Contains(expr, "document.title") || strings.Contains(expr, "location.href") {
			return map[string]any{"result": map[string]any{"value": map[string]any{
				"ready": "complete",
				"url":   "https://example.com/test",
				"title": "Example Domain",
				"text":  "Example text content",
			}}}, nil
		}
		return map[string]any{"result": map[string]any{"value": "ok"}}, nil
	})

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	// 1. Missing click target must return error and never dispatch click
	plannerMissingTarget := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			// Verify question includes goal_satisfied choice
			qGoal, ok := questions["goal_satisfied"].(map[string]any)
			if !ok || qGoal["type"] != "choice" {
				t.Errorf("questions missing choice goal_satisfied: %#v", questions)
			}
			return map[string]any{
				"answers": map[string]any{
					"operation": map[string]any{"choice": "CLICK"},
					// click_target omitted
				},
			}, nil
		},
		JevKey: "test-key",
	}

	_, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Negative click target test",
		SessionKey: fixtureSessionKey,
	}, nil, plannerMissingTarget)
	if err == nil || !strings.Contains(err.Error(), "click operation requires a target element choice") {
		t.Fatalf("expected missing click target error, got: %v", err)
	}

	// 2. Unknown target ID must return error
	plannerUnknownTarget := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return map[string]any{
				"answers": map[string]any{
					"operation":    map[string]any{"choice": "CLICK"},
					"click_target": map[string]any{"choice": "999"},
				},
			}, nil
		},
		JevKey: "test-key",
	}

	_, err = mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Unknown click target test",
		SessionKey: fixtureSessionKey,
	}, nil, plannerUnknownTarget)
	if err == nil || !strings.Contains(err.Error(), "not in observed elements") {
		t.Fatalf("expected target not in observed elements error, got: %v", err)
	}

	// 3. Missing typing match for field must fail with error and zero input insertText
	fake.cdpHandlers.Store("Runtime.evaluate", func(method string, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		if strings.Contains(expr, "window.__astrorderObserve") {
			return map[string]any{
				"result": map[string]any{
					"value": []any{
						map[string]any{
							"id":    "1",
							"tag":   "INPUT",
							"label": "Passport Number",
							"role":  "textbox",
							"rect": map[string]any{
								"x": 10.0, "y": 10.0, "w": 50.0, "h": 20.0,
							},
						},
					},
				},
			}, nil
		}
		return map[string]any{"result": map[string]any{"value": "ok"}}, nil
	})

	insertTextCalls := 0
	// Retain navigation metadata while replacing only DOM observation.
	previous, _ := fake.cdpHandlers.Load("Runtime.evaluate")
	fake.cdpHandlers.Store("Runtime.evaluate", func(method string, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		if strings.Contains(expr, "document.readyState") {
			return map[string]any{"result": map[string]any{"value": map[string]any{"ready": "complete", "url": "https://example.com/test", "title": "Fixture"}}}, nil
		}
		return previous.(func(string, map[string]any) (any, error))(method, params)
	})
	fake.cdpHandlers.Store("Input.insertText", func(method string, params map[string]any) (any, error) {
		insertTextCalls++
		return map[string]any{}, nil
	})

	plannerMissingMatch := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return map[string]any{
				"answers": map[string]any{
					"operation":        map[string]any{"choice": "TYPE_TEXT"},
					"type_text_target": map[string]any{"choice": "1"},
				},
			}, nil
		},
		JevKey: "test-key",
	}

	_, err = mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Missing input match test",
		Inputs:     map[string]string{"unrelated_field": "123"},
		SessionKey: fixtureSessionKey,
	}, nil, plannerMissingMatch)
	if err == nil || !strings.Contains(err.Error(), "no matching input value found") {
		t.Fatalf("expected no matching input error, got: %v", err)
	}
	if insertTextCalls > 0 {
		t.Fatalf("Input.insertText must not be dispatched when field match fails")
	}

	// 4. DONE with missing or NOT_SATISFIED goal_satisfied must reject completion
	plannerDoneNotSatisfied := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return map[string]any{
				"answers": map[string]any{
					"operation":      map[string]any{"choice": "DONE"},
					"goal_satisfied": map[string]any{"choice": "NOT_SATISFIED"},
				},
			}, nil
		},
		JevKey: "test-key",
	}

	res, err := mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "DONE without evidence test",
		MaxSteps:   2,
		SessionKey: fixtureSessionKey,
	}, nil, plannerDoneNotSatisfied)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK || res.Status == "done" {
		t.Fatalf("task must not be done when goal_satisfied is NOT_SATISFIED")
	}
	// Check that DONE_REJECTED was recorded in history
	hasRejected := false
	for _, step := range res.Steps {
		if step["operation"] == "DONE_REJECTED" {
			hasRejected = true
			break
		}
	}
	if !hasRejected {
		t.Fatalf("expected DONE_REJECTED step recorded: %#v", res.Steps)
	}

	// 5. Malformed DOM evaluation error must propagate
	fake.cdpHandlers.Store("Runtime.evaluate", func(method string, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		if strings.Contains(expr, "window.__astrorderObserve") {
			return map[string]any{
				"result": map[string]any{
					"value": "not-an-array",
				},
			}, nil
		}
		if strings.Contains(expr, "document.readyState") {
			return map[string]any{"result": map[string]any{"value": map[string]any{"ready": "complete", "url": "https://example.com/test", "title": "Fixture"}}}, nil
		}
		return map[string]any{"result": map[string]any{"value": "ok"}}, nil
	})

	plannerSimple := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			return map[string]any{
				"answers": map[string]any{
					"operation": map[string]any{"choice": "DONE"},
				},
			}, nil
		},
		JevKey: "test-key",
	}

	_, err = mgr.RunBrowserTask(ctx, BrowserTaskRequest{
		URL:        "https://example.com/test",
		Goal:       "Malformed DOM test",
		SessionKey: fixtureSessionKey,
	}, nil, plannerSimple)
	if err == nil || !strings.Contains(err.Error(), "malformed observed DOM elements") {
		t.Fatalf("expected malformed DOM elements error, got: %v", err)
	}
}
