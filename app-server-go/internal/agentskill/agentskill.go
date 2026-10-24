package agentskill

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

//go:embed SKILL.md
var EmbeddedSkillMD []byte

const (
	ManifestOwner   = "astrorderctl"
	ManifestVersion = 1
	ManifestFile    = "astrorder-manifest.json"
	CallerFile      = "astrorder-caller.json"
	SkillMDFile     = "SKILL.md"
)

type InstallOptions struct {
	Runtime      string `json:"runtime"`
	Home         string `json:"home"`
	CLI          string `json:"cli"`
	AgentID      string `json:"agent_id"`
	ConnectionID string `json:"connection_id"`
	ProfileName  string `json:"profile_name"`
}

type InstallResult struct {
	OK           bool   `json:"ok"`
	Runtime      string `json:"runtime"`
	Home         string `json:"home"`
	SkillDir     string `json:"skill_dir"`
	SkillFile    string `json:"skill_file"`
	BinaryFile   string `json:"binary_file"`
	CallerConfig string `json:"caller_config"`
	ManifestFile string `json:"manifest_file"`
	AgentID      string `json:"agent_id"`
	ConnectionID string `json:"connection_id"`
	ProfileName  string `json:"profile_name"`
}

type Manifest struct {
	FormatVersion int               `json:"format_version"`
	Owner         string            `json:"owner"`
	Runtime       string            `json:"runtime"`
	AgentID       string            `json:"agent_id"`
	ConnectionID  string            `json:"connection_id"`
	ProfileName   string            `json:"profile_name"`
	CLIPath       string            `json:"cli_path"`
	InstalledAt   string            `json:"installed_at"`
	FileHashes    map[string]string `json:"file_hashes"`
}

type callerConfigFile struct {
	AgentID      string `json:"agent_id"`
	ConnectionID string `json:"connection_id"`
	ProfileName  string `json:"profile_name"`
	Runtime      string `json:"runtime"`
}

func normalizePath(p string) string {
	if runtime.GOOS == "windows" {
		if len(p) >= 3 && p[0] == '/' && p[2] == '/' && ((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) {
			drive := strings.ToUpper(string(p[1]))
			p = drive + ":" + p[2:]
		}
	}
	return p
}

func containsWhitespace(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func targetBinaryName(cliPath string) string {
	if runtime.GOOS == "windows" || strings.HasSuffix(strings.ToLower(cliPath), ".exe") {
		return "astrorder-agent.exe"
	}
	return "astrorder-agent"
}

func fileExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && !stat.IsDir()
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func checkSymlinkEscape(home string, targetPath string) error {
	cleanHome := filepath.Clean(home)
	evalHome, err := filepath.EvalSymlinks(cleanHome)
	if err != nil {
		evalHome = cleanHome
	}

	// Verify target itself is not an escaping symlink
	if lstat, err := os.Lstat(targetPath); err == nil && lstat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed file %s must not be a symlink", targetPath)
	}

	// Check directory ancestry
	current := filepath.Dir(targetPath)
	for {
		lstat, err := os.Lstat(current)
		if err == nil && lstat.Mode()&os.ModeSymlink != 0 {
			evalCurrent, err := filepath.EvalSymlinks(current)
			if err != nil {
				return fmt.Errorf("eval symlink %s: %w", current, err)
			}
			rel, err := filepath.Rel(evalHome, evalCurrent)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("symlink escape detected: %s resolves outside home (%s)", current, evalCurrent)
			}
		}
		parent := filepath.Dir(current)
		if parent == current || parent == cleanHome || len(parent) < len(cleanHome) {
			break
		}
		current = parent
	}
	return nil
}

