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
	"sort"
	"strings"
	"time"
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
	if IsReservedPluginID(manifest.ID) || m.isBuiltin(manifest.ID) || m.isOfficial(manifest.ID) {
		return PluginView{}, fmt.Errorf("%w: cannot overwrite builtin, official, or reserved plugin %q", ErrConflict, manifest.ID)
	}
	if err := ValidateManifestForSource(SourceExternal, manifest); err != nil {
		return PluginView{}, err
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

	stagedManifest, err := LoadManifestFile(filepath.Join(stageDir, "plugin.json"))
	if err != nil {
		return PluginView{}, err
	}
	if IsReservedPluginID(stagedManifest.ID) || m.isBuiltin(stagedManifest.ID) || m.isOfficial(stagedManifest.ID) {
		return PluginView{}, fmt.Errorf("%w: cannot overwrite builtin, official, or reserved plugin %q", ErrConflict, stagedManifest.ID)
	}
	if err := ValidateManifestForSource(SourceExternal, stagedManifest); err != nil {
		return PluginView{}, err
	}
	if err := m.checkExternalProcessPolicy(ctx, stagedManifest, stageDir); err != nil {
		return PluginView{}, err
	}

	return m.commitStagedPlugin(ctx, stagedManifest, &stageDir)
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
	if IsReservedPluginID(manifest.ID) || m.isBuiltin(manifest.ID) || m.isOfficial(manifest.ID) {
		return PluginView{}, fmt.Errorf("%w: cannot overwrite builtin, official, or reserved plugin %q", ErrConflict, manifest.ID)
	}
	if err := ValidateManifestForSource(SourceExternal, manifest); err != nil {
		return PluginView{}, err
	}
	if err := m.checkExternalProcessPolicy(ctx, manifest, stageDir); err != nil {
		return PluginView{}, err
	}

	return m.commitStagedPlugin(ctx, manifest, &stageDir)
}

