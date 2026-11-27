package pluginhost

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxPluginFiles        = 2000
	maxPluginTotalBytes   = 100 << 20 // 100 MiB uncompressed safety cap
	maxSinglePluginFileSz = 32 << 20  // 32 MiB per file cap
)

// InstallFromDir validates and installs an external plugin from a local directory
// into <PluginsDir>/<id>/, leaving it disabled until the user explicitly grants
// permissions and enables it.
func (m *Manager) InstallFromDir(ctx context.Context, srcDir string) (PluginView, error) {
	if strings.TrimSpace(m.pluginsDir) == "" {
		return PluginView{}, fmt.Errorf("%w: plugins directory is not configured", ErrInvalid)
	}
	cleanSrc := filepath.Clean(strings.TrimSpace(srcDir))
	info, err := os.Stat(cleanSrc)
	if err != nil {
		return PluginView{}, fmt.Errorf("%w: source directory %q is inaccessible: %v", ErrInvalid, srcDir, err)
	}
	if !info.IsDir() {
		return PluginView{}, fmt.Errorf("%w: source path %q is not a directory", ErrInvalid, srcDir)
	}

	manifest, err := LoadManifestFile(filepath.Join(cleanSrc, "plugin.json"))
	if err != nil {
		return PluginView{}, err
	}
	if m.isBuiltin(manifest.ID) {
		return PluginView{}, fmt.Errorf("%w: cannot overwrite builtin plugin %q", ErrConflict, manifest.ID)
	}

	if err := os.MkdirAll(m.pluginsDir, 0o755); err != nil {
		return PluginView{}, fmt.Errorf("create plugins dir: %w", err)
	}
	stageDir, err := createStagingDir(m.pluginsDir, manifest.ID)
	if err != nil {
		return PluginView{}, err
	}
	defer func() {
		if stageDir != "" {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if err := copyDirSafely(cleanSrc, stageDir); err != nil {
		return PluginView{}, err
	}

	return m.commitStagedPlugin(ctx, manifest, &stageDir)
}

// InstallFromZip validates and installs an external plugin from a .zip / .astrpkg archive
// with strict zip-slip protection, symlink rejection, and size/file limits.
func (m *Manager) InstallFromZip(ctx context.Context, r io.ReaderAt, size int64) (PluginView, error) {
	if strings.TrimSpace(m.pluginsDir) == "" {
		return PluginView{}, fmt.Errorf("%w: plugins directory is not configured", ErrInvalid)
	}
	if r == nil || size <= 0 {
		return PluginView{}, fmt.Errorf("%w: empty plugin archive", ErrInvalid)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return PluginView{}, fmt.Errorf("%w: invalid zip archive: %v", ErrInvalid, err)
	}
	if len(zr.File) == 0 {
		return PluginView{}, fmt.Errorf("%w: zip archive is empty", ErrInvalid)
	}
	if len(zr.File) > maxPluginFiles {
		return PluginView{}, fmt.Errorf("%w: zip archive exceeds maximum file count (%d)", ErrInvalid, maxPluginFiles)
	}

	if err := os.MkdirAll(m.pluginsDir, 0o755); err != nil {
		return PluginView{}, fmt.Errorf("create plugins dir: %w", err)
	}
	stageDir, err := createStagingDir(m.pluginsDir, "zip")
	if err != nil {
		return PluginView{}, err
	}
	defer func() {
		if stageDir != "" {
			_ = os.RemoveAll(stageDir)
		}
	}()

	stripPrefix := detectZipStripPrefix(zr.File)

	var totalBytes int64
	for _, f := range zr.File {
		if isIgnoredArchiveEntry(f.Name) {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return PluginView{}, fmt.Errorf("%w: symlinks are not allowed in plugin archives (%q)", ErrInvalid, f.Name)
		}

		rel, err := sanitizeZipEntryPath(f.Name, stripPrefix)
		if err != nil {
			return PluginView{}, fmt.Errorf("%w: unsafe archive entry %q: %v", ErrInvalid, f.Name, err)
		}
		if rel == "" {
			continue
		}

		destPath := filepath.Join(stageDir, filepath.FromSlash(rel))
		if !isPathStrictlyInside(stageDir, destPath) {
			return PluginView{}, fmt.Errorf("%w: archive entry %q escapes staging directory", ErrInvalid, f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return PluginView{}, err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return PluginView{}, err
		}

		rc, err := f.Open()
		if err != nil {
			return PluginView{}, fmt.Errorf("open zip entry %q: %w", f.Name, err)
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		mode &= 0o755
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return PluginView{}, err
		}
		written, copyErr := io.Copy(out, io.LimitReader(rc, maxSinglePluginFileSz+1))
		closeOutErr := out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return PluginView{}, copyErr
		}
		if closeOutErr != nil {
			return PluginView{}, closeOutErr
		}
		if written > maxSinglePluginFileSz {
			return PluginView{}, fmt.Errorf("%w: archive entry %q exceeds single file limit", ErrInvalid, f.Name)
		}
		totalBytes += written
		if totalBytes > maxPluginTotalBytes {
			return PluginView{}, fmt.Errorf("%w: archive exceeds maximum uncompressed size", ErrInvalid)
		}
	}

	manifest, err := LoadManifestFile(filepath.Join(stageDir, "plugin.json"))
	if err != nil {
		return PluginView{}, err
	}
	if m.isBuiltin(manifest.ID) {
		return PluginView{}, fmt.Errorf("%w: cannot overwrite builtin plugin %q", ErrConflict, manifest.ID)
	}

	return m.commitStagedPlugin(ctx, manifest, &stageDir)
}

func (m *Manager) commitStagedPlugin(ctx context.Context, manifest Manifest, stageDirPtr *string) (PluginView, error) {
	stageDir := *stageDirPtr
	targetDir := filepath.Join(m.pluginsDir, manifest.ID)
	if !isPathStrictlyInside(m.pluginsDir, targetDir) {
		return PluginView{}, fmt.Errorf("%w: invalid target plugin path for %q", ErrInvalid, manifest.ID)
	}

	// Stop existing process if upgrading an already-installed external plugin.
	m.supervisor.Stop(manifest.ID)

	if err := os.RemoveAll(targetDir); err != nil {
		return PluginView{}, fmt.Errorf("remove previous plugin directory: %w", err)
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		return PluginView{}, fmt.Errorf("finalize plugin install: %w", err)
	}
	*stageDirPtr = "" // Successfully moved; prevent defer cleanup.

	// Never auto-enable untrusted external code on install!
	if err := m.storage.SetEnabled(ctx, manifest.ID, false); err != nil {
		return PluginView{}, fmt.Errorf("persist disabled state for %q: %w", manifest.ID, err)
	}

	m.mu.Lock()
	m.externals[manifest.ID] = &externalPlugin{
		manifest: manifest,
		dir:      targetDir,
	}
	m.mu.Unlock()

	view, err := m.GetPlugin(ctx, manifest.ID)
	if err != nil {
		return PluginView{}, err
	}
	return view, nil
}

// Uninstall stops a running external plugin, deletes its directory, and purges all stored state.
func (m *Manager) Uninstall(ctx context.Context, id string) error {
	cleanID := strings.TrimSpace(id)
	if err := ValidatePluginID(cleanID); err != nil {
		return err
	}
	if m.isBuiltin(cleanID) {
		return fmt.Errorf("%w: builtin plugin %q cannot be uninstalled", ErrConflict, cleanID)
	}

	m.mu.Lock()
	ext, exists := m.externals[cleanID]
	delete(m.externals, cleanID)
	m.mu.Unlock()

	targetDir := ""
	if ext != nil {
		targetDir = ext.dir
	} else if m.pluginsDir != "" {
		candidate := filepath.Join(m.pluginsDir, cleanID)
		if _, err := os.Stat(candidate); err == nil {
			targetDir = candidate
			exists = true
		}
	}
	if !exists {
		return fmt.Errorf("%w: plugin %q", ErrNotFound, cleanID)
	}

	m.supervisor.Stop(cleanID)

	if targetDir != "" && m.pluginsDir != "" && isPathStrictlyInside(m.pluginsDir, targetDir) {
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("remove plugin directory: %w", err)
		}
	}
	if err := m.storage.CleanupPlugin(ctx, cleanID); err != nil {
		return fmt.Errorf("cleanup plugin storage: %w", err)
	}
	m.notifySkillsChanged()
	return nil
}

// ReloadExternal rescans <PluginsDir>/*/plugin.json, updates the registry, stops
// removed plugins, and starts enabled & permission-granted process plugins.
func (m *Manager) ReloadExternal(ctx context.Context) error {
	if strings.TrimSpace(m.pluginsDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(m.pluginsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			m.mu.Lock()
			oldIDs := make([]string, 0, len(m.externals))
			for id := range m.externals {
				oldIDs = append(oldIDs, id)
			}
			m.externals = map[string]*externalPlugin{}
			m.mu.Unlock()
			for _, id := range oldIDs {
				m.supervisor.Stop(id)
			}
			return nil
		}
		return fmt.Errorf("read plugins dir: %w", err)
	}

	discovered := map[string]*externalPlugin{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(m.pluginsDir, entry.Name())
		manifestPath := filepath.Join(dir, "plugin.json")
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			continue
		}
		manifest, loadErr := LoadManifestFile(manifestPath)
		if loadErr != nil {
			m.logger.Warn("skipping invalid external plugin manifest", "dir", dir, "error", loadErr)
			continue
		}
		if m.isBuiltin(manifest.ID) {
			m.logger.Warn("skipping external plugin colliding with builtin id", "id", manifest.ID, "dir", dir)
			continue
		}
		discovered[manifest.ID] = &externalPlugin{
			manifest: manifest,
			dir:      dir,
		}
	}

	m.mu.Lock()
	var removed []string
	for oldID := range m.externals {
		if _, stillPresent := discovered[oldID]; !stillPresent {
			removed = append(removed, oldID)
		}
	}
	m.externals = discovered
	m.mu.Unlock()

	for _, id := range removed {
		m.supervisor.Stop(id)
	}

	// Auto-start enabled & permission-granted process plugins.
	enabledMap, _ := m.storage.GetEnabledMap(ctx)
	permMap, _ := m.storage.GetAllGrantedPermissions(ctx)
	for _, ext := range discovered {
		if ext.manifest.Backend.Type != BackendTypeProcess {
			continue
		}
		if !enabledMap[ext.manifest.ID] {
			continue
		}
		_, pending := computePermissionState(ext.manifest.Permissions, permMap[ext.manifest.ID])
		if len(pending) > 0 {
			continue
		}
		if err := m.supervisor.Start(ctx, ext.manifest, ext.dir); err != nil {
			m.logger.Warn("failed to start enabled external plugin on reload", "plugin_id", ext.manifest.ID, "error", err)
		}
	}

	return nil
}

