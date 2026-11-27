package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigPackagedVsDevSeparation(t *testing.T) {
	fakeLocal := filepath.Join(t.TempDir(), "LocalAppData")
	t.Setenv("LOCALAPPDATA", fakeLocal)

	packagedCfg, err := loadConfig("", "", true)
	if err != nil {
		t.Fatalf("loadConfig packaged failed: %v", err)
	}
	if !packagedCfg.Packaged {
		t.Fatalf("expected Packaged=true, got false")
	}
	wantPackagedState := filepath.Join(fakeLocal, "Astrorder")
	if !strings.EqualFold(packagedCfg.StateDir, wantPackagedState) {
		t.Fatalf("packaged StateDir = %q, want %q", packagedCfg.StateDir, wantPackagedState)
	}
	if !strings.EqualFold(filepath.Base(packagedCfg.RootDir), "server") {
		t.Fatalf("packaged RootDir = %q, expected basename 'server'", packagedCfg.RootDir)
	}

	devCfg, err := loadConfig("", "", false)
	if err != nil {
		t.Fatalf("loadConfig dev failed: %v", err)
	}
	if devCfg.Packaged {
		t.Fatalf("expected Packaged=false for dev build")
	}
	wantDevState := filepath.Join(fakeLocal, "Astrorder-Wails-Dev")
	if !strings.EqualFold(devCfg.StateDir, wantDevState) {
		t.Fatalf("dev StateDir = %q, want %q", devCfg.StateDir, wantDevState)
	}

	if _, err := loadConfig("", wantPackagedState, false); err == nil {
		t.Fatalf("expected dev config to reject production state directory %q", wantPackagedState)
	}
}
