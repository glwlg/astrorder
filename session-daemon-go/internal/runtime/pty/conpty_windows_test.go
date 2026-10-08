//go:build windows

package pty

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("ASTRORDER_CONPTY_ENV_HELPER") == "1" {
		fmt.Printf("PTY_ENV_VALUE=%s:%s\n", os.Getenv("TERM"), os.Getenv("PTY_TEST_UNICODE"))
		os.Exit(0)
	}
}
func TestConPTYUsesExplicitEnvironmentAndSpacedExecutable(t *testing.T) {
	t.Setenv("ASTRORDER_CONPTY_ENV_HELPER", "1")
	t.Setenv("TERM", "parent-not-child")
	dir := t.TempDir()
	exe := filepath.Join(dir, "native shell with spaces.exe")
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	target.Close()
	env := append(os.Environ(), "TERM=explicit-conpty", "PTY_TEST_UNICODE=边界检查")
	terminal, err := defaultPlatformTerminal(exe, dir, env, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { data, err := io.ReadAll(terminal); done <- result{data, err} }()
	select {
	case r := <-done:
		if r.err != nil || !strings.Contains(string(r.data), "PTY_ENV_VALUE=explicit-conpty:边界检查") {
			t.Fatalf("native environment mismatch: %q, %v", r.data, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native console failed to reach EOF after child exit")
	}
}
