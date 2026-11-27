package pluginhost

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/store"
)

const (
	prefExtPermissions = "ext:permissions"
	prefExtKVPrefix    = "ext:kv:"
	prefExtSecPrefix   = "ext:secrets:"
)

// Storage persists plugin enabled states, granted permissions, per-plugin KV values,
// and encrypted per-plugin secrets in workspace_preferences.
type Storage struct {
	store  *store.Store
	sealer *secretSealer
	mu     sync.Mutex
}

func NewStorage(st *store.Store, pluginsDir string) *Storage {
	keyDir := ""
	if st != nil && strings.TrimSpace(st.Path()) != "" {
		keyDir = filepath.Dir(st.Path())
	} else if strings.TrimSpace(pluginsDir) != "" {
		keyDir = pluginsDir
	}
	return &Storage{
		store:  st,
		sealer: newSecretSealer(keyDir),
	}
}

// GetEnabledMap returns the persisted plugins:enabled preference map.
func (s *Storage) GetEnabledMap(ctx context.Context) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getEnabledMapLocked(ctx)
}

func (s *Storage) getEnabledMapLocked(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	if s.store == nil {
		return out, nil
	}
	pref, err := s.store.GetPreference(ctx, store.PrefPluginsEnabled)
	if err != nil {
		return nil, err
	}
	if !pref.Exists {
		return out, nil
	}
	if saved, ok := pref.Value.(map[string]any); ok {
		for id, raw := range saved {
			key := strings.TrimSpace(id)
			if key == "" {
				continue
			}
			if flag, ok := raw.(bool); ok {
				out[key] = flag
			}
		}
	}
	return out, nil
}

// IsEnabled returns whether pluginID is enabled, defaulting to true for builtin and false for external.
func (s *Storage) IsEnabled(ctx context.Context, pluginID string, isBuiltin bool) (bool, error) {
	m, err := s.GetEnabledMap(ctx)
	if err != nil {
		return isBuiltin, err
	}
	if flag, ok := m[pluginID]; ok {
		return flag, nil
	}
	return isBuiltin, nil
}

// SetEnabled persists the enabled state for one plugin ID inside plugins:enabled.
func (s *Storage) SetEnabled(ctx context.Context, pluginID string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	m, err := s.getEnabledMapLocked(ctx)
	if err != nil {
		return err
	}
	m[pluginID] = enabled
	return s.store.SetPreference(ctx, store.PrefPluginsEnabled, m, true)
}

// DeleteEnabled removes a plugin ID entry from plugins:enabled.
func (s *Storage) DeleteEnabled(ctx context.Context, pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	m, err := s.getEnabledMapLocked(ctx)
	if err != nil {
		return err
	}
	if _, exists := m[pluginID]; !exists {
		return nil
	}
	delete(m, pluginID)
	return s.store.SetPreference(ctx, store.PrefPluginsEnabled, m, true)
}

// GetAllGrantedPermissions loads ext:permissions -> map[pluginID][]string.
func (s *Storage) GetAllGrantedPermissions(ctx context.Context) (map[string][]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getAllPermissionsLocked(ctx)
}

func (s *Storage) getAllPermissionsLocked(ctx context.Context) (map[string][]string, error) {
	out := map[string][]string{}
	if s.store == nil {
		return out, nil
	}
	pref, err := s.store.GetPreference(ctx, prefExtPermissions)
	if err != nil {
		return nil, err
	}
	if !pref.Exists {
		return out, nil
	}
	saved, ok := pref.Value.(map[string]any)
	if !ok {
		return out, nil
	}
	for id, raw := range saved {
		key := strings.TrimSpace(id)
		if key == "" {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			continue
		}
		perms := make([]string, 0, len(list))
		seen := map[string]struct{}{}
		for _, item := range list {
			if str, ok := item.(string); ok {
				p := strings.TrimSpace(str)
				if p != "" {
					if _, dup := seen[p]; !dup {
						seen[p] = struct{}{}
						perms = append(perms, p)
					}
				}
			}
		}
		out[key] = perms
	}
	return out, nil
}

// GetGrantedPermissions returns the list of user-approved permissions for pluginID.
func (s *Storage) GetGrantedPermissions(ctx context.Context, pluginID string) ([]string, error) {
	all, err := s.GetAllGrantedPermissions(ctx)
	if err != nil {
		return nil, err
	}
	if perms, ok := all[pluginID]; ok {
		return append([]string(nil), perms...), nil
	}
	return []string{}, nil
}

