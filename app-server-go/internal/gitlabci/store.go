package gitlabci

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

type instanceRegistryPayload struct {
	Version int        `json:"version"`
	Items   []Instance `json:"items"`
}

// Registry persists self-hosted / cloud GitLab server configurations in SQLite preferences.
type Registry struct {
	store *store.Store
	mu    sync.Mutex
}

func NewRegistry(st *store.Store) *Registry {
	return &Registry{store: st}
}

func (r *Registry) List(ctx context.Context) ([]Instance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(ctx)
}

func (r *Registry) ListPublic(ctx context.Context) ([]PublicInstance, error) {
	items, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PublicInstance, 0, len(items))
	for _, item := range items {
		out = append(out, item.ToPublic())
	}
	return out, nil
}

func (r *Registry) Find(ctx context.Context, ref string) (*Instance, error) {
	items, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	clean := strings.TrimSpace(ref)
	if clean == "" {
		return nil, fmt.Errorf("%w: instance reference is required", ErrInvalid)
	}
	for i := range items {
		if items[i].ID == clean || strings.EqualFold(items[i].Name, clean) || strings.EqualFold(items[i].BaseURL, strings.TrimRight(clean, "/")) {
			found := items[i]
			return &found, nil
		}
	}
	return nil, fmt.Errorf("%w: GitLab 实例 %q 不存在", ErrNotFound, ref)
}

// MatchForHost picks the best enabled GitLab instance matching host (and optional port).
func (r *Registry) MatchForHost(ctx context.Context, host, port string) (*Instance, bool, error) {
	items, err := r.List(ctx)
	if err != nil {
		return nil, false, err
	}
	hasAny := false
	targetHost := strings.ToLower(strings.TrimSpace(host))
	targetPort := strings.TrimSpace(port)
	var hostFallback *Instance
	for i := range items {
		item := items[i]
		if !item.Enabled {
			continue
		}
		hasAny = true
		parsed, parseErr := url.Parse(item.BaseURL)
		if parseErr != nil {
			continue
		}
		instHost := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		if instHost == "" || instHost != targetHost {
			continue
		}
		instPort := strings.TrimSpace(parsed.Port())
		if targetPort != "" && instPort == targetPort {
			matched := item
			return &matched, hasAny, nil
		}
		if hostFallback == nil || item.IsDefault {
			copied := item
			hostFallback = &copied
		}
	}
	return hostFallback, hasAny, nil
}

// DefaultInstance returns the marked default enabled instance, or the first enabled instance.
func (r *Registry) DefaultInstance(ctx context.Context) (*Instance, bool, error) {
	items, err := r.List(ctx)
	if err != nil {
		return nil, false, err
	}
	var firstEnabled *Instance
	for i := range items {
		if !items[i].Enabled {
			continue
		}
		if items[i].IsDefault {
			cp := items[i]
			return &cp, true, nil
		}
		if firstEnabled == nil {
			cp := items[i]
			firstEnabled = &cp
		}
	}
	return firstEnabled, firstEnabled != nil, nil
}

func (r *Registry) Create(ctx context.Context, req CreateInstanceRequest) (*Instance, error) {
	baseURL, err := normalizeBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return nil, fmt.Errorf("%w: 请填写 GitLab Personal Access Token", ErrInvalid)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		if u, parseErr := url.Parse(baseURL); parseErr == nil && u.Hostname() != "" {
			name = u.Hostname()
		} else {
			name = "GitLab"
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	isDefault := req.IsDefault || len(items) == 0
	if isDefault {
		for i := range items {
			items[i].IsDefault = false
		}
	}
	item := Instance{
		ID:                 "gl_" + randomHex(6),
		Name:               name,
		BaseURL:            baseURL,
		Token:              token,
		InsecureSkipVerify: req.InsecureSkipVerify,
		Enabled:            enabled,
		IsDefault:          isDefault,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	items = append(items, item)
	if err := r.saveLocked(ctx, items); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *Registry) Update(ctx context.Context, id string, req UpdateInstanceRequest) (*Instance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range items {
		if items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: GitLab 实例 %q 不存在", ErrNotFound, id)
	}

	cur := items[idx]
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: 实例名称不能为空", ErrInvalid)
		}
		cur.Name = trimmed
	}
	if req.BaseURL != nil {
		norm, urlErr := normalizeBaseURL(*req.BaseURL)
		if urlErr != nil {
			return nil, urlErr
		}
		cur.BaseURL = norm
	}
	if req.Token != nil {
		if trimmed := strings.TrimSpace(*req.Token); trimmed != "" {
			cur.Token = trimmed
		}
	}
	if req.InsecureSkipVerify != nil {
		cur.InsecureSkipVerify = *req.InsecureSkipVerify
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.IsDefault != nil {
		cur.IsDefault = *req.IsDefault
		if cur.IsDefault {
			for i := range items {
				if i != idx {
					items[i].IsDefault = false
				}
			}
		}
	}
	cur.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	items[idx] = cur
	if err := r.saveLocked(ctx, items); err != nil {
		return nil, err
	}
	return &cur, nil
}

func (r *Registry) Delete(ctx context.Context, id string) (*Instance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range items {
		if items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: GitLab 实例 %q 不存在", ErrNotFound, id)
	}
	removed := items[idx]
	items = append(items[:idx], items[idx+1:]...)
	if removed.IsDefault && len(items) > 0 {
		items[0].IsDefault = true
	}
	if err := r.saveLocked(ctx, items); err != nil {
		return nil, err
	}
	return &removed, nil
}

func (r *Registry) loadLocked(ctx context.Context) ([]Instance, error) {
	if r.store == nil {
		return []Instance{}, nil
	}
	pref, err := r.store.GetPreference(ctx, PrefInstancesKey)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []Instance{}, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return []Instance{}, nil
	}
	var payload instanceRegistryPayload
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Items != nil {
		return payload.Items, nil
	}
	var direct []Instance
	if err := json.Unmarshal(raw, &direct); err == nil && direct != nil {
		return direct, nil
	}
	return []Instance{}, nil
}

func (r *Registry) saveLocked(ctx context.Context, items []Instance) error {
	if r.store == nil {
		return fmt.Errorf("store is not configured")
	}
	payload := instanceRegistryPayload{Version: 1, Items: items}
	return r.store.SetPreference(ctx, PrefInstancesKey, payload, true)
}

func (i Instance) ToPublic() PublicInstance {
	host := ""
	if u, err := url.Parse(i.BaseURL); err == nil {
		host = u.Host
	}
	return PublicInstance{
		ID:                 i.ID,
		Name:               i.Name,
		BaseURL:            i.BaseURL,
		Host:               host,
		TokenSet:           strings.TrimSpace(i.Token) != "",
		MaskedToken:        maskSecret(i.Token),
		InsecureSkipVerify: i.InsecureSkipVerify,
		Enabled:            i.Enabled,
		IsDefault:          i.IsDefault,
		CreatedAt:          i.CreatedAt,
		UpdatedAt:          i.UpdatedAt,
	}
}

func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: 请填写 GitLab 服务地址", ErrInvalid)
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("%w: 无效的 GitLab 服务地址 %q", ErrInvalid, raw)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: GitLab 地址仅支持 http 或 https 协议", ErrInvalid)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	cleanedPath := strings.TrimRight(parsed.Path, "/")
	cleanedPath = strings.TrimSuffix(cleanedPath, "/api/v4")
	parsed.Path = cleanedPath
	return strings.TrimRight(parsed.String(), "/"), nil
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

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
