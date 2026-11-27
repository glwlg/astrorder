package desktopcontrol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess emulates CursorTouch/Windows-MCP over stdio JSON-RPC 2.0
// when spawned by unit tests via os.Executable().
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DESKTOPCONTROL_HELPER") != "1" {
		return
	}
	defer os.Exit(0)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if len(req.ID) == 0 {
			// Notification
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": mcpProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "windows-mcp", "version": "4.2.6-mock"},
				},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)

			// Emit a server notification before replying to verify notification skipping.
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/message",
				"params":  map[string]any{"level": "info", "data": "running " + params.Name},
			})

			switch params.Name {
			case "DisplayInventory":
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "Display 0: 1920x1080 (primary, scale 1.25)"},
						},
					},
				})
			case "Snapshot", "Screenshot":
				pngB64 := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nmockpng"))
				argsJSON, _ := json.Marshal(params.Arguments)
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": fmt.Sprintf("%s args=%s", params.Name, string(argsJSON))},
							{"type": "image", "mimeType": "image/png", "data": pngB64},
						},
					},
				})
			case "Click":
				if params.Arguments["loc"] == nil && params.Arguments["label"] == nil {
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"result": map[string]any{
							"isError": true,
							"content": []map[string]any{{"type": "text", "text": "missing target"}},
						},
					})
					continue
				}
				argsJSON, _ := json.Marshal(params.Arguments)
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": "clicked " + string(argsJSON)}},
					},
				})
			default:
				argsJSON, _ := json.Marshal(params.Arguments)
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("%s:%s", params.Name, string(argsJSON))}},
					},
				})
			}
		}
	}
}

func newMockWindowsDriver(t *testing.T, readCfg func(context.Context) Config) *WindowsMCPDriver {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	drv := NewWindowsMCPDriver(readCfg)
	drv.commandBuilder = func(cfg Config) (CommandSpec, error) {
		return CommandSpec{
			Command: exe,
			Args:    []string{"-test.run=TestHelperProcess", "--", cfg.PackageSpec},
			Env:     []string{"GO_WANT_DESKTOPCONTROL_HELPER=1"},
		}, nil
	}
	t.Cleanup(func() {
		_ = drv.Close(context.Background())
	})
	return drv
}

func TestWindowsMCPDriverRoundTripAndLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	drv := newMockWindowsDriver(t, func(context.Context) Config {
		cfg := DefaultConfig()
		cfg.IdleTimeoutSec = 60
		return cfg
	})

	// Before first call, driver is lazily not running.
	if info := drv.Info(ctx); info.Running {
		t.Fatalf("expected driver not running before first call, got %+v", info)
	}

	// TestConnection starts MCP server and queries DisplayInventory.
	info, msg, err := drv.TestConnection(ctx, nil)
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if !info.Running || info.Version != "4.2.6-mock" || !strings.Contains(msg, "Display 0: 1920x1080") {
		t.Fatalf("unexpected TestConnection info=%+v msg=%q", info, msg)
	}

	// Snapshot returns both text and decoded-ready image block, coercing string CLI params.
	snapRes, err := drv.Invoke(ctx, "snapshot", map[string]any{
		"use_vision":     "true",
		"use_annotation": "true",
		"display":        "0,1",
	})
	if err != nil {
		t.Fatalf("Invoke snapshot: %v", err)
	}
	if !strings.Contains(snapRes.Text, `"use_vision":true`) || !strings.Contains(snapRes.Text, `"display":[0,1]`) {
		t.Fatalf("unexpected snapshot text: %s", snapRes.Text)
	}
	if len(snapRes.Images) != 1 || snapRes.Images[0].MimeType != "image/png" || snapRes.Images[0].DataBase64 == "" {
		t.Fatalf("expected 1 png image block, got %+v", snapRes.Images)
	}

	// Click accepts string coordinates "640,480" and coerces to [640,480].
	clickRes, err := drv.Invoke(ctx, "click", map[string]any{
		"loc":    "640,480",
		"button": "right",
		"clicks": "2",
	})
	if err != nil {
		t.Fatalf("Invoke click: %v", err)
	}
	if !strings.Contains(clickRes.Text, `"loc":[640,480]`) || !strings.Contains(clickRes.Text, `"clicks":2`) {
		t.Fatalf("unexpected click text: %s", clickRes.Text)
	}

	// Wait with condition routes to WaitFor; without condition routes to Wait.
	wfRes, err := drv.Invoke(ctx, "wait", map[string]any{
		"condition": "text_exists",
		"text":      "Done",
		"timeout":   "5.5",
	})
	if err != nil || !strings.HasPrefix(wfRes.Text, "WaitFor:") || !strings.Contains(wfRes.Text, `"timeout":5.5`) {
		t.Fatalf("unexpected WaitFor result: res=%+v err=%v", wfRes, err)
	}
	wRes, err := drv.Invoke(ctx, "wait", map[string]any{"duration": "3"})
	if err != nil || !strings.HasPrefix(wRes.Text, "Wait:") || !strings.Contains(wRes.Text, `"duration":3`) {
		t.Fatalf("unexpected Wait result: res=%+v err=%v", wRes, err)
	}

	// Close shuts down child process cleanly.
	if err := drv.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if info := drv.Info(ctx); info.Running {
		t.Fatalf("expected driver stopped after Close, got %+v", info)
	}
}

