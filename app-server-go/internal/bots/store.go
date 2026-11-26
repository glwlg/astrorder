package bots

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"

	"astrorder.dev/app-server/internal/store"
)

var (
	ErrNotFound = errors.New("bot not found")
	ErrConflict = errors.New("bot conflict")
	ErrInvalid  = errors.New("invalid bot input")
)

type registryPayload struct {
	Version int          `json:"version"`
	Items   []BotProfile `json:"items"`
}

// Registry persists BotProfile entries in store workspace_preferences without altering SQLite schema.
type Registry struct {
	store *store.Store
	mu    sync.Mutex
}

func NewRegistry(st *store.Store) *Registry {
	return &Registry{store: st}
}

func (r *Registry) loadLocked(ctx context.Context) ([]BotProfile, error) {
	if r == nil || r.store == nil {
		return []BotProfile{}, nil
	}
	pref, err := r.store.GetPreference(ctx, PreferenceKey)
	if err != nil {
		return nil, fmt.Errorf("load bot registry: %w", err)
	}
	if !pref.Exists || pref.Value == nil {
		return []BotProfile{}, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal bot registry preference: %w", err)
	}
	var payload registryPayload
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Items != nil {
		for i := range payload.Items {
			if strings.TrimSpace(payload.Items[i].DefaultApprovalMode) == "" {
				payload.Items[i].DefaultApprovalMode = ApprovalModeFullAccess
			}
		}
		return payload.Items, nil
	}
	var direct []BotProfile
	if err := json.Unmarshal(raw, &direct); err == nil && direct != nil {
		for i := range direct {
			if strings.TrimSpace(direct[i].DefaultApprovalMode) == "" {
				direct[i].DefaultApprovalMode = ApprovalModeFullAccess
			}
		}
		return direct, nil
	}
	return []BotProfile{}, nil
}

func (r *Registry) saveLocked(ctx context.Context, items []BotProfile) error {
	if r == nil || r.store == nil {
		return errors.New("store is not configured")
	}
	payload := registryPayload{
		Version: 1,
		Items:   items,
	}
	if err := r.store.SetPreference(ctx, PreferenceKey, payload, true); err != nil {
		return fmt.Errorf("save bot registry: %w", err)
	}
	return nil
}

// List returns all registered Bots.
func (r *Registry) List(ctx context.Context) ([]BotProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BotProfile, len(items))
	copy(out, items)
	return out, nil
}

// Find resolves a Bot by ID, slug (case-insensitive), or display name (case-insensitive).
func (r *Registry) Find(ctx context.Context, ref string) (*BotProfile, error) {
	target := strings.TrimSpace(ref)
	if target == "" {
		return nil, ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == target {
			found := items[i]
			return &found, nil
		}
	}
	for i := range items {
		if strings.EqualFold(items[i].Slug, target) {
			found := items[i]
			return &found, nil
		}
	}
	for i := range items {
		if strings.EqualFold(items[i].Name, target) {
			found := items[i]
			return &found, nil
		}
	}
	return nil, ErrNotFound
}