func createStagingDir(pluginsDir, prefix string) (string, error) {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	dir := filepath.Join(pluginsDir, ".staging-"+prefix+"-"+hex.EncodeToString(b))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	return dir, nil
}

func copyDirSafely(srcRoot, dstRoot string) error {
	absSrc, err := filepath.Abs(srcRoot)
	if err != nil {
		return err
	}
	var fileCount int
	var totalBytes int64
	return filepath.WalkDir(absSrc, func(curr string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlinks are not allowed in plugin directories (%q)", ErrInvalid, curr)
		}
		rel, err := filepath.Rel(absSrc, curr)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, "..") {
			return fmt.Errorf("%w: path escapes source directory", ErrInvalid)
		}
		dest := filepath.Join(dstRoot, rel)
		if !isPathStrictlyInside(dstRoot, dest) {
			return fmt.Errorf("%w: destination path escapes staging directory", ErrInvalid)
		}
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		fileCount++
		if fileCount > maxPluginFiles {
			return fmt.Errorf("%w: plugin directory exceeds maximum file count (%d)", ErrInvalid, maxPluginFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSinglePluginFileSz {
			return fmt.Errorf("%w: file %q exceeds single file limit", ErrInvalid, rel)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		in, err := os.Open(curr)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = in.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, maxSinglePluginFileSz+1))
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		totalBytes += n
		if totalBytes > maxPluginTotalBytes {
			return fmt.Errorf("%w: plugin exceeds maximum total size", ErrInvalid)
		}
		return nil
	})
}

