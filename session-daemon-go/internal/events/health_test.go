package events

import (
	"path/filepath"
	"testing"
)

func TestStorageWriteFailuresBlockAdmission(t *testing.T) {
	for _, operation := range []string{"commit", "track", "forget"} {
		t.Run(operation, func(t *testing.T) {
			j, err := Open(filepath.Join(t.TempDir(), "journal.db"), 16)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			if err = j.TrackSession("s", "idle"); err != nil {
				t.Fatal(err)
			}
			// Close only the underlying store to inject an actual database write failure.
			if err = j.store.Close(); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "commit":
				_, err = j.Commit("s", "data", nil, "running")
			case "track":
				err = j.TrackSession("s", "running")
			case "forget":
				err = j.ForgetSession("s")
			}
			if err == nil {
				t.Fatal("write succeeded against unavailable database")
			}
			if j.CheckAdmission() == nil || j.Health()["degraded"] != true {
				t.Fatal("write failure did not block new work")
			}
			if j.Statuses()["s"]["status"] != "idle" {
				t.Fatal("failed write advanced status")
			}
		})
	}
}
