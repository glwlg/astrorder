package pluginhost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

type cachedPluginSignature struct {
	fingerprint string
	sigKey      string
	verified    bool
}

const (
	prefAppProfileKey           = "app-profile"
	prefPluginSecurityKey       = "plugin-security"
	prefFallbackAppProfile      = "app:profile"
	signatureDomainPrefixV1     = "astrorder-plugin-sig-v1\n"
	contentDigestDomainPrefixV1 = "astrorder-plugin-digest-v1\n"
)

var reservedPluginIDs = map[string]struct{}{
	"_sdk":             {},
	"r2t2":             {},
	"r2t2-local-voice": {},
	"external":         {},
	"official":         {},
}

// IsReservedPluginID reports whether id is reserved by built-in runtime subsystems
// (such as the built-in R2T2 audio plugin or the external plugins container directory).
func IsReservedPluginID(id string) bool {
	_, ok := reservedPluginIDs[strings.ToLower(strings.TrimSpace(id))]
	return ok
}

// isIgnoredPluginDirName reports whether a directory entry inside a plugins root
// must be skipped during external plugin discovery (hidden/staging dirs, rollback
// backup dirs, or reserved builtin directories like r2t2).
func isIgnoredPluginDirName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || strings.HasPrefix(trimmed, ".") {
		return true
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, ".bak-") {
		return true
	}
	return IsReservedPluginID(lower)
}

func normalizeAppProfile(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "office", "normal", "basic", "standard":
		return AppProfileOffice
	case "developer", "advanced", "dev":
		return AppProfileDeveloper
	default:
		return ""
	}
}

// EffectiveAppProfile returns the active global app profile ("office" or "developer").
// Defaults to "developer" when unset so existing local developer environments remain unaffected.
func (m *Manager) EffectiveAppProfile(ctx context.Context) string {
	if p := normalizeAppProfile(m.appProfileOverride); p != "" {
		return p
	}
	if m.store != nil {
		if pref, err := m.store.GetPreference(ctx, store.PrefPluginsConfig); err == nil && pref.Exists {
			if cfgMap, ok := pref.Value.(map[string]any); ok {
				if appProf, ok := cfgMap[prefAppProfileKey].(map[string]any); ok {
					if mode, ok := appProf["mode"].(string); ok {
						if p := normalizeAppProfile(mode); p != "" {
							return p
						}
					}
				}
			}
		}
		if pref, err := m.store.GetPreference(ctx, prefFallbackAppProfile); err == nil && pref.Exists {
			if mode, ok := pref.Value.(string); ok {
				if p := normalizeAppProfile(mode); p != "" {
					return p
				}
			}
		}
	}
	if p := normalizeAppProfile(os.Getenv("ASTRORDER_APP_PROFILE")); p != "" {
		return p
	}
	return AppProfileDeveloper
}

func (m *Manager) readSecurityPolicyBool(ctx context.Context, key string) (explicit bool, allowed bool) {
	if m.store == nil {
		return false, false
	}
	pref, err := m.store.GetPreference(ctx, store.PrefPluginsConfig)
	if err != nil || !pref.Exists {
		return false, false
	}
	cfgMap, ok := pref.Value.(map[string]any)
	if !ok {
		return false, false
	}
	secMap, ok := cfgMap[prefPluginSecurityKey].(map[string]any)
	if !ok {
		return false, false
	}
	if v, ok := secMap[key].(bool); ok {
		return true, v
	}
	return false, false
}

func (m *Manager) readSecurityPolicyAllowUnsigned(ctx context.Context) (explicit bool, allowed bool) {
	return m.readSecurityPolicyBool(ctx, "allow_unsigned_process_plugins")
}

func (m *Manager) readSecurityPolicyAllowUnsignedESM(ctx context.Context) (explicit bool, allowed bool) {
	return m.readSecurityPolicyBool(ctx, "allow_unsigned_esm_plugins")
}

// AllowUnsignedProcessPlugins reports whether unsigned local external process plugins
// may be installed or started under the current global appProfile and security policy.
func (m *Manager) AllowUnsignedProcessPlugins(ctx context.Context) (bool, string) {
	if explicit, allowed := m.readSecurityPolicyAllowUnsigned(ctx); explicit && !allowed {
		return false, "unsigned external process plugins are disabled by security policy"
	}
	profile := m.EffectiveAppProfile(ctx)
	if profile != AppProfileDeveloper {
		return false, fmt.Sprintf("unsigned external process plugins are blocked in %s mode; switch to developer/advanced mode to install or run local process plugins", profile)
	}
	return true, ""
}