func TestServiceSafetyPoliciesAndExtensionHTTP(t *testing.T) {
	ctx := context.Background()
	cfgValues := map[string]any{
		"mode":              ModeReadOnly,
		"blocked_shortcuts": "win+r, alt+f4",
	}
	drv := newMockWindowsDriver(t, func(context.Context) Config { return ParseConfig(cfgValues) })
	svc := NewService(Options{
		ReadConfig: func(context.Context) (map[string]any, error) { return cfgValues, nil },
		Driver:     drv,
	})

	// Manifest defaults to disabled and exposes desktop-control skill.
	m := svc.Manifest()
	if m.DefaultEnabled == nil || *m.DefaultEnabled {
		t.Fatalf("expected desktop-control DefaultEnabled=false, got %v", m.DefaultEnabled)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest validation failed: %v", err)
	}
	skills := svc.Skills()
	if len(skills) != 1 || skills[0].Name != "astrorder-desktop-control" || !strings.Contains(skills[0].Content, "{{CALLER_CONFIG_PATH}}") {
		t.Fatalf("unexpected skills: %+v", skills)
	}

	// Read-only mode permits status, displays, clipboard(get), but blocks click & clipboard(set).
	if _, err := svc.InvokeCapability(ctx, "status", nil); err != nil {
		t.Fatalf("status should succeed in read_only: %v", err)
	}
	if _, err := svc.InvokeCapability(ctx, "displays", nil); err != nil {
		t.Fatalf("displays should succeed in read_only: %v", err)
	}
	if _, err := svc.InvokeCapability(ctx, "clipboard", map[string]any{"mode": "get"}); err != nil {
		t.Fatalf("clipboard get should succeed in read_only: %v", err)
	}
	if _, err := svc.InvokeCapability(ctx, "click", map[string]any{"label": 1}); !errors.Is(err, ErrReadOnlyMode) {
		t.Fatalf("expected ErrReadOnlyMode for click, got %v", err)
	}
	if _, err := svc.InvokeCapability(ctx, "clipboard", map[string]any{"mode": "set", "text": "x"}); !errors.Is(err, ErrReadOnlyMode) {
		t.Fatalf("expected ErrReadOnlyMode for clipboard set, got %v", err)
	}

	// Switch to interactive mode: blocked shortcuts are still intercepted regardless of case/order.
	cfgValues["mode"] = ModeInteractive
	for _, blocked := range []string{"Alt+F4", "f4 + alt", "WIN+R", "r+super"} {
		if _, err := svc.InvokeCapability(ctx, "shortcut", map[string]any{"shortcut": blocked}); !errors.Is(err, ErrBlockedShortcut) {
			t.Fatalf("expected ErrBlockedShortcut for %q, got %v", blocked, err)
		}
	}
	// Non-blocked shortcut succeeds.
	scRes, err := svc.InvokeCapability(ctx, "shortcut", map[string]any{"shortcut": "ctrl+c"})
	if err != nil {
		t.Fatalf("expected ctrl+c to succeed, got %v", err)
	}
	if cr, ok := scRes.(*CallResult); !ok || !strings.Contains(cr.Text, "ctrl+c") {
		t.Fatalf("unexpected shortcut result: %+v", scRes)
	}

	// ServeExtension /status & /test-connection work cleanly.
	rec := httptest.NewRecorder()
	svc.ServeExtension(rec, httptest.NewRequest(http.MethodGet, "/status", nil), "/status")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"backend":"windows-mcp"`) {
		t.Fatalf("GET /status code=%d body=%s", rec.Code, rec.Body.String())
	}

	testRec := httptest.NewRecorder()
	reqBody := bytes.NewBufferString(`{"values":{"package_spec":"windows-mcp==4.2.6"}}`)
	svc.ServeExtension(testRec, httptest.NewRequest(http.MethodPost, "/test-connection", reqBody), "/test-connection")
	if testRec.Code != http.StatusOK || !strings.Contains(testRec.Body.String(), `"ok":true`) {
		t.Fatalf("POST /test-connection code=%d body=%s", testRec.Code, testRec.Body.String())
	}

	// Disabling plugin tears down running driver process.
	svc.OnEnabledChanged(ctx, false)
	if info := drv.Info(ctx); info.Running {
		t.Fatalf("expected driver stopped after OnEnabledChanged(false)")
	}
}
