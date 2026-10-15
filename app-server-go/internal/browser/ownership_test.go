package browser

import (
	"context"
	"testing"
)

func TestForeignTargetNeverReadResetOrClosed(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()
	shot, err := mgr.NewTab(ctx, fixtureSessionKey, "")
	if err != nil {
		t.Fatal(err)
	}
	foreign := "other-agent::other-session"
	for _, reset := range []bool{false, true} {
		if _, _, err = mgr.SessionPage(ctx, foreign, reset, true, shot.TargetID); err == nil {
			t.Errorf("foreign target accepted reset=%v", reset)
		}
	}
	if _, err = mgr.CloseTab(ctx, foreign, shot.TargetID); err == nil {
		t.Error("foreign tab closed")
	}
	pages, err := fakePages(fake, ctx)
	if err != nil || len(pages) != 1 {
		t.Fatalf("foreign request changed tabs: %v %v", pages, err)
	}
}
func fakePages(fake *FakeCDPServer, ctx context.Context) ([]PageTarget, error) {
	return (&CDPEndpoints{BaseURL: fake.server.URL, HTTPClient: fake.server.Client()}).ListPages(ctx)
}
