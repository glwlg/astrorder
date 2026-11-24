package main

import (
	"testing"
)

func TestSanitizeSettingsTab(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"  skills  ":    "skills",
		"appearance":    "appearance",
		"connections":   "connections",
		"unknown-tab":   "",
		"../evil":       "",
		"skills?foo=1":  "",
	}
	for input, want := range cases {
		if got := sanitizeSettingsTab(input); got != want {
			t.Fatalf("sanitizeSettingsTab(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestOpenSettingsAndFocusMainGuards(t *testing.T) {
	shell := &ShellService{}
	if err := shell.OpenSettings("skills"); err == nil {
		t.Fatal("expected error when desktop app is nil")
	}
	if err := shell.FocusMain("/chat"); err == nil {
		t.Fatal("expected error when main window is nil")
	}
}
