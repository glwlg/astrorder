package store

import (
	"context"
	"testing"
)

func TestRecoveryStatusRefreshPreservesConcurrentRunningTransition(t *testing.T) {
	st := openStatusRefreshStore(t)
	defer st.Close()
	seedStatusRefreshSession(t, st)
	row, outcome, err := st.RefreshSessionStatus(context.Background(), "a", "s", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned", Status: "error"}, "idle")
	if err != nil || outcome != RefreshSessionStatusRebound || row != nil {
		t.Fatalf("stale recovery overwrote live status: outcome=%v row=%v err=%v", outcome, row, err)
	}
	row, err = st.GetSession(context.Background(), "a", "s")
	if err != nil || row["status"] != "running" {
		t.Fatalf("running status lost: %v %v", row, err)
	}
}