// AllowUnsignedESMPlugins reports whether unsigned external same-page ESM plugins
// may be enabled or loaded under the current global appProfile and security policy.
func (m *Manager) AllowUnsignedESMPlugins(ctx context.Context) (bool, string) {
	if explicit, allowed := m.readSecurityPolicyAllowUnsignedESM(ctx); explicit && !allowed {
		return false, "unsigned external ESM plugins are disabled by security policy"
	}
	profile := m.EffectiveAppProfile(ctx)
	if profile != AppProfileDeveloper {
		return false, fmt.Sprintf("unsigned external in-page ESM plugins are blocked in %s mode; only official or Ed25519-signed plugins are allowed", profile)
	}
	return true, ""
}

// effectiveRequiredPermissions returns the permissions that must be granted before
// the plugin may run. Unsigned external ESM plugins always require explicit ui:slots
// consent because they execute inside the host page context.
func effectiveRequiredPermissions(source string, manifest Manifest, sigVerified bool) []string {
	perms := nonNilStrings(manifest.Permissions)
	if source == SourceExternal && IsFrontendESM(manifest) && !sigVerified {
		if !hasPermission(perms, PermUISlots) {
			perms = append(perms, PermUISlots)
		}
	}
	return perms
}

// checkFrontendESMPolicy enforces the dual-track trust boundary for same-page ESM plugins
// and returns whether the plugin directory passed live Ed25519 signature verification:
//   - source == "official" (read-only OfficialPluginsDir): allowed in all profiles.
//   - source == "external" with valid live Ed25519 directory signature: allowed in all profiles.
//   - unsigned external ESM plugin: strictly blocked in office/normal mode; in developer mode
//     allowed only when security policy permits it (and explicit permission grant is enforced separately).
func (m *Manager) checkFrontendESMPolicy(ctx context.Context, source string, manifest Manifest, pluginDir string) (bool, error) {
	if !IsFrontendESM(manifest) {
		return false, nil
	}
	sigVerified := m.verifyPluginSignatureCached(manifest, pluginDir)
	if source == SourceOfficial || sigVerified {
		return sigVerified, nil
	}
	allowed, reason := m.AllowUnsignedESMPlugins(ctx)
	if !allowed {
		m.logger.Warn(
			"blocked unsigned external ESM plugin by security policy",
			"plugin_id", manifest.ID,
			"app_profile", m.EffectiveAppProfile(ctx),
			"reason", reason,
		)
		return false, fmt.Errorf("%w: plugin %q: %s", ErrPermissionDenied, manifest.ID, reason)
	}
	return false, nil
}

// checkExternalProcessPolicy blocks installing or running unsigned external process
// plugins while in office/normal mode unless cryptographically verified against a
// trusted public key.
func (m *Manager) checkExternalProcessPolicy(ctx context.Context, manifest Manifest, pluginDir string) error {
	if manifest.Backend.Type != BackendTypeProcess {
		return nil
	}
	if m.verifyPluginSignatureCached(manifest, pluginDir) {
		return nil
	}
	allowed, reason := m.AllowUnsignedProcessPlugins(ctx)
	if !allowed {
		m.logger.Warn(
			"blocked unsigned external process plugin by security policy",
			"plugin_id", manifest.ID,
			"app_profile", m.EffectiveAppProfile(ctx),
			"reason", reason,
		)
		return fmt.Errorf("%w: plugin %q: %s", ErrPermissionDenied, manifest.ID, reason)
	}
	return nil
}

// verifyPluginSignatureCached caches Ed25519 directory verification keyed by a
// stat-only directory fingerprint (relative path, size, mtime, mode). Files
// modified within the last 2 seconds always bypass the cache (racy-clean guard)
// so live tampering is detected immediately without re-hashing static files on
// every ESM asset request.
func (m *Manager) verifyPluginSignatureCached(manifest Manifest, pluginDir string) bool {
	if manifest.Signature == nil || len(m.trustedPublicKeys) == 0 || strings.TrimSpace(pluginDir) == "" {
		return false
	}
	fp, racy, fpErr := computePluginStatFingerprint(pluginDir)
	sigKey := strings.TrimSpace(manifest.Signature.Algorithm) + ":" +
		strings.TrimSpace(manifest.Signature.KeyID) + ":" +
		strings.TrimSpace(manifest.Signature.Value)
	cacheKey := filepath.Clean(pluginDir)

	if fpErr == nil && !racy {
		m.sigCacheMu.RLock()
		if entry, ok := m.sigCache[cacheKey]; ok && entry.fingerprint == fp && entry.sigKey == sigKey {
			m.sigCacheMu.RUnlock()
			return entry.verified
		}
		m.sigCacheMu.RUnlock()
	}

	verified := verifyPluginSignature(manifest, pluginDir, m.trustedPublicKeys)
	if fpErr == nil && !racy {
		m.sigCacheMu.Lock()
		if m.sigCache == nil {
			m.sigCache = map[string]cachedPluginSignature{}
		}
		m.sigCache[cacheKey] = cachedPluginSignature{
			fingerprint: fp,
			sigKey:      sigKey,
			verified:    verified,
		}
		m.sigCacheMu.Unlock()
	}
	return verified
}

