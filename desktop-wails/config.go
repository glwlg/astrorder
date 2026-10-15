package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config intentionally has no production defaults. Backend attachment is opt-in.
type Config struct {
	Backend  *url.URL
	StateDir string
	RootDir  string
	Packaged bool
}

func loadConfig(backend, stateDir string, packaged bool) (Config, error) {
	root, err := filepath.Abs("..")
	if err != nil {
		root = "."
	}
	if packaged {
		// Packaged layout: <install>/Astrorder.exe with kernels under <install>/server.
		exe, err := os.Executable()
		if err == nil {
			root = filepath.Join(filepath.Dir(exe), "server")
		}
	}

	if stateDir == "" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return Config{}, fmt.Errorf("LOCALAPPDATA is required; pass --state-dir explicitly")
		}
		if packaged {
			stateDir = filepath.Join(local, "Astrorder")
		} else {
			stateDir = filepath.Join(local, "Astrorder-Wails-Dev")
		}
	}
	absolute, err := filepath.Abs(stateDir)
	if err != nil {
		return Config{}, err
	}
	if !packaged {
		production := filepath.Join(os.Getenv("LOCALAPPDATA"), "Astrorder")
		rel, err := filepath.Rel(production, absolute)
		if err != nil {
			return Config{}, err
		}
		if rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return Config{}, fmt.Errorf("development shell must not use the production state directory")
		}
	}
	cfg := Config{StateDir: absolute, RootDir: root, Packaged: packaged}
	if backend == "" {
		return cfg, nil
	}
	u, err := url.Parse(backend)
	if err != nil {
		return Config{}, fmt.Errorf("invalid backend URL: %w", err)
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" || u.Port() == "30001" {
		return Config{}, fmt.Errorf("backend must be an explicit loopback HTTP origin with a non-production port")
	}
	cfg.Backend = u
	return cfg, nil
}
