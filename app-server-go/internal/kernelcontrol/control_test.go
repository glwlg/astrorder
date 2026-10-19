package kernelcontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOperationSurvivesServiceRecreation(t *testing.T) {
	dir := t.TempDir()
	s := &Service{StateDir: dir, Launch: func(id string) error { return nil }}
	op, err := s.Start("app", "restart", true)
	if err != nil {
		t.Fatal(err)
	}
	other := &Service{StateDir: dir}
	got, err := other.Get(op.ID)
	if err != nil || got.Status != "queued" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err = s.Start("daemon", "restart", true); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestWorkerPersistsFailureAndReleasesBusy(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	op, err := s.Start("daemon", "update", true)
	if err != nil {
		t.Fatal(err)
	}
	s.Run = func(context.Context, string, string) (map[string]any, error) {
		return map[string]any{"update_available": false}, nil
	}
	if err = s.Execute(context.Background(), op.ID); err == nil {
		t.Fatal("update without candidate succeeded")
	}
	got, _ := s.Get(op.ID)
	if got.Status != "failed" || got.Error == "" {
		t.Fatalf("%+v", got)
	}
	if _, err = s.Start("app", "restart", true); err != nil {
		t.Fatal(err)
	}
}
func TestRejectsInvalidActionsAndIDs(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	for _, p := range [][2]string{{"app", "update"}, {"daemon", "stop"}, {"unknown", "restart"}} {
		if _, e := s.Start(p[0], p[1], true); e == nil {
			t.Fatal(p)
		}
	}
	if _, e := s.Start("app", "restart", false); e == nil {
		t.Fatal("unconfirmed")
	}
	for _, id := range []string{"../production", "", "ABC"} {
		if _, e := s.Get(id); e == nil {
			t.Fatal(id)
		}
	}
}
func TestWorkerRequiresNewHealthyIdentity(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "changed"}[changed], func(t *testing.T) {
			s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
			op, _ := s.Start("app", "restart", true)
			calls := 0
			s.Run = func(_ context.Context, target, action string) (map[string]any, error) {
				if action == "restart" {
					return map[string]any{}, nil
				}
				calls++
				pid := 1
				if calls > 1 && changed {
					pid = 2
				}
				return map[string]any{"state": "running", "healthy": true, "identity_verified": true, "port_owned": true, "process": map[string]any{"pid": pid, "creation_time": pid}}, nil
			}
			err := s.Execute(context.Background(), op.ID)
			if (err == nil) != changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			got, _ := s.Get(op.ID)
			want := "failed"
			if changed {
				want = "success"
			}
			if got.Status != want {
				t.Fatal(got)
			}
		})
	}
}
func TestStartMarksAbandonedOperationFailed(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	op, _ := s.Start("app", "restart", true)
	data, _ := os.ReadFile(filepath.Join(s.StateDir, "kernel-operations", "active.json"))
	var old map[string]any
	json.Unmarshal(data, &old)
	old["created_at"] = "2000-01-01T00:00:00Z"
	data, _ = json.Marshal(old)
	os.WriteFile(filepath.Join(s.StateDir, "kernel-operations", "active.json"), data, 0600)
	if _, err := s.Start("app", "restart", true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(op.ID)
	if got.Status != "failed" {
		t.Fatal(got)
	}
}
