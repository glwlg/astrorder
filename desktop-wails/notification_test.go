package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNotifyPropagatesProcessFailure(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows toast")
	}
	t.Setenv("SystemRoot", filepath.Join(t.TempDir(), "missing-windows"))
	ok, err := (&ShellService{}).Notify("test", "test")
	if ok || err == nil {
		t.Fatal("failed process reported a delivered notification")
	}
}

func TestRealWindowsNotification(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("ASTRORDER_TEST_TOAST") != "1" {
		t.Skip("explicit local notification smoke test")
	}
	ok, err := (&ShellService{}).Notify("星序 · 通知验证", "任务完成与待回答问题通知通道测试。")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
}
