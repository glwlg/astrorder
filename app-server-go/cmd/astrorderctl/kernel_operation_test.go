package main

import (
	"astrorder.dev/app-server/internal/kernelcontrol"
	"bytes"
	"context"
	"testing"
)

func TestKernelOperationCLIReadsDurableReceipt(t *testing.T) {
	s := &kernelcontrol.Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	op, err := s.Start("app", "restart", true)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// A nonexistent installation must produce a durable failure rather than a fake success.
	code := run([]string{"kernel-operation", "--root", t.TempDir(), "--state-dir", s.StateDir, "--id", op.ID}, &out)
	if code == 0 {
		t.Fatal(out.String())
	}
	got, err := s.Get(op.ID)
	if err != nil || got.Status != "failed" {
		t.Fatalf("%+v %v", got, err)
	}
	_ = context.Background()
}
