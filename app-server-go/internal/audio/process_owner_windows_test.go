//go:build windows

package audio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func audioOwnerHelper(mode, record string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestAudioOwnerHelper$")
	cmd.Env = append(os.Environ(), "AUDIO_OWNER_HELPER="+mode, "AUDIO_OWNER_RECORD="+record)
	return cmd
}

func TestAudioOwnerHelper(t *testing.T) {
	mode, record := os.Getenv("AUDIO_OWNER_HELPER"), os.Getenv("AUDIO_OWNER_RECORD")
	switch mode {
	case "":
		return
	case "parent":
		child := audioOwnerHelper("child", record)
		release, err := startPlatformProcess(child)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := os.WriteFile(record, []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()
	case "child":
		grandchild := audioOwnerHelper("leaf", record)
		configurePlatformProcess(grandchild)
		grandchild.Env = append(grandchild.Env, "AUDIO_OWNER_HELPER=leaf", "AUDIO_OWNER_RECORD="+record)
		if err := grandchild.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record+".grandchild", []byte(strconv.Itoa(grandchild.Process.Pid)), 0600); err != nil {
			t.Fatal(err)
		}
		_ = grandchild.Wait()
	case "leaf":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestAudioOwnerKillsModelTreeWhenAppIsTerminated(t *testing.T) {
	record := filepath.Join(t.TempDir(), "model.pid")
	parent := audioOwnerHelper("parent", record)
	configurePlatformProcess(parent)
	parent.Env = append(parent.Env, "AUDIO_OWNER_HELPER=parent", "AUDIO_OWNER_RECORD="+record)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()

	openRecordedProcess := func(path string) windows.Handle {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			raw, err := os.ReadFile(path)
			pid, parseErr := strconv.Atoi(string(raw))
			if err == nil && parseErr == nil {
				handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = windows.TerminateProcess(handle, 1); _ = windows.CloseHandle(handle) })
				return handle
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("helper did not create %s", path)
		return 0
	}
	child := openRecordedProcess(record)
	grandchild := openRecordedProcess(record + ".grandchild")
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	for _, handle := range []windows.Handle{child, grandchild} {
		result, err := windows.WaitForSingleObject(handle, 5000)
		if err != nil || result != windows.WAIT_OBJECT_0 {
			t.Fatalf("audio process survived App termination: result=%d err=%v", result, err)
		}
	}
}
