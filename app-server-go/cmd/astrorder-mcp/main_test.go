package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRetiresWithoutConfigOrCredentials(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(&stderr); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	message := stderr.String()
	if !strings.Contains(message, "retired") {
		t.Fatalf("expected retirement message, got %q", message)
	}
	if !strings.Contains(message, "astrorder-agent") {
		t.Fatalf("expected astrorder-agent guidance, got %q", message)
	}
}