// Create validates and appends a new BotProfile.
func (r *Registry) Create(ctx context.Context, req CreateBotRequest) (*BotProfile, error) {
	name := strings.TrimSpace(req.Name)
	workspace := strings.TrimSpace(req.Workspace)
	agentID := strings.TrimSpace(req.DefaultAgentID)
	if name == "" {
		return nil, fmt.Errorf("%w: Bot 名称不能为空", ErrInvalid)
	}
	if workspace == "" {
		return nil, fmt.Errorf("%w: 工作区目录不能为空", ErrInvalid)
	}
	if agentID == "" {
		return nil, fmt.Errorf("%w: 默认执行 Agent 不能为空", ErrInvalid)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}

	slug := NormalizeSlug(req.Slug)
	if slug == "" {
		slug = NormalizeSlug(name)
	}
	if slug == "" {
		slug = NormalizeSlug(workspaceBasename(workspace))
	}
	if slug == "" {
		slug = "bot-" + randomHex(3)
	}
	slug = ensureUniqueSlug(slug, "", items)

	approvalMode, err := NormalizeApprovalMode(req.DefaultApprovalMode)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	bot := BotProfile{
		ID:                  "bot_" + randomHex(6),
		Slug:                slug,
		Name:                name,
		Description:         strings.TrimSpace(req.Description),
		Icon:                normalizeIcon(req.Icon),
		Workspace:           workspace,
		ConnectionID:        normalizeConnectionID(req.ConnectionID),
		DefaultAgentID:      agentID,
		DefaultProvider:     strings.TrimSpace(req.DefaultProvider),
		DefaultModel:        strings.TrimSpace(req.DefaultModel),
		DefaultEffort:       strings.TrimSpace(req.DefaultEffort),
		DefaultApprovalMode: approvalMode,
		DefaultNotifyCaller: req.DefaultNotifyCaller,
		Presets:             cleanPresets(req.Presets),
		OfficeMode:          req.OfficeMode,
		Tags:                cleanTags(req.Tags),
		SourceSessionKey:    strings.TrimSpace(req.SourceSessionKey),
		SourceSessionTitle:  strings.TrimSpace(req.SourceSessionTitle),
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	items = append(items, bot)
	if err := r.saveLocked(ctx, items); err != nil {
		return nil, err
	}
	return &bot, nil
}

// Update modifies fields on an existing BotProfile.
func (r *Registry) Update(ctx context.Context, idOrRef string, req UpdateBotRequest) (*BotProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	idx := findBotIndex(items, idOrRef)
	if idx < 0 {
		return nil, ErrNotFound
	}
	bot := items[idx]
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: Bot 名称不能为空", ErrInvalid)
		}
		bot.Name = name
	}
	if req.Slug != nil {
		nextSlug := NormalizeSlug(*req.Slug)
		if nextSlug == "" {
			return nil, fmt.Errorf("%w: slug 只能包含字母、数字或连字符", ErrInvalid)
		}
		for i, other := range items {
			if i != idx && strings.EqualFold(other.Slug, nextSlug) {
				return nil, fmt.Errorf("%w: slug %q 已被其他 Bot 使用", ErrConflict, nextSlug)
			}
		}
		bot.Slug = nextSlug
	}
	if req.Description != nil {
		bot.Description = strings.TrimSpace(*req.Description)
	}
	if req.Icon != nil {
		bot.Icon = normalizeIcon(*req.Icon)
	}
	if req.Workspace != nil {
		ws := strings.TrimSpace(*req.Workspace)
		if ws == "" {
			return nil, fmt.Errorf("%w: 工作区目录不能为空", ErrInvalid)
		}
		bot.Workspace = ws
	}
	if req.ConnectionID != nil {
		bot.ConnectionID = normalizeConnectionID(*req.ConnectionID)
	}
	if req.DefaultAgentID != nil {
		aid := strings.TrimSpace(*req.DefaultAgentID)
		if aid == "" {
			return nil, fmt.Errorf("%w: 默认执行 Agent 不能为空", ErrInvalid)
		}
		bot.DefaultAgentID = aid
	}
	if req.DefaultProvider != nil {
		bot.DefaultProvider = strings.TrimSpace(*req.DefaultProvider)
	}
	if req.DefaultModel != nil {
		bot.DefaultModel = strings.TrimSpace(*req.DefaultModel)
	}
	if req.DefaultEffort != nil {
		bot.DefaultEffort = strings.TrimSpace(*req.DefaultEffort)
	}
	if req.DefaultApprovalMode != nil {
		mode, modeErr := NormalizeApprovalMode(*req.DefaultApprovalMode)
		if modeErr != nil {
			return nil, modeErr
		}
		bot.DefaultApprovalMode = mode
	}
	if strings.TrimSpace(bot.DefaultApprovalMode) == "" {
		bot.DefaultApprovalMode = ApprovalModeFullAccess
	}
	if req.DefaultNotifyCaller != nil {
		v := *req.DefaultNotifyCaller
		bot.DefaultNotifyCaller = &v
	}
	if req.Presets != nil {
		bot.Presets = cleanPresets(*req.Presets)
	}
	if req.OfficeMode != nil {
		bot.OfficeMode = *req.OfficeMode
	}
	if req.Tags != nil {
		bot.Tags = cleanTags(req.Tags)
	}
	bot.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	items[idx] = bot
	if err := r.saveLocked(ctx, items); err != nil {
		return nil, err
	}
	return &bot, nil
}

