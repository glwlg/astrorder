package browser

import (
	"context"
	"testing"
)

func TestBrowserManagerRunTaskDelegation(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	handler, err := NewHandler(Options{
		Authorizer: testAuthenticator(),
		CDPURL:     fake.server.URL,
		NoAutoExec: true,
		Store:      newEngineStore(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	h := handler.(*Handler)
	h.manager.engineRunner = func(_ context.Context, request engineRequest) (engineResult, error) {
		return engineResult{OK: true, Status: "done", TargetID: request.TargetID}, nil
	}
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