func readCallerConfig(path string) (*callerConfigFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read caller config %s: %w", path, err)
	}
	var rawMap map[string]any
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil, fmt.Errorf("parse caller config %s: %w", path, err)
	}
	if _, ok := rawMap["session_id"]; ok {
		return nil, fmt.Errorf("caller config %s must not contain session_id", path)
	}
	if _, ok := rawMap["sessionid"]; ok {
		return nil, fmt.Errorf("caller config %s must not contain sessionid", path)
	}
	allowedKeys := map[string]bool{
		"agent_id":      true,
		"connection_id": true,
		"profile_name":  true,
		"runtime":       true,
	}
	for k := range rawMap {
		if !allowedKeys[k] {
			return nil, fmt.Errorf("caller config %s contains unknown field %q", path, k)
		}
	}
	cfg := &callerConfigFile{}
	for _, key := range []string{"agent_id", "connection_id", "profile_name", "runtime"} {
		v, ok := rawMap[key].(string)
		if !ok || v == "" {
			return nil, fmt.Errorf("caller config %s field %q is required and must not be empty", path, key)
		}
		if containsWhitespace(v) {
			return nil, fmt.Errorf("caller config %s field %q contains illegal whitespace: %q", path, key, v)
		}
		switch key {
		case "agent_id":
			cfg.AgentID = v
		case "connection_id":
			cfg.ConnectionID = v
		case "profile_name":
			cfg.ProfileName = v
		case "runtime":
			cfg.Runtime = v
		}
	}
	return cfg, nil
}

func readManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if m.Owner == "" {
		return nil, fmt.Errorf("manifest %s missing owner field", path)
	}
	if m.FileHashes == nil {
		m.FileHashes = make(map[string]string)
	}
	return &m, nil
}

func renderSkillMD(template []byte, targetRuntime string, home string) []byte {
	content := string(template)
	callerBash := filepath.ToSlash(filepath.Join(home, CallerFile))
	callerWin := filepath.FromSlash(filepath.Join(home, CallerFile))

	content = strings.ReplaceAll(content, "{{CALLER_CONFIG_PATH}}", callerBash)
	content = strings.ReplaceAll(content, "{{CALLER_CONFIG_PATH_WIN}}", callerWin)
	return []byte(content)
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic rename %s to %s: %w", tmpName, path, err)
	}
	cleanup = false
	return nil
}

// Install validates options, checks conflicts/ownership, and performs atomic skill distribution.
func Install(opts InstallOptions) (*InstallResult, error) {
	return install(opts, func(path string, data []byte, perm os.FileMode) error {
		if existing, err := os.ReadFile(path); err == nil && hashBytes(existing) == hashBytes(data) {
			return nil
		}
		return atomicWriteFile(path, data, perm)
	})
}

