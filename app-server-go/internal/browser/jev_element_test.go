package browser

import (
	"context"
	"strings"
	"testing"
)

func TestExtractInteractiveElementsAndTargetDispatch(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	// Configure fake CDP handler to return elements with rects and labels
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
								"x": 200.0, "y": 300.0, "w": 100.0, "h": 50.0,
							},
						},
						map[string]any{
							"id":    "2",
							"tag":   "INPUT",
							"label": "Username Field",
							"role":  "textbox",
							"rect": map[string]any{
								"x": 150.0, "y": 180.0, "w": 200.0, "h": 40.0,
							},
						},
					},
				},
			}, nil
		}
		return map[string]any{"result": map[string]any{"value": "ok"}}, nil
	})

	var recordedClicks []map[string]float64
	var recordedTexts []string

	fake.cdpHandlers.Store("Input.dispatchMouseEvent", func(method string, params map[string]any) (any, error) {
		mType, _ := params["type"].(string)
		if mType == "mousePressed" {
			x, _ := params["x"].(float64)
			y, _ := params["y"].(float64)
			recordedClicks = append(recordedClicks, map[string]float64{"x": x, "y": y})
		}
		return map[string]any{}, nil
	})

	fake.cdpHandlers.Store("Input.insertText", func(method string, params map[string]any) (any, error) {
		t, _ := params["text"].(string)
		recordedTexts = append(recordedTexts, t)
		return map[string]any{}, nil
	})

	stepCount := 0
	planner := &JevPlanner{
		Invoker: func(ctx context.Context, state any, questions map[string]any) (map[string]any, error) {
			stepCount++
			if stepCount == 1 {
				// Step 1: fill username input
				return map[string]any{
					"answers": map[string]any{
						"operation":        map[string]any{"choice": "TYPE_TEXT"},
						"type_text_target": map[string]any{"choice": "2"},
					},
				}, nil
			}
			if stepCount == 2 {
				// Step 2: click submit button
				return map[string]any{
					"answers": map[string]any{
						"operation":    map[string]any{"choice": "CLICK"},
						"click_target": map[string]any{"choice": "1"},
					},
				}, nil
			}
			// Step 3: DONE verified
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
		Goal:       "Fill username and submit",
		Inputs:     map[string]string{"username": "luke_test"},
		SessionKey: fixtureSessionKey,
	}, nil, planner)
	if err != nil {
		t.Fatalf("RunBrowserTask failed: %v", err)
	}

	if !res.OK || res.Status != "done" {
		t.Fatalf("expected status done, got %s", res.Status)
	}

	// Verify click was dispatched to actual element center (x: 200+50=250, y: 300+25=325), NOT viewport center (x:640, y:400)
	if len(recordedClicks) == 0 {
		t.Fatalf("expected recorded click mousePressed")
	}
	lastClick := recordedClicks[len(recordedClicks)-1]
	if lastClick["x"] != 250 || lastClick["y"] != 325 {
		t.Fatalf("expected click at element center (250, 325), got (%v, %v)", lastClick["x"], lastClick["y"])
	}

	// Verify text was inserted for matched field
	if len(recordedTexts) == 0 || recordedTexts[0] != "luke_test" {
		t.Fatalf("expected recorded text 'luke_test', got %#v", recordedTexts)
	}
}
