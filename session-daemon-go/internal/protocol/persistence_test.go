package protocol

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/process"
)

func TestColdStartReportsOrphanLivenessWithoutAdoptingOrKilling(t *testing.T) {
	if mode := os.Getenv("ASTRORDER_ORPHAN_HELPER"); mode != "" {
		if mode == "live" {
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	live := exec.Command(os.Args[0], "-test.run=^TestColdStartReportsOrphanLivenessWithoutAdoptingOrKilling$")
	live.Env = append(os.Environ(), "ASTRORDER_ORPHAN_HELPER=live")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Process.Kill(); _ = live.Wait() }()
	dead := exec.Command(os.Args[0], "-test.run=^TestColdStartReportsOrphanLivenessWithoutAdoptingOrKilling$")
	dead.Env = append(os.Environ(), "ASTRORDER_ORPHAN_HELPER=dead")
	if err := dead.Start(); err != nil {
		t.Fatal(err)
	}
	_ = dead.Wait()

	path := filepath.Join(t.TempDir(), "sessiond.db")
	d, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	if r := d.recordEvent(map[string]any{"session_id": "live", "event": "started", "status": "running", "payload": map[string]any{"owner_pid": live.Process.Pid}}); r["action"] == "error" {
		t.Fatal(r)
	}
	if r := d.recordEvent(map[string]any{"session_id": "dead", "event": "started", "status": "running", "payload": map[string]any{"owner_pid": dead.Process.Pid}}); r["action"] == "error" {
		t.Fatal(r)
	}
	d.Close()

	restored, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	liveFrames, err := restored.journal.Replay("live", 1)
	if err != nil {
		t.Fatal(err)
	}
	deadFrames, err := restored.journal.Replay("dead", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(liveFrames) != 1 || liveFrames[0].Payload["orphan_alive"] != true || liveFrames[0].Payload["adopted"] != false {
		t.Fatalf("live orphan was not reported: %+v", liveFrames)
	}
	if !process.Alive(live.Process.Pid) {
		t.Fatal("cold start killed or lost the still-running child")
	}
	if len(deadFrames) != 1 || deadFrames[0].Payload["orphan_alive"] != false || deadFrames[0].Payload["adopted"] != false {
		t.Fatalf("dead owner was not reported: %+v", deadFrames)
	}
}

func TestColdStartMarksLostProcessOwnershipAsErrorWithoutRespawn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessiond.db")
	d, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	if r := d.recordEvent(map[string]any{"session_id": "busy", "event": "started", "status": "running"}); r["action"] == "error" {
		t.Fatal(r)
	}
	if r := d.recordEvent(map[string]any{"session_id": "busy", "event": "approval", "status": "waiting_approval"}); r["action"] == "error" {
		t.Fatal(r)
	}
	if r := d.recordEvent(map[string]any{"session_id": "quiet", "event": "done", "status": "idle"}); r["action"] == "error" {
		t.Fatal(r)
	}
	before := d.journal.Statuses()["busy"]["max_seq_id"]
	d.Close()

	restored, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	busy := restored.journal.Statuses()["busy"]
	if busy["status"] != "error" {
		t.Fatalf("lost process still advertised as %v", busy["status"])
	}
	if restored.journal.Statuses()["quiet"]["status"] != "idle" {
		t.Fatal("idle session was rewritten")
	}
	frames, err := restored.journal.Replay("busy", before.(int64))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].Event != "runtime.ownership_lost" || frames[0].Status != "error" || frames[0].Payload["adopted"] != false {
		t.Fatalf("missing explicit non-adoption frame: %+v", frames)
	}
	if restored.shutdown(map[string]any{})["action"] != "error" {
		t.Fatal("unreconciled session authorized shutdown")
	}
	restored.Close()

	again, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.journal.Statuses()["busy"]["max_seq_id"] != frames[0].SeqID {
		t.Fatal("second restart appended another ownership event")
	}
}

func TestPersistentDaemonRestoresActiveSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessiond.db")
	d, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	id := d.daemonID
	r := d.recordEvent(map[string]any{"session_id": "s", "event": "started", "status": "running"})
	if r["action"] == "error" {
		t.Fatal(r)
	}
	d.Close()
	restored, err := Open("secret", path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.daemonID != id {
		t.Fatal("identity changed")
	}
	result := restored.shutdown(map[string]any{})
	if result["action"] != "error" {
		t.Fatal("restored active session bypassed shutdown protection")
	}
}