func install(opts InstallOptions, writeFile func(string, []byte, os.FileMode) error) (_ *InstallResult, resultErr error) {
	// 1. Validate Runtime
	targetRuntime := strings.ToLower(strings.TrimSpace(opts.Runtime))
	if targetRuntime != "hermes" && targetRuntime != "codex" && targetRuntime != "grok" {
		return nil, fmt.Errorf("invalid runtime %q: must be hermes, codex or grok", opts.Runtime)
	}

	// 2. Validate Home
	rawHome := normalizePath(opts.Home)
	if rawHome == "" {
		return nil, errors.New("home directory is required")
	}
	if !filepath.IsAbs(rawHome) {
		return nil, fmt.Errorf("home directory must be absolute path: %q", opts.Home)
	}
	cleanHome := filepath.Clean(rawHome)

	// 3. Validate CLI Executable
	rawCLI := normalizePath(opts.CLI)
	if rawCLI == "" {
		return nil, errors.New("cli executable path is required")
	}
	if !filepath.IsAbs(rawCLI) {
		return nil, fmt.Errorf("cli executable path must be absolute path: %q", opts.CLI)
	}
	cleanCLI := filepath.Clean(rawCLI)
	cliStat, err := os.Stat(cleanCLI)
	if err != nil {
		return nil, fmt.Errorf("cli executable does not exist at %s: %w", cleanCLI, err)
	}
	if cliStat.IsDir() {
		return nil, fmt.Errorf("cli executable path is a directory: %s", cleanCLI)
	}
	// Bounded execution test for genuine executable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cleanCLI, "--help")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cli executable %s failed --help execution check: %w", cleanCLI, err)
	}

	// 4. Validate Static Identity 4-tuple
	if opts.AgentID == "" {
		return nil, errors.New("agent-id is required")
	}
	if containsWhitespace(opts.AgentID) {
		return nil, fmt.Errorf("agent-id contains illegal whitespace: %q", opts.AgentID)
	}
	if opts.ConnectionID == "" {
		return nil, errors.New("connection-id is required")
	}
	if containsWhitespace(opts.ConnectionID) {
		return nil, fmt.Errorf("connection-id contains illegal whitespace: %q", opts.ConnectionID)
	}
	if opts.ProfileName == "" {
		return nil, errors.New("profile-name is required")
	}
	if containsWhitespace(opts.ProfileName) {
		return nil, fmt.Errorf("profile-name contains illegal whitespace: %q", opts.ProfileName)
	}

	// Target paths
	binName := targetBinaryName(cleanCLI)
	skillDir := filepath.Join(cleanHome, "skills", "astrorder")
	scriptsDir := filepath.Join(skillDir, "scripts")
	skillFile := filepath.Join(skillDir, SkillMDFile)
	binaryFile := filepath.Join(scriptsDir, binName)
	callerFilePath := filepath.Join(cleanHome, CallerFile)
	manifestFile := filepath.Join(skillDir, ManifestFile)

	// 5. Symlink Escape Checks
	for _, target := range []string{callerFilePath, skillFile, binaryFile, manifestFile} {
		if err := checkSymlinkEscape(cleanHome, target); err != nil {
			return nil, err
		}
	}

	// 6. Pre-flight Conflict & Ownership Validation
	skillExists := fileExists(skillFile)
	binaryExists := fileExists(binaryFile)
	manifestExists := fileExists(manifestFile)
	callerExists := fileExists(callerFilePath)

	// User-owned skill/binary check: target exists but no astrorder manifest
	if skillExists && !manifestExists {
		return nil, fmt.Errorf("existing skill %s is user-owned (no astrorder manifest found); refusing to overwrite", skillFile)
	}
	if binaryExists && !manifestExists {
		return nil, fmt.Errorf("existing binary %s is user-owned (no astrorder manifest found); refusing to overwrite", binaryFile)
	}

	// Manifest-based checks
	expectedRelBin := filepath.ToSlash(filepath.Join("scripts", binName))
	allowedRelKeys := map[string]bool{
		SkillMDFile:    true,
		CallerFile:     true,
		expectedRelBin: true,
	}

	if manifestExists {
		existingManifest, err := readManifest(manifestFile)
		if err != nil {
			return nil, fmt.Errorf("corrupt manifest %s: %w; refusing to overwrite", manifestFile, err)
		}
		if existingManifest.FormatVersion != ManifestVersion {
			return nil, fmt.Errorf("manifest %s has unknown format version %d (expected %d); refusing to overwrite", manifestFile, existingManifest.FormatVersion, ManifestVersion)
		}
		if existingManifest.Owner != ManifestOwner {
			return nil, fmt.Errorf("manifest %s owner %q does not match %q; refusing to overwrite", manifestFile, existingManifest.Owner, ManifestOwner)
		}
		// Binding conflict check
		if existingManifest.Runtime != targetRuntime ||
			existingManifest.AgentID != opts.AgentID ||
			existingManifest.ConnectionID != opts.ConnectionID ||
			existingManifest.ProfileName != opts.ProfileName {
			return nil, fmt.Errorf("binding conflict: existing installation has (runtime=%s, agent_id=%s, connection_id=%s, profile_name=%s), requested (runtime=%s, agent_id=%s, connection_id=%s, profile_name=%s)",
				existingManifest.Runtime, existingManifest.AgentID, existingManifest.ConnectionID, existingManifest.ProfileName,
				targetRuntime, opts.AgentID, opts.ConnectionID, opts.ProfileName)
		}
		// CLI path conflict check
		if filepath.Clean(existingManifest.CLIPath) != cleanCLI {
			return nil, fmt.Errorf("cli path conflict: existing installation uses CLI %q, requested %q", existingManifest.CLIPath, cleanCLI)
		}
		// Whitelist relative paths in manifest
		for rel := range existingManifest.FileHashes {
			if !allowedRelKeys[rel] {
				return nil, fmt.Errorf("manifest %s contains unexpected file key %q; refusing to overwrite", manifestFile, rel)
			}
		}
		for rel := range allowedRelKeys {
			if _, ok := existingManifest.FileHashes[rel]; !ok {
				return nil, fmt.Errorf("manifest %s missing expected file hash for %q; refusing to overwrite", manifestFile, rel)
			}
		}
		// Verify old file hashes (reject if user modified)
		for relPath, expectedHash := range existingManifest.FileHashes {
			var checkPath string
			switch relPath {
			case SkillMDFile:
				checkPath = skillFile
			case CallerFile:
				checkPath = callerFilePath
			default:
				checkPath = filepath.Join(skillDir, filepath.FromSlash(relPath))
			}
			if fileExists(checkPath) {
				actualHash, err := hashFile(checkPath)
				if err != nil {
					return nil, fmt.Errorf("hash file %s: %w", checkPath, err)
				}
				if actualHash != expectedHash {
					return nil, fmt.Errorf("file %s has been modified by user (expected hash %s, got %s); refusing to overwrite", checkPath, expectedHash, actualHash)
				}
			}
		}
	}

	// Existing caller config validation
	if callerExists {
		existingCaller, err := readCallerConfig(callerFilePath)
		if err != nil {
			return nil, fmt.Errorf("existing caller config %s: %w", callerFilePath, err)
		}
		if existingCaller.Runtime != targetRuntime ||
			existingCaller.AgentID != opts.AgentID ||
			existingCaller.ConnectionID != opts.ConnectionID ||
			existingCaller.ProfileName != opts.ProfileName {
			return nil, fmt.Errorf("caller config %s already exists with different binding (agent_id=%s, connection_id=%s, profile_name=%s, runtime=%s)",
				callerFilePath, existingCaller.AgentID, existingCaller.ConnectionID, existingCaller.ProfileName, existingCaller.Runtime)
		}
	}

	// 7. Prepare Artifact Contents
	// Caller config: strictly 4 static fields, SID never persisted
	callerStruct := callerConfigFile{
		AgentID:      opts.AgentID,
		ConnectionID: opts.ConnectionID,
		ProfileName:  opts.ProfileName,
		Runtime:      targetRuntime,
	}
	callerBytes, err := json.MarshalIndent(callerStruct, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal caller config: %w", err)
	}
	callerBytes = append(callerBytes, '\n')

	// Skill markdown: rendered with concrete paths
	renderedSkill := renderSkillMD(EmbeddedSkillMD, targetRuntime, cleanHome)

	// Read source CLI executable bytes
	cliBytes, err := os.ReadFile(cleanCLI)
	if err != nil {
		return nil, fmt.Errorf("read source cli %s: %w", cleanCLI, err)
	}

	// Compute hashes
	hashSkill := hashBytes(renderedSkill)
	hashCLI := hashBytes(cliBytes)
	hashCaller := hashBytes(callerBytes)

	relBinPath := filepath.ToSlash(filepath.Join("scripts", binName))
	manifest := Manifest{
		FormatVersion: ManifestVersion,
		Owner:         ManifestOwner,
		Runtime:       targetRuntime,
		AgentID:       opts.AgentID,
		ConnectionID:  opts.ConnectionID,
		ProfileName:   opts.ProfileName,
		CLIPath:       cleanCLI,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		FileHashes: map[string]string{
			SkillMDFile: hashSkill,
			relBinPath:  hashCLI,
			CallerFile:  hashCaller,
		},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')

	// 8. Transactional Backup & Rollback Setup
	type targetBackup struct {
		path    string
		existed bool
		data    []byte
		perm    os.FileMode
	}

	targetsToManage := []string{callerFilePath, binaryFile, skillFile, manifestFile}
	backups := make(map[string]targetBackup, len(targetsToManage))
	for _, p := range targetsToManage {
		if stat, err := os.Stat(p); err == nil && !stat.IsDir() {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("backup existing file %s: %w", p, err)
			}
			backups[p] = targetBackup{
				path:    p,
				existed: true,
				data:    data,
				perm:    stat.Mode().Perm(),
			}
		} else {
			backups[p] = targetBackup{
				path:    p,
				existed: false,
			}
		}
	}

	var committed bool
	var writtenPaths []string
	defer func() {
		if !committed {
			// Roll back in reverse order of writing
			for i := len(writtenPaths) - 1; i >= 0; i-- {
				p := writtenPaths[i]
				b := backups[p]
				if b.existed {
					if err := atomicWriteFile(p, b.data, b.perm); err != nil {
						resultErr = errors.Join(resultErr, fmt.Errorf("rollback restore %s: %w", p, err))
					}
				} else {
					if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
						resultErr = errors.Join(resultErr, fmt.Errorf("rollback remove %s: %w", p, err))
					}
				}
			}
		}
	}()

	// 9. Safe Atomic Writes
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return nil, fmt.Errorf("create scripts directory %s: %w", scriptsDir, err)
	}

	if err := writeFile(callerFilePath, callerBytes, 0644); err != nil {
		return nil, fmt.Errorf("write caller config: %w", err)
	}
	writtenPaths = append(writtenPaths, callerFilePath)

	binPerm := os.FileMode(0755)
	if err := writeFile(binaryFile, cliBytes, binPerm); err != nil {
		return nil, fmt.Errorf("write cli binary: %w", err)
	}
	writtenPaths = append(writtenPaths, binaryFile)

	if err := writeFile(skillFile, renderedSkill, 0644); err != nil {
		return nil, fmt.Errorf("write skill file: %w", err)
	}
	writtenPaths = append(writtenPaths, skillFile)

	// Manifest write represents the commit step
	if err := writeFile(manifestFile, manifestBytes, 0644); err != nil {
		return nil, fmt.Errorf("write manifest file: %w", err)
	}
	writtenPaths = append(writtenPaths, manifestFile)

	// 10. Read-back Verification (读回核验)
	verifyCaller, err := readCallerConfig(callerFilePath)
	if err != nil {
		return nil, fmt.Errorf("verification failed for caller config: %w", err)
	}
	if verifyCaller.AgentID != opts.AgentID || verifyCaller.ConnectionID != opts.ConnectionID ||
		verifyCaller.ProfileName != opts.ProfileName || verifyCaller.Runtime != targetRuntime {
		return nil, fmt.Errorf("verification mismatch in caller config fields")
	}

	readSkillHash, err := hashFile(skillFile)
	if err != nil || readSkillHash != hashSkill {
		return nil, fmt.Errorf("verification failed for skill file hash: %w", err)
	}

	readCLIHash, err := hashFile(binaryFile)
	if err != nil || readCLIHash != hashCLI {
		return nil, fmt.Errorf("verification failed for cli binary hash: %w", err)
	}

	if _, err := readManifest(manifestFile); err != nil {
		return nil, fmt.Errorf("verification failed for manifest: %w", err)
	}

	committed = true

	return &InstallResult{
		OK:           true,
		Runtime:      targetRuntime,
		Home:         cleanHome,
		SkillDir:     skillDir,
		SkillFile:    skillFile,
		BinaryFile:   binaryFile,
		CallerConfig: callerFilePath,
		ManifestFile: manifestFile,
		AgentID:      opts.AgentID,
		ConnectionID: opts.ConnectionID,
		ProfileName:  opts.ProfileName,
	}, nil
}