func isIgnoredArchiveEntry(name string) bool {
	trimmed := strings.TrimSpace(name)
	return trimmed == "" || strings.HasPrefix(trimmed, "__MACOSX/") || trimmed == "__MACOSX"
}

func detectZipStripPrefix(files []*zip.File) string {
	hasRootManifest := false
	topDirs := map[string]struct{}{}
	for _, f := range files {
		if isIgnoredArchiveEntry(f.Name) {
			continue
		}
		norm := strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "./")
		if norm == "plugin.json" {
			hasRootManifest = true
			break
		}
		parts := strings.Split(strings.TrimSuffix(norm, "/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			topDirs[parts[0]] = struct{}{}
		}
	}
	if hasRootManifest || len(topDirs) != 1 {
		return ""
	}
	var only string
	for k := range topDirs {
		only = k
	}
	// Verify <only>/plugin.json actually exists before stripping.
	target := only + "/plugin.json"
	for _, f := range files {
		norm := strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "./")
		if norm == target {
			return only + "/"
		}
	}
	return ""
}

func sanitizeZipEntryPath(rawName, stripPrefix string) (string, error) {
	if strings.ContainsRune(rawName, 0) {
		return "", errors.New("null byte in path")
	}
	if strings.Contains(rawName, "\\") {
		return "", errors.New("backslash not allowed in archive entry")
	}
	trimmed := strings.TrimSpace(rawName)
	if strings.HasPrefix(trimmed, "/") || filepath.IsAbs(trimmed) {
		return "", errors.New("absolute path not allowed")
	}
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		return "", errors.New("drive-qualified path not allowed")
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return "", errors.New("path traversal '..' not allowed")
		}
	}
	norm := strings.TrimPrefix(trimmed, "./")
	if stripPrefix != "" {
		if norm == strings.TrimSuffix(stripPrefix, "/") || norm == stripPrefix {
			return "", nil
		}
		norm = strings.TrimPrefix(norm, stripPrefix)
	}
	norm = strings.TrimSuffix(norm, "/")
	if norm == "" || norm == "." {
		return "", nil
	}
	cleaned := path.Clean(norm)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", errors.New("cleaned path escapes root")
	}
	return cleaned, nil
}

func isPathStrictlyInside(baseDir, candidate string) bool {
	absBase, err1 := filepath.Abs(baseDir)
	absCand, err2 := filepath.Abs(candidate)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absCand)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}
