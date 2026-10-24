package events

import (
	"path/filepath"
	"testing"
)

func TestRetiredEventAfterReopenDoesNotPoisonStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	j, err := Open(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.TrackSession("retired", "idle"); err != nil {
		t.Fatal(err)
	}
	if err = j.ForgetSession("retired"); err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = Open(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err = j.Commit("retired", "native.activity", nil, "idle"); err == nil {
		t.Fatal("retired event accepted")
	}
	if err = j.CheckAdmission(); err != nil {
		t.Fatalf("late retired event poisoned all sessions: %v", err)
	}
	if _, err = j.Commit("live", "message", nil, "idle"); err != nil {
		t.Fatal(err)
	}
}
