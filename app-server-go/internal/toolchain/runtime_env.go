package toolchain

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// readRegistryPathsFunc is overridden in tests to verify Windows Registry PATH
// refresh and merging deterministically on any host OS.
var readRegistryPathsFunc = readSystemRegistryPaths

// ManagedRuntimesRoot resolves the Astrorder-managed portable runtimes directory
// (e.g. <DataDir>/runtimes), honoring ASTRORDER_RUNTIMES_DIR when set.
func ManagedRuntimesRoot(dataDir, stateDir string) string {
	if override := strings.TrimSpace(os.Getenv("ASTRORDER_RUNTIMES_DIR")); override != "" {
		return filepath.Clean(override)
	}
	if d := strings.TrimSpace(dataDir); d != "" {
		return filepath.Join(filepath.Clean(d), "runtimes")
	}
	if s := strings.TrimSpace(stateDir); s != "" {
		return filepath.Join(filepath.Clean(s), "runtimes")
	}
	if runtime.GOOS == "windows" {
		if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
			return filepath.Join(filepath.Clean(base), "Astrorder", "data", "runtimes")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(filepath.Clean(home), ".astrorder", "runtimes")
	}
	return ""
}

// ManagedRuntimeBinDirs returns the ordered bin directories inside the managed
// runtimes root where portable Node.js, uv, and globally installed npm/uv CLIs live.
func ManagedRuntimeBinDirs(runtimesRoot string) []string {
	root := strings.TrimSpace(runtimesRoot)
	if root == "" {
		return nil
	}
	root = filepath.Clean(root)
	return []string{
		filepath.Join(root, "node"),
		filepath.Join(root, "node", "bin"),
		filepath.Join(root, "uv"),
		filepath.Join(root, "uv", "bin"),
		filepath.Join(root, "npm-global"),
		filepath.Join(root, "npm-global", "bin"),
		filepath.Join(root, "bin"),
	}
}

// MergePathLists merges ordered directory slices into a deduplicated PATH string.
func MergePathLists(lists ...[]string) string {
	seen := make(map[string]struct{}, 32)
	merged := make([]string, 0, 32)
	for _, list := range lists {
		for _, raw := range list {
			entry := strings.TrimSpace(strings.Trim(raw, `"`))
			if entry == "" {
				continue
			}
			clean := filepath.Clean(entry)
			key := clean
			if runtime.GOOS == "windows" {
				key = strings.ToLower(clean)
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, clean)
		}
	}
	return strings.Join(merged, string(os.PathListSeparator))
}

// BuildRefreshedLocalPath constructs a real-time local PATH merging:
//  1. Astrorder-managed portable runtimes bin directories
//  2. Current process PATH
//  3. Live Windows Registry User & Machine PATH entries (so tools installed
//     after startup are visible immediately without restarting Windows/Astrorder)
//  4. Existing well-known user bin directories (npm global, ~/.local/bin, etc.)
func BuildRefreshedLocalPath(runtimesRoot string) string {
	managed := ManagedRuntimeBinDirs(runtimesRoot)
	current := filepath.SplitList(os.Getenv("PATH"))
	var reg []string
	if readRegistryPathsFunc != nil {
		reg = readRegistryPathsFunc()
	}
	var existingExtra []string
	for _, dir := range localWellKnownBinDirs() {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			existingExtra = append(existingExtra, dir)
		}
	}
	return MergePathLists(managed, current, reg, existingExtra)
}

func localWellKnownBinDirs() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".vite-plus", "bin"),
			filepath.Join(home, ".antigravity", "bin"),
			filepath.Join(home, ".grok", "bin"),
			filepath.Join(home, ".hermes", "hermes-agent", "venv", "bin"),
		)
	}
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			dirs = append(dirs,
				filepath.Join(localAppData, "agy", "bin"),
				filepath.Join(localAppData, "hermes", "bin"),
				filepath.Join(localAppData, "hermes", "hermes-agent", "venv", "Scripts"),
				filepath.Join(localAppData, "Programs", "nodejs"),
				filepath.Join(localAppData, "Programs", "Git", "cmd"),
				filepath.Join(localAppData, "Microsoft", "WinGet", "Links"),
			)
		}
		if pf := strings.TrimSpace(os.Getenv("ProgramFiles")); pf != "" {
			dirs = append(dirs,
				filepath.Join(pf, "nodejs"),
				filepath.Join(pf, "Git", "cmd"),
			)
		}
	}
	return dirs
}