// Delete unregisters a Bot from Astrorder without ever deleting files on disk.
func (r *Registry) Delete(ctx context.Context, idOrRef string) (*BotProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	idx := findBotIndex(items, idOrRef)
	if idx < 0 {
		return nil, ErrNotFound
	}
	removed := items[idx]
	next := append(items[:idx], items[idx+1:]...)
	if err := r.saveLocked(ctx, next); err != nil {
		return nil, err
	}
	return &removed, nil
}

func findBotIndex(items []BotProfile, ref string) int {
	target := strings.TrimSpace(ref)
	for i := range items {
		if items[i].ID == target {
			return i
		}
	}
	for i := range items {
		if strings.EqualFold(items[i].Slug, target) {
			return i
		}
	}
	return -1
}

// NormalizeSlug turns arbitrary text into a lowercase ASCII kebab-case identifier.
func NormalizeSlug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '_' || r == '.' || unicode.IsSpace(r) || r == '/' || r == '\\' {
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}

func ensureUniqueSlug(base, excludeID string, items []BotProfile) string {
	exists := func(candidate string) bool {
		for _, item := range items {
			if item.ID == excludeID {
				continue
			}
			if strings.EqualFold(item.Slug, candidate) {
				return true
			}
		}
		return false
	}
	if !exists(base) {
		return base
	}
	for n := 2; n < 1000; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !exists(candidate) {
			return candidate
		}
	}
	return base + "-" + randomHex(3)
}

func normalizeConnectionID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "local") {
		return ""
	}
	return trimmed
}

func normalizeIcon(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "robot", "video", "doc", "code", "design", "data", "spark", "briefcase", "ops":
		return trimmed
	default:
		return "robot"
	}
}

// NormalizeApprovalMode validates a Bot approval mode, defaulting empty input to full_access.
func NormalizeApprovalMode(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "", ApprovalModeFullAccess:
		return ApprovalModeFullAccess, nil
	case ApprovalModeAuto:
		return ApprovalModeAuto, nil
	case ApprovalModeManual:
		return ApprovalModeManual, nil
	default:
		return "", fmt.Errorf("%w: 不支持的权限模式 %q（可选 full_access / auto / manual）", ErrInvalid, raw)
	}
}

func cleanTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func cleanPresets(presets []BotPreset) []BotPreset {
	if len(presets) == 0 {
		return nil
	}
	out := make([]BotPreset, 0, len(presets))
	seen := make(map[string]struct{}, len(presets))
	for _, p := range presets {
		label := strings.TrimSpace(p.Label)
		prompt := strings.TrimSpace(p.Prompt)
		if label == "" || prompt == "" {
			continue
		}
		id := strings.TrimSpace(p.ID)
		if id == "" {
			id = "preset_" + randomHex(4)
		}
		if _, dup := seen[id]; dup {
			id = "preset_" + randomHex(4)
		}
		seen[id] = struct{}{}
		out = append(out, BotPreset{
			ID:     id,
			Label:  truncateRunes(label, 40),
			Title:  strings.TrimSpace(p.Title),
			Prompt: prompt,
		})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func workspaceBasename(ws string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(ws), "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return ""
	}
	return path.Base(normalized)
}

// NormalizeWorkspaceKey produces a normalized path key for matching sessions to Bot workspaces.
func NormalizeWorkspaceKey(ws string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(ws), "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	// Case-insensitive match for Windows drive letters like C:/foo
	if len(normalized) >= 2 && normalized[1] == ':' {
		return strings.ToLower(normalized)
	}
	return normalized
}