// SetGrantedPermissions saves user-approved permissions for pluginID.
func (s *Storage) SetGrantedPermissions(ctx context.Context, pluginID string, perms []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	all, err := s.getAllPermissionsLocked(ctx)
	if err != nil {
		return err
	}
	clean := make([]string, 0, len(perms))
	seen := map[string]struct{}{}
	for _, p := range perms {
		t := strings.TrimSpace(p)
		if t == "" || !IsKnownPermission(t) {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		clean = append(clean, t)
	}
	all[pluginID] = clean
	return s.store.SetPreference(ctx, prefExtPermissions, all, true)
}

// DeleteGrantedPermissions removes granted permissions for pluginID.
func (s *Storage) DeleteGrantedPermissions(ctx context.Context, pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	all, err := s.getAllPermissionsLocked(ctx)
	if err != nil {
		return err
	}
	if _, exists := all[pluginID]; !exists {
		return nil
	}
	delete(all, pluginID)
	return s.store.SetPreference(ctx, prefExtPermissions, all, true)
}

// GetKV reads the non-secret KV map stored under ext:kv:<pluginID>.
func (s *Storage) GetKV(ctx context.Context, pluginID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getKVLocked(ctx, pluginID)
}

func (s *Storage) getKVLocked(ctx context.Context, pluginID string) (map[string]any, error) {
	out := map[string]any{}
	if s.store == nil {
		return out, nil
	}
	pref, err := s.store.GetPreference(ctx, prefExtKVPrefix+pluginID)
	if err != nil {
		return nil, err
	}
	if !pref.Exists {
		return out, nil
	}
	if m, ok := pref.Value.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	return out, nil
}

// SetKV writes updates into ext:kv:<pluginID>. When replace is false, keys are merged
// (and nil values delete their key). When replace is true, the map is overwritten.
func (s *Storage) SetKV(ctx context.Context, pluginID string, updates map[string]any, replace bool) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current map[string]any
	var err error
	if replace {
		current = map[string]any{}
	} else {
		current, err = s.getKVLocked(ctx, pluginID)
		if err != nil {
			return nil, err
		}
	}
	for k, v := range updates {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if v == nil {
			delete(current, key)
		} else {
			current[key] = v
		}
	}
	if s.store != nil {
		if err := s.store.SetPreference(ctx, prefExtKVPrefix+pluginID, current, true); err != nil {
			return nil, err
		}
	}
	return current, nil
}

// DeleteKV removes ext:kv:<pluginID>.
func (s *Storage) DeleteKV(ctx context.Context, pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	_, err := s.store.DeletePreference(ctx, prefExtKVPrefix+pluginID)
	return err
}

func (s *Storage) getEncryptedSecretsLocked(ctx context.Context, pluginID string) (map[string]string, error) {
	out := map[string]string{}
	if s.store == nil {
		return out, nil
	}
	pref, err := s.store.GetPreference(ctx, prefExtSecPrefix+pluginID)
	if err != nil {
		return nil, err
	}
	if !pref.Exists {
		return out, nil
	}
	if m, ok := pref.Value.(map[string]any); ok {
		for k, raw := range m {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			if str, ok := raw.(string); ok && str != "" {
				out[key] = str
			}
		}
	}
	return out, nil
}

// GetRawSecrets decrypts and returns all secret key-value pairs for pluginID.
// NEVER return these to a browser; only serve to the owning plugin over the Host API.
func (s *Storage) GetRawSecrets(ctx context.Context, pluginID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := s.getEncryptedSecretsLocked(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	plain := make(map[string]string, len(enc))
	for k, sealed := range enc {
		v, err := s.sealer.UnsealString(sealed)
		if err != nil {
			return nil, fmt.Errorf("unseal secret %q: %w", k, err)
		}
		plain[k] = v
	}
	return plain, nil
}

// GetMaskedSecrets returns presence flags and masked representations safe for browser responses.
func (s *Storage) GetMaskedSecrets(ctx context.Context, pluginID string) (map[string]bool, map[string]string, error) {
	raw, err := s.GetRawSecrets(ctx, pluginID)
	if err != nil {
		return nil, nil, err
	}
	secretSet := make(map[string]bool, len(raw))
	masked := make(map[string]string, len(raw))
	for k, v := range raw {
		if strings.TrimSpace(v) == "" {
			continue
		}
		secretSet[k] = true
		masked[k] = maskSecret(v)
	}
	return secretSet, masked, nil
}

// SetSecrets encrypts and merges secret updates into ext:secrets:<pluginID>.
// Setting a key to "" removes that secret; omitted keys remain untouched.
func (s *Storage) SetSecrets(ctx context.Context, pluginID string, updates map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := s.getEncryptedSecretsLocked(ctx, pluginID)
	if err != nil {
		return err
	}
	for k, v := range updates {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if v == "" {
			delete(enc, key)
			continue
		}
		sealed, err := s.sealer.SealString(v)
		if err != nil {
			return fmt.Errorf("seal secret %q: %w", key, err)
		}
		enc[key] = sealed
	}
	if s.store != nil {
		return s.store.SetPreference(ctx, prefExtSecPrefix+pluginID, enc, true)
	}
	return nil
}

// DeleteSecrets removes ext:secrets:<pluginID>.
func (s *Storage) DeleteSecrets(ctx context.Context, pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	_, err := s.store.DeletePreference(ctx, prefExtSecPrefix+pluginID)
	return err
}

// CleanupPlugin removes all persisted state owned by an uninstalled external plugin.
func (s *Storage) CleanupPlugin(ctx context.Context, pluginID string) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	record(s.DeleteKV(ctx, pluginID))
	record(s.DeleteSecrets(ctx, pluginID))
	record(s.DeleteGrantedPermissions(ctx, pluginID))
	record(s.DeleteEnabled(ctx, pluginID))
	return firstErr
}

func maskSecret(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 8 {
		return "****"
	}
	return string(r[:4]) + "****" + string(r[len(r)-4:])
}