// MergeProcessEnv applies overrides to base environment variables without
// creating duplicate keys (case-insensitive on Windows so Path/PATH stay unified).
func MergeProcessEnv(base []string, overrides map[string]string) []string {
	norm := func(k string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	overrideIndex := make(map[string]string, len(overrides))
	overrideKey := make(map[string]string, len(overrides))
	for k, v := range overrides {
		nk := norm(k)
		overrideIndex[nk] = v
		overrideKey[nk] = k
	}

	out := make([]string, 0, len(base)+len(overrides))
	written := make(map[string]bool, len(overrides))
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		nk := norm(k)
		if val, replaced := overrideIndex[nk]; replaced {
			if !written[nk] {
				out = append(out, overrideKey[nk]+"="+val)
				written[nk] = true
			}
			continue
		}
		out = append(out, entry)
	}
	for nk, val := range overrideIndex {
		if !written[nk] {
			out = append(out, overrideKey[nk]+"="+val)
		}
	}
	return out
}

// LookPathInPath locates binary inside an explicit PATH string without mutating
// the process environment.
func LookPathInPath(binary, pathEnv string) (string, bool) {
	name := strings.TrimSpace(binary)
	if name == "" {
		return "", false
	}
	if strings.ContainsAny(name, `/\`) {
		if isExecutableFile(name) {
			return name, true
		}
		return "", false
	}
	suffixes := executableSuffixes()
	for _, dir := range filepath.SplitList(pathEnv) {
		dir = strings.TrimSpace(strings.Trim(dir, `"`))
		if dir == "" {
			continue
		}
		for _, suffix := range suffixes {
			candidate := filepath.Join(dir, name+suffix)
			if isExecutableFile(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

func executableSuffixes() []string {
	if runtime.GOOS != "windows" {
		return []string{""}
	}
	var suffixes []string
	for _, ext := range strings.Split(strings.ToLower(os.Getenv("PATHEXT")), ";") {
		ext = strings.TrimSpace(ext)
		if ext != "" {
			suffixes = append(suffixes, ext)
		}
	}
	if len(suffixes) == 0 {
		return []string{".exe", ".cmd", ".bat", ".com"}
	}
	return suffixes
}

func isExecutableFile(candidate string) bool {
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func (s *Service) runtimesRoot() string {
	return ManagedRuntimesRoot(s.dataDir, s.stateDir)
}

func (s *Service) refreshedLocalPath() string {
	return BuildRefreshedLocalPath(s.runtimesRoot())
}

func (s *Service) localChildEnv(extraEnv map[string]string) []string {
	overrides := make(map[string]string, len(extraEnv)+2)
	for k, v := range extraEnv {
		overrides[k] = v
	}
	overrides["PATH"] = s.refreshedLocalPath()
	// Ensure portable npm global installs go into a managed or writable directory
	// that is already on the refreshed PATH when using managed portable node.
	if root := s.runtimesRoot(); root != "" {
		managedNodeDir := filepath.Join(root, "node")
		if info, err := os.Stat(managedNodeDir); err == nil && info.IsDir() {
			if _, hasPrefix := overrides["NPM_CONFIG_PREFIX"]; !hasPrefix && strings.TrimSpace(os.Getenv("NPM_CONFIG_PREFIX")) == "" {
				overrides["NPM_CONFIG_PREFIX"] = filepath.Join(root, "npm-global")
			}
		}
	}
	return MergeProcessEnv(os.Environ(), overrides)
}