func computePluginStatFingerprint(pluginDir string) (string, bool, error) {
	absRoot, err := filepath.Abs(pluginDir)
	if err != nil {
		return "", false, err
	}
	now := time.Now()
	racy := false
	entries := make([]string, 0, 16)
	err = filepath.WalkDir(absRoot, func(curr string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in signed plugin directory")
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, curr)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < 2*time.Second {
			racy = true
		}
		entries = append(entries, fmt.Sprintf("%s\x00%d\x00%d\x00%o\n",
			filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))
		return nil
	})
	if err != nil {
		return "", false, err
	}
	sort.Strings(entries)
	h := sha256.New()
	for _, line := range entries {
		_, _ = io.WriteString(h, line)
	}
	return hex.EncodeToString(h.Sum(nil)), racy, nil
}

// SignPluginDir computes a deterministic Ed25519 signature over plugin.json (excluding
// the signature field) and all regular files in pluginDir.
func SignPluginDir(pluginDir, keyID string, privKey ed25519.PrivateKey) (*PluginSignature, error) {
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: invalid ed25519 private key length", ErrInvalid)
	}
	manifest, err := LoadManifestFile(filepath.Join(pluginDir, "plugin.json"))
	if err != nil {
		return nil, err
	}
	payload, err := buildPluginSignaturePayload(manifest, pluginDir)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(privKey, payload)
	return &PluginSignature{
		Algorithm: "ed25519",
		KeyID:     strings.TrimSpace(keyID),
		Value:     base64.StdEncoding.EncodeToString(sig),
	}, nil
}

func verifyPluginSignature(manifest Manifest, pluginDir string, trustedKeys map[string]ed25519.PublicKey) bool {
	if manifest.Signature == nil || len(trustedKeys) == 0 || strings.TrimSpace(pluginDir) == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(manifest.Signature.Algorithm), "ed25519") {
		return false
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(manifest.Signature.Value))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}
	payload, err := buildPluginSignaturePayload(manifest, pluginDir)
	if err != nil {
		return false
	}
	keyID := strings.TrimSpace(manifest.Signature.KeyID)
	if keyID != "" {
		pub, ok := trustedKeys[keyID]
		return ok && len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, payload, sigBytes)
	}
	for _, pub := range trustedKeys {
		if len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, payload, sigBytes) {
			return true
		}
	}
	return false
}

type pluginFileDigest struct {
	rel string
	sum string
}

func collectPluginFileDigests(pluginDir string, includeManifest bool, rejectSymlinks bool) ([]pluginFileDigest, error) {
	absRoot, err := filepath.Abs(pluginDir)
	if err != nil {
		return nil, err
	}
	digests := []pluginFileDigest{}
	err = filepath.WalkDir(absRoot, func(curr string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			if rejectSymlinks {
				return fmt.Errorf("symlinks are not allowed in signed plugin directory")
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, curr)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		if !includeManifest && slashRel == "plugin.json" {
			return nil
		}
		f, err := os.Open(curr)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		digests = append(digests, pluginFileDigest{
			rel: slashRel,
			sum: hex.EncodeToString(h.Sum(nil)),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(digests, func(i, j int) bool {
		return digests[i].rel < digests[j].rel
	})
	return digests, nil
}

// ComputePluginContentDigest returns a deterministic 16-hex-character SHA-256 digest
// over all regular files in pluginDir (including plugin.json) for cache busting and
// hot-reload change detection.
func ComputePluginContentDigest(pluginDir string) (string, error) {
	digests, err := collectPluginFileDigests(pluginDir, true, false)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(contentDigestDomainPrefixV1))
	for _, fd := range digests {
		h.Write([]byte(fd.rel))
		h.Write([]byte{0})
		h.Write([]byte(fd.sum))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func buildPluginSignaturePayload(manifest Manifest, pluginDir string) ([]byte, error) {
	canonical := manifest
	canonical.Signature = nil
	canonical.Normalize()
	manifestJSON, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	manifestSum := sha256.Sum256(manifestJSON)

	digests, err := collectPluginFileDigests(pluginDir, false, true)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(signatureDomainPrefixV1)
	buf.WriteString(hex.EncodeToString(manifestSum[:]))
	buf.WriteByte('\n')
	for _, fd := range digests {
		buf.WriteString(fd.rel)
		buf.WriteByte(0)
		buf.WriteString(fd.sum)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}
