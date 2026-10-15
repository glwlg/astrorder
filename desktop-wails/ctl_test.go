package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCtlExecutableResolution(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	exe := resolveCtlExecutable(root)
	if exe == "" {
		t.Fatalf("expected to resolve astrorderctl executable in workspace %s", root)
	}
}

func TestCtlValidation(t *testing.T) {
	root, _ := filepath.Abs("..")
	client, err := NewCtlClient(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := client.Run(ctx, "invalid_target", "start", false); err == nil {
		t.Error("expected error for invalid target")
	}
	if _, err := client.Run(ctx, "app", "invalid_action", false); err == nil {
		t.Error("expected error for invalid action")
	}
	if _, err := client.Run(ctx, "app", "install", false); err == nil {
		t.Error("expected error for install action on app")
	}
	// Page-facing Run takes no confirmation flag; without a native dialog a
	// daemon stop against unreadable state must be cancelled, never forced.
	service := &ShellService{ctl: &CtlClient{CtlPath: client.CtlPath, RootDir: root, StateDir: t.TempDir()}}
	result, err := service.Run("daemon", "stop")
	if err != nil || result["state"] != "cancelled" {
		t.Fatalf("unconfirmed daemon stop = %v, %v", result, err)
	}
}

func TestDesktopDaemonUpdateWithoutCandidateUsesCheckBeforeConfirmation(t *testing.T) {
	candidate := os.Getenv("ASTRORDER_TEST_CTL")
	if candidate == "" {
		t.Skip("set ASTRORDER_TEST_CTL to the freshly built isolated controller")
	}
	root := t.TempDir()
	service := &ShellService{ctl: &CtlClient{CtlPath: candidate, RootDir: root, StateDir: t.TempDir()}}
	result, err := service.Run("daemon", "update")
	if err != nil || result["ok"] != true || result["state"] != "no_update" {
		t.Fatalf("update without candidate should not ask to stop: %v err=%v", result, err)
	}
}

func TestDesktopDaemonUpdateWithoutNativeConsentKeepsCandidate(t *testing.T) {
	candidate := os.Getenv("ASTRORDER_TEST_CTL")
	if candidate == "" {
		t.Skip("set ASTRORDER_TEST_CTL to the freshly built isolated controller")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "astrorder-sessiond.exe.new")
	if err := os.WriteFile(path, []byte("staged update"), 0600); err != nil {
		t.Fatal(err)
	}
	service := &ShellService{ctl: &CtlClient{CtlPath: candidate, RootDir: root, StateDir: t.TempDir()}}
	result, err := service.Run("daemon", "update")
	if err != nil || result["state"] != "cancelled" {
		t.Fatalf("update against unreadable authority without native consent: %v err=%v", result, err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "staged update" {
		t.Fatalf("cancelled update modified candidate: %v", err)
	}
}

func TestCtlUpdateHelperProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_CTL_HELPER") != "1" {
		return
	}
	if marker := os.Getenv("ASTRORDER_CTL_HELPER_MARKER"); marker != "" {
		if err := os.WriteFile(marker+".started", []byte("started"), 0600); err != nil {
			os.Exit(1)
		}
		time.Sleep(500 * time.Millisecond)
		if err := os.WriteFile(marker, []byte("complete"), 0600); err != nil {
			os.Exit(1)
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "state": "running"})
	os.Exit(0)
}

func TestCtlDaemonUpdateSurvivesCallerCancellation(t *testing.T) {
	t.Setenv("ASTRORDER_CTL_HELPER", "1")
	marker := filepath.Join(t.TempDir(), "finished")
	t.Setenv("ASTRORDER_CTL_HELPER_MARKER", marker)
	client := &CtlClient{CtlPath: os.Args[0], RootDir: t.TempDir(), StateDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := client.invokeIndependent(ctx, "-test.run=^TestCtlUpdateHelperProcess$")
		finished <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker + ".started"); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("isolated controller startup failed: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated controller did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller should return cancellation: %v", err)
	}
	for {
		if body, err := os.ReadFile(marker); err == nil && string(body) == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controller stopped with its cancelled caller")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCtlIndependentInvocationReturnsStructuredResult(t *testing.T) {
	t.Setenv("ASTRORDER_CTL_HELPER", "1")
	client := &CtlClient{CtlPath: os.Args[0], RootDir: t.TempDir(), StateDir: t.TempDir()}
	out, err := client.invokeIndependent(context.Background(), "-test.run=^TestCtlUpdateHelperProcess$")
	if err != nil || !bytes.Contains(out, []byte(`"ok":true`)) {
		t.Fatalf("isolated ctl invocation: %s err=%v", out, err)
	}
}
