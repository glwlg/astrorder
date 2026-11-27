//go:build windows

package pluginhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func pluginJobHelperCmd(mode, record string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestPluginJobObjectHelper$")
	cmd.Env = append(os.Environ(), "PLUGIN_JOB_HELPER="+mode, "PLUGIN_JOB_RECORD="+record)
	return cmd
}

func TestPluginJobObjectHelper(t *testing.T) {
	mode, record := os.Getenv("PLUGIN_JOB_HELPER"), os.Getenv("PLUGIN_JOB_RECORD")
	switch mode {
	case "":
		return
	case "parent":
		child := pluginJobHelperCmd("child", record)
		configurePluginProcess(child)
		guard, err := startPluginProcess(child)
		if err != nil {
			t.Fatal(err)
		}
		defer guard.Release()
		if err := os.WriteFile(record, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = child.Wait()
	case "child":
		grandchild := pluginJobHelperCmd("leaf", record)
		configurePluginProcess(grandchild)
		if err := grandchild.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record+".grandchild", []byte(strconv.Itoa(grandchild.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = grandchild.Wait()
	case "leaf":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestPluginJobObjectReclaimsProcessTreeOnHostCrash(t *testing.T) {
	record := filepath.Join(t.TempDir(), "plugin.pid")
	parent := pluginJobHelperCmd("parent", record)
	configurePluginProcess(parent)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()

	openRecorded := func(path string) windows.Handle {
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
		t.Fatalf("helper did not write %s", path)
		return 0
	}

	child := openRecorded(record)
	grandchild := openRecorded(record + ".grandchild")
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	for _, h := range []windows.Handle{child, grandchild} {
		res, err := windows.WaitForSingleObject(h, 5000)
		if err != nil || res != windows.WAIT_OBJECT_0 {
			t.Fatalf("plugin process survived host termination: result=%d err=%v", res, err)
		}
	}
}