func (m *Manager) renamePath(oldPath, newPath string) error {
	if m.renameFn != nil {
		return m.renameFn(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

// commitStagedPlugin performs an atomic, rollback-safe swap:
// 1. Validate policy & target containment.
// 2. Stop any running instance of the plugin.
// 3. If an existing installation exists, move it aside to .<id>.bak-<ts>-<rand> (never delete first).
// 4. Move the validated staging dir into targetDir; on failure automatically restore backupDir -> targetDir.
// 5. Persist disabled state; on failure roll back targetDir and restore backupDir -> targetDir.
// 6. Clean up backupDir only after commit succeeds.
func (m *Manager) commitStagedPlugin(ctx context.Context, manifest Manifest, stageDirPtr *string) (PluginView, error) {
	stageDir := *stageDirPtr
	if IsReservedPluginID(manifest.ID) || m.isBuiltin(manifest.ID) || m.isOfficial(manifest.ID) {
		return PluginView{}, fmt.Errorf("%w: cannot overwrite builtin, official, or reserved plugin %q", ErrConflict, manifest.ID)
	}
	if err := ValidateManifestForSource(SourceExternal, manifest); err != nil {
		return PluginView{}, err
	}
	if err := m.checkExternalProcessPolicy(ctx, manifest, stageDir); err != nil {
		return PluginView{}, err
	}
	targetDir := filepath.Join(m.pluginsDir, manifest.ID)
	if !isPathStrictlyInside(m.pluginsDir, targetDir) {
		return PluginView{}, fmt.Errorf("%w: invalid target plugin path for %q", ErrInvalid, manifest.ID)
	}

	// Stop existing process if upgrading an already-installed external plugin.
	m.supervisor.Stop(manifest.ID)

	var backupDir string
	if _, statErr := os.Stat(targetDir); statErr == nil {
		backupDir = makeBackupDirPath(m.pluginsDir, manifest.ID)
		if !isPathStrictlyInside(m.pluginsDir, backupDir) {
			return PluginView{}, fmt.Errorf("%w: invalid backup plugin path for %q", ErrInvalid, manifest.ID)
		}
		if err := m.renamePath(targetDir, backupDir); err != nil {
			return PluginView{}, fmt.Errorf("backup previous plugin directory: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return PluginView{}, fmt.Errorf("inspect previous plugin directory: %w", statErr)
	}

	if err := m.renamePath(stageDir, targetDir); err != nil {
		if backupDir != "" {
			if rbErr := m.renamePath(backupDir, targetDir); rbErr != nil {
				m.logger.Error("failed to roll back plugin directory after swap failure", "plugin_id", manifest.ID, "backup_dir", backupDir, "error", rbErr)
				return PluginView{}, fmt.Errorf("finalize plugin install: %w (rollback also failed: %v)", err, rbErr)
			}
			m.logger.Warn("rolled back plugin directory to previous version after swap failure", "plugin_id", manifest.ID, "error", err)
		}
		return PluginView{}, fmt.Errorf("finalize plugin install: %w", err)
	}
	*stageDirPtr = "" // Successfully moved into targetDir; prevent defer cleanup of targetDir.

	// Never auto-enable untrusted external code on install!
	if err := m.storage.SetEnabled(ctx, manifest.ID, false); err != nil {
		// Roll back filesystem swap if state persistence fails.
		failedStaging := makeBackupDirPath(m.pluginsDir, manifest.ID+"-failed")
		if mvErr := os.Rename(targetDir, failedStaging); mvErr == nil {
			_ = os.RemoveAll(failedStaging)
		} else {
			_ = os.RemoveAll(targetDir)
		}
		if backupDir != "" {
			if rbErr := m.renamePath(backupDir, targetDir); rbErr != nil {
				m.logger.Error("failed to restore plugin backup after state persistence error", "plugin_id", manifest.ID, "backup_dir", backupDir, "error", rbErr)
			}
		}
		return PluginView{}, fmt.Errorf("persist disabled state for %q: %w", manifest.ID, err)
	}

	digest, digestErr := ComputePluginContentDigest(targetDir)
	if digestErr != nil {
		m.logger.Warn("failed to compute plugin content digest after install", "plugin_id", manifest.ID, "error", digestErr)
	}
	sigVerified := verifyPluginSignature(manifest, targetDir, m.trustedPublicKeys)

	m.mu.Lock()
	_, existed := m.externals[manifest.ID]
	m.externals[manifest.ID] = &externalPlugin{
		manifest:          manifest,
		dir:               targetDir,
		source:            SourceExternal,
		contentDigest:     digest,
		signatureVerified: sigVerified,
	}
	m.mu.Unlock()

	if backupDir != "" {
		if rmErr := os.RemoveAll(backupDir); rmErr != nil {
			m.logger.Warn("failed to clean up previous plugin backup directory", "plugin_id", manifest.ID, "backup_dir", backupDir, "error", rmErr)
		}
	}

	action := "installed"
	if existed || backupDir != "" {
		action = "updated"
	}
	disabled := false
	m.notifySkillsChanged()
	m.notifyChanged(ExtensionChangeEvent{
		Action:        action,
		PluginID:      manifest.ID,
		Source:        SourceExternal,
		Enabled:       &disabled,
		ContentDigest: digest,
	})

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
	if IsReservedPluginID(cleanID) || m.isBuiltin(cleanID) || m.isOfficial(cleanID) {
		return fmt.Errorf("%w: builtin, official, or reserved plugin %q cannot be uninstalled", ErrConflict, cleanID)
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

	if targetDir != "" {
		inPrimary := m.pluginsDir != "" && isPathStrictlyInside(m.pluginsDir, targetDir)
		inLegacy := m.legacyPluginsDir != "" && isPathStrictlyInside(m.legacyPluginsDir, targetDir) && !IsReservedPluginID(filepath.Base(targetDir))
		if inPrimary || inLegacy {
			if err := os.RemoveAll(targetDir); err != nil {
				return fmt.Errorf("remove plugin directory: %w", err)
			}
		}
	}
	if err := m.storage.CleanupPlugin(ctx, cleanID); err != nil {
		return fmt.Errorf("cleanup plugin storage: %w", err)
	}
	m.notifySkillsChanged()
	m.notifyChanged(ExtensionChangeEvent{
		Action:   "uninstalled",
		PluginID: cleanID,
		Source:   SourceExternal,
	})
	return nil
}

// Reload rescans OfficialPluginsDir, <PluginsDir>/*/plugin.json, and optional
// read-only LegacyPluginsDir, refreshing both standalone disk plugins and hybrid
// builtin ESM frontends, updating content digests, and emitting ExtensionChangeEvent.
func (m *Manager) Reload(ctx context.Context) error {
	return m.ReloadExternal(ctx)
}

// ReloadExternal rescans OfficialPluginsDir, <PluginsDir>/*/plugin.json, and optional
// read-only LegacyPluginsDir (while strictly skipping builtin directories such as r2t2
// and never mixing official plugins into writable external directories), updates content
// digests (including hybrid builtin official ESM frontends), stops removed or
// policy-disallowed plugins, starts enabled process plugins, and emits an ExtensionChangeEvent.
func (m *Manager) ReloadExternal(ctx context.Context) error {
	return m.reloadDiskPlugins(ctx, true)
}

func (m *Manager) reloadDiskPlugins(ctx context.Context, emitChange bool) error {
	if strings.TrimSpace(m.officialPluginsDir) == "" && strings.TrimSpace(m.pluginsDir) == "" && strings.TrimSpace(m.legacyPluginsDir) == "" {
		return nil
	}

	rawOfficials := map[string]*externalPlugin{}
	discoveredExternals := map[string]*externalPlugin{}

	scanDir := func(root string, source string, dest map[string]*externalPlugin, blocked map[string]*externalPlugin) error {
		if strings.TrimSpace(root) == "" {
			return nil
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("read plugins dir %q: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || isIgnoredPluginDirName(entry.Name()) {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			manifestPath := filepath.Join(dir, "plugin.json")
			if _, statErr := os.Stat(manifestPath); statErr != nil {
				continue
			}
			manifest, loadErr := LoadManifestFile(manifestPath)
			if loadErr != nil {
				m.logger.Warn("skipping invalid plugin manifest", "source", source, "dir", dir, "error", loadErr)
				continue
			}
			if sourceErr := ValidateManifestForSource(source, manifest); sourceErr != nil {
				m.logger.Warn("skipping plugin violating source manifest rules", "source", source, "id", manifest.ID, "dir", dir, "error", sourceErr)
				continue
			}
			if IsReservedPluginID(manifest.ID) {
				m.logger.Warn("skipping plugin colliding with reserved id", "source", source, "id", manifest.ID, "dir", dir)
				continue
			}
			// External plugins can NEVER shadow or hijack a registered builtin plugin.
			// OfficialPluginsDir entries matching a builtin ID are partitioned below under m.mu.Lock()
			// so hybrid builtin + official ESM frontends bind cleanly regardless of registration order.
			if source != SourceOfficial && m.isBuiltin(manifest.ID) {
				m.logger.Warn("skipping external plugin colliding with builtin id", "source", source, "id", manifest.ID, "dir", dir)
				continue
			}
			if blocked != nil {
				if _, shadowed := blocked[manifest.ID]; shadowed {
					m.logger.Warn("skipping external plugin colliding with official plugin id", "id", manifest.ID, "dir", dir)
					continue
				}
			}
			if _, already := dest[manifest.ID]; already {
				continue
			}
			digest, digestErr := ComputePluginContentDigest(dir)
			if digestErr != nil {
				m.logger.Warn("failed to compute plugin content digest", "id", manifest.ID, "dir", dir, "error", digestErr)
			}
			sigVerified := verifyPluginSignature(manifest, dir, m.trustedPublicKeys)
			dest[manifest.ID] = &externalPlugin{
				manifest:          manifest,
				dir:               dir,
				source:            source,
				contentDigest:     digest,
				signatureVerified: sigVerified,
			}
		}
		return nil
	}

	if m.officialPluginsDir != "" {
		if err := scanDir(m.officialPluginsDir, SourceOfficial, rawOfficials, nil); err != nil {
			m.logger.Warn("official plugins directory scan failed", "dir", m.officialPluginsDir, "error", err)
		}
	}
	if m.pluginsDir != "" && filepath.Clean(m.pluginsDir) != filepath.Clean(m.officialPluginsDir) {
		if err := scanDir(m.pluginsDir, SourceExternal, discoveredExternals, rawOfficials); err != nil {
			return err
		}
	}
	if m.legacyPluginsDir != "" &&
		filepath.Clean(m.legacyPluginsDir) != filepath.Clean(m.pluginsDir) &&
		filepath.Clean(m.legacyPluginsDir) != filepath.Clean(m.officialPluginsDir) {
		if err := scanDir(m.legacyPluginsDir, SourceExternal, discoveredExternals, rawOfficials); err != nil {
			m.logger.Warn("legacy plugins directory scan failed", "dir", m.legacyPluginsDir, "error", err)
		}
	}

	changedSet := map[string]struct{}{}
	discoveredOfficials := map[string]*externalPlugin{}
	discoveredBuiltinFrontends := map[string]*externalPlugin{}
	m.mu.Lock()
	for id, plug := range rawOfficials {
		if _, isB := m.builtins[id]; isB {
			if IsFrontendESM(plug.manifest) {
				discoveredBuiltinFrontends[id] = plug
			} else {
				m.logger.Warn("skipping non-ESM official plugin colliding with builtin id", "id", id, "dir", plug.dir)
			}
			continue
		}
		discoveredOfficials[id] = plug
	}
	for id := range m.builtins {
		delete(discoveredExternals, id)
	}

	var removed []string
	for oldID, oldPlug := range m.officials {
		nextPlug, stillPresent := discoveredOfficials[oldID]
		if !stillPresent {
			removed = append(removed, oldID)
			changedSet[oldID] = struct{}{}
		} else if nextPlug.contentDigest != oldPlug.contentDigest || nextPlug.manifest.Version != oldPlug.manifest.Version {
			changedSet[oldID] = struct{}{}
		}
	}
	for newID := range discoveredOfficials {
		if _, existed := m.officials[newID]; !existed {
			changedSet[newID] = struct{}{}
		}
	}
	for oldID, oldPlug := range m.builtinFrontends {
		nextPlug, stillPresent := discoveredBuiltinFrontends[oldID]
		if !stillPresent {
			changedSet[oldID] = struct{}{}
		} else if nextPlug.contentDigest != oldPlug.contentDigest || nextPlug.manifest.Version != oldPlug.manifest.Version {
			changedSet[oldID] = struct{}{}
		}
	}
	for newID := range discoveredBuiltinFrontends {
		if _, existed := m.builtinFrontends[newID]; !existed {
			changedSet[newID] = struct{}{}
		}
	}
	for oldID, oldPlug := range m.externals {
		nextPlug, stillPresent := discoveredExternals[oldID]
		if !stillPresent {
			removed = append(removed, oldID)
			changedSet[oldID] = struct{}{}
		} else if nextPlug.contentDigest != oldPlug.contentDigest || nextPlug.manifest.Version != oldPlug.manifest.Version {
			changedSet[oldID] = struct{}{}
		}
	}
	for newID := range discoveredExternals {
		if _, existed := m.externals[newID]; !existed {
			changedSet[newID] = struct{}{}
		}
	}
	m.officials = discoveredOfficials
	m.builtinFrontends = discoveredBuiltinFrontends
	m.externals = discoveredExternals
	m.mu.Unlock()

	for _, id := range removed {
		m.supervisor.Stop(id)
	}

	// Auto-start enabled, permission-granted, and policy-allowed process plugins.
	enabledMap, _ := m.storage.GetEnabledMap(ctx)
	permMap, _ := m.storage.GetAllGrantedPermissions(ctx)
	startCandidates := make([]*externalPlugin, 0, len(discoveredOfficials)+len(discoveredExternals))
	for _, p := range discoveredOfficials {
		startCandidates = append(startCandidates, p)
	}
	for _, p := range discoveredExternals {
		startCandidates = append(startCandidates, p)
	}
	for _, plug := range startCandidates {
		if plug.manifest.Backend.Type != BackendTypeProcess {
			continue
		}
		if !isPluginEnabledInMap(enabledMap, plug.manifest.ID, EffectiveDefaultEnabled(plug.source, plug.manifest)) {
			continue
		}
		granted, pending := computePermissionState(plug.manifest.Permissions, permMap[plug.manifest.ID])
		if len(pending) > 0 {
			continue
		}
		if plug.source == SourceExternal {
			if policyErr := m.checkExternalProcessPolicy(ctx, plug.manifest, plug.dir); policyErr != nil {
				m.supervisor.Stop(plug.manifest.ID)
				m.logger.Warn("skipping auto-start of external process plugin due to security policy", "plugin_id", plug.manifest.ID, "error", policyErr)
				continue
			}
		}
		if err := m.supervisor.Start(ctx, plug.manifest, plug.dir, granted); err != nil {
			m.logger.Warn("failed to start enabled plugin on reload", "plugin_id", plug.manifest.ID, "error", err)
		}
	}

	if emitChange {
		changedIDs := make([]string, 0, len(changedSet))
		for id := range changedSet {
			changedIDs = append(changedIDs, id)
		}
		sort.Strings(changedIDs)
		ev := ExtensionChangeEvent{
			Action:     "reloaded",
			ChangedIDs: changedIDs,
		}
		if len(changedIDs) == 1 {
			ev.PluginID = changedIDs[0]
			if plug, ok := m.lookupDiskPlugin(changedIDs[0]); ok {
				ev.Source = plug.source
				ev.ContentDigest = plug.contentDigest
			} else if bf, ok := m.lookupBuiltinFrontend(changedIDs[0]); ok {
				ev.Source = SourceBuiltin
				ev.ContentDigest = bf.contentDigest
			} else if m.isBuiltin(changedIDs[0]) {
				ev.Source = SourceBuiltin
			}
		}
		m.notifySkillsChanged()
		m.notifyChanged(ev)
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

func makeBackupDirPath(pluginsDir, id string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return filepath.Join(pluginsDir, fmt.Sprintf(".%s.bak-%d-%s", id, time.Now().UTC().UnixNano(), hex.EncodeToString(b)))
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
