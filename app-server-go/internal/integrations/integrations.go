// Package integrations manages user-defined external web integrations pinned
// to the sidebar, backed by workspace_preferences in SQLite and a backend site
// metadata / favicon fetcher.
package integrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

const (
	prefKey         = "integrations:items"
	maxIntegrations = 100
	maxBodyBytes    = 384 * 1024
	maxIconBytes    = 200 * 1024
)

// BrowserAuthorizer preserves the shared browser token and Origin policy.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Options wires the integrations HTTP handler.
type Options struct {
	Store      *store.Store
	Authorizer BrowserAuthorizer
	HTTPClient *http.Client
}

// Integration represents one sidebar web link / embedded external tool.
type Integration struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	IconType      string `json:"icon_type"`
	IconValue     string `json:"icon_value"`
	AutoIconValue string `json:"auto_icon_value,omitempty"`
	OpenMode      string `json:"open_mode"`
	Visible       bool   `json:"visible"`
	Order         int    `json:"order"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type handler struct {
	Options
	mu sync.Mutex
}

// NewHandler returns the /api/v1/integrations HTTP handler.
func NewHandler(options Options) http.Handler {
	return &handler{Options: options}
}

// Matches reports whether this package owns the route.
func Matches(method, path string) bool {
	trimmed := strings.TrimRight(path, "/")
	switch trimmed {
	case "/api/v1/integrations":
		return method == http.MethodGet || method == http.MethodPost
	case "/api/v1/integrations/reorder", "/api/v1/integrations/fetch-meta":
		return method == http.MethodPost
	}
	if strings.HasPrefix(trimmed, "/api/v1/integrations/") {
		rest := strings.TrimPrefix(trimmed, "/api/v1/integrations/")
		if rest != "" && !strings.Contains(rest, "/") {
			return method == http.MethodPut || method == http.MethodDelete
		}
	}
	return false
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Authorizer != nil {
		if failure := h.Authorizer.AuthorizeBrowser(r); failure != nil {
			writeJSON(w, failure.Status, map[string]string{"detail": failure.Detail})
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.Store == nil {
		fail(w, http.StatusServiceUnavailable, "Integration store is not configured")
		return
	}

	trimmed := strings.TrimRight(r.URL.Path, "/")
	switch {
	case trimmed == "/api/v1/integrations" && r.Method == http.MethodGet:
		h.list(w, r)
	case trimmed == "/api/v1/integrations" && r.Method == http.MethodPost:
		h.create(w, r)
	case trimmed == "/api/v1/integrations/reorder" && r.Method == http.MethodPost:
		h.reorder(w, r)
	case trimmed == "/api/v1/integrations/fetch-meta" && r.Method == http.MethodPost:
		h.fetchMeta(w, r)
	case strings.HasPrefix(trimmed, "/api/v1/integrations/"):
		id := strings.TrimPrefix(trimmed, "/api/v1/integrations/")
		if r.Method == http.MethodPut {
			h.update(w, r, id)
		} else if r.Method == http.MethodDelete {
			h.remove(w, r, id)
		} else {
			fail(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	default:
		fail(w, http.StatusNotFound, "Not Found")
	}
}

func (h *handler) loadItems(ctx context.Context) ([]Integration, error) {
	pref, err := h.Store.GetPreference(ctx, prefKey)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []Integration{}, nil
	}
	encoded, err := json.Marshal(pref.Value)
	if err != nil {
		return nil, err
	}
	var items []Integration
	if err := json.Unmarshal(encoded, &items); err != nil {
		return []Integration{}, nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Order < items[j].Order
	})
	for i := range items {
		items[i].Order = i
		if items[i].IconType == "" {
			items[i].IconType = "auto"
		}
		if items[i].OpenMode == "" {
			items[i].OpenMode = "embed"
		}
	}
	return items, nil
}

func (h *handler) saveItems(ctx context.Context, items []Integration) error {
	for i := range items {
		items[i].Order = i
	}
	return h.Store.SetPreference(ctx, prefKey, items, true)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	items, err := h.loadItems(r.Context())
	h.mu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取集成列表")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type integrationPayload struct {
	Name          *string `json:"name"`
	URL           *string `json:"url"`
	IconType      *string `json:"icon_type"`
	IconValue     *string `json:"icon_value"`
	AutoIconValue *string `json:"auto_icon_value"`
	OpenMode      *string `json:"open_mode"`
	Visible       *bool   `json:"visible"`
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var payload integrationPayload
	if err := decodeJSON(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "请求格式无效")
		return
	}
	rawURL := ""
	if payload.URL != nil {
		rawURL = strings.TrimSpace(*payload.URL)
	}
	parsedURL, err := validateIntegrationURL(rawURL)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	nameSupplied := payload.Name != nil
	name := ""
	if nameSupplied {
		name = strings.TrimSpace(*payload.Name)
	}
	iconType := "auto"
	if payload.IconType != nil && strings.TrimSpace(*payload.IconType) != "" {
		iconType = strings.TrimSpace(*payload.IconType)
	}
	iconValue := ""
	if payload.IconValue != nil {
		iconValue = strings.TrimSpace(*payload.IconValue)
	}
	autoIconValue := ""
	if payload.AutoIconValue != nil {
		autoIconValue = strings.TrimSpace(*payload.AutoIconValue)
	}
	openMode := "embed"
	if payload.OpenMode != nil && strings.TrimSpace(*payload.OpenMode) != "" {
		openMode = strings.TrimSpace(*payload.OpenMode)
	}
	visible := true
	if payload.Visible != nil {
		visible = *payload.Visible
	}

	// Best-effort metadata fetch on create if auto icon wasn't prefilled (or name was omitted).
	if (iconType == "auto" && iconValue == "" && autoIconValue == "") || !nameSupplied {
		fetchCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		meta, _ := FetchSiteMeta(fetchCtx, h.HTTPClient, rawURL)
		cancel()
		if !nameSupplied && meta.Title != "" {
			name = truncateRunes(meta.Title, 48)
		}
		if autoIconValue == "" && meta.IconDataURL != "" {
			autoIconValue = meta.IconDataURL
		}
		if iconType == "auto" && iconValue == "" && autoIconValue != "" {
			iconValue = autoIconValue
		}
	}
	if !nameSupplied && name == "" {
		name = parsedURL.Hostname()
	}
	if iconType == "auto" && iconValue != "" && autoIconValue == "" {
		autoIconValue = iconValue
	}

	item := Integration{
		ID:            newIntegrationID(),
		Name:          name,
		URL:           rawURL,
		IconType:      iconType,
		IconValue:     iconValue,
		AutoIconValue: autoIconValue,
		OpenMode:      openMode,
		Visible:       visible,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := validateIntegration(&item); err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.loadItems(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取集成列表")
		return
	}
	if len(items) >= maxIntegrations {
		fail(w, http.StatusUnprocessableEntity, "集成数量已达到上限")
		return
	}
	item.Order = len(items)
	items = append(items, item)
	if err := h.saveItems(r.Context(), items); err != nil {
		fail(w, http.StatusInternalServerError, "无法保存集成配置")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request, id string) {
	var payload integrationPayload
	if err := decodeJSON(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "请求格式无效")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.loadItems(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取集成列表")
		return
	}
	index := -1
	for i := range items {
		if items[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		fail(w, http.StatusNotFound, "集成项不存在")
		return
	}
	updated := items[index]
	if payload.Name != nil {
		updated.Name = strings.TrimSpace(*payload.Name)
	}
	if payload.URL != nil {
		updated.URL = strings.TrimSpace(*payload.URL)
	}
	if payload.IconType != nil {
		updated.IconType = strings.TrimSpace(*payload.IconType)
	}
	if payload.IconValue != nil {
		updated.IconValue = strings.TrimSpace(*payload.IconValue)
	}
	if payload.AutoIconValue != nil {
		updated.AutoIconValue = strings.TrimSpace(*payload.AutoIconValue)
	}
	if payload.OpenMode != nil {
		updated.OpenMode = strings.TrimSpace(*payload.OpenMode)
	}
	if payload.Visible != nil {
		updated.Visible = *payload.Visible
	}
	if updated.IconType == "auto" && updated.IconValue != "" && updated.AutoIconValue == "" {
		updated.AutoIconValue = updated.IconValue
	}
	updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := validateIntegration(&updated); err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	items[index] = updated
	if err := h.saveItems(r.Context(), items); err != nil {
		fail(w, http.StatusInternalServerError, "无法保存集成配置")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) remove(w http.ResponseWriter, r *http.Request, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.loadItems(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取集成列表")
		return
	}
	next := make([]Integration, 0, len(items))
	removed := false
	for _, item := range items {
		if item.ID == id {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		fail(w, http.StatusNotFound, "集成项不存在")
		return
	}
	if err := h.saveItems(r.Context(), next); err != nil {
		fail(w, http.StatusInternalServerError, "无法保存集成配置")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

func (h *handler) reorder(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "请求格式无效")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.loadItems(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法读取集成列表")
		return
	}
	byID := make(map[string]Integration, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	ordered := make([]Integration, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, id := range payload.IDs {
		if item, ok := byID[id]; ok && !seen[id] {
			seen[id] = true
			ordered = append(ordered, item)
		}
	}
	for _, item := range items {
		if !seen[item.ID] {
			ordered = append(ordered, item)
		}
	}
	if err := h.saveItems(r.Context(), ordered); err != nil {
		fail(w, http.StatusInternalServerError, "无法保存排序")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ordered})
}

func (h *handler) fetchMeta(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "请求格式无效")
		return
	}
	rawURL := strings.TrimSpace(payload.URL)
	if _, err := validateIntegrationURL(rawURL); err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	fetchCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	meta, err := FetchSiteMeta(fetchCtx, h.HTTPClient, rawURL)
	if err != nil && meta.Title == "" && meta.IconDataURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"title":           "",
			"icon_data_url":   "",
			"icon_source_url": "",
			"warning":         err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func validateIntegrationURL(raw string) (*url.URL, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, errors.New("链接地址必须是完整的 http 或 https 地址")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, errors.New("链接地址必须是完整的 http 或 https 地址")
	}
	if parsed.User != nil {
		return nil, errors.New("链接地址不能包含用户名或密码")
	}
	return parsed, nil
}

func validateIntegration(item *Integration) error {
	if _, err := validateIntegrationURL(item.URL); err != nil {
		return err
	}
	if utf8.RuneCountInString(item.Name) > 64 {
		return errors.New("名称长度不能超过 64 个字符")
	}
	switch item.OpenMode {
	case "embed", "external":
	default:
		return errors.New("打开方式无效")
	}
	switch item.IconType {
	case "auto", "image":
		if err := validateImageIconValue(item.IconValue); err != nil {
			return err
		}
	case "builtin":
		if item.IconValue == "" || len(item.IconValue) > 32 {
			return errors.New("内置图标标识无效")
		}
	case "emoji":
		if count := utf8.RuneCountInString(item.IconValue); count < 1 || count > 16 {
			return errors.New("Emoji 图标长度无效")
		}
	default:
		return errors.New("图标类型无效")
	}
	if item.AutoIconValue != "" {
		if err := validateImageIconValue(item.AutoIconValue); err != nil {
			item.AutoIconValue = ""
		}
	}
	return nil
}

func validateImageIconValue(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxIconBytes {
		return errors.New("图标数据过大，请使用 150KB 以内的图片")
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "data:image/") {
		return nil
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if _, err := validateIntegrationURL(value); err != nil {
			return errors.New("自定义图标链接无效")
		}
		return nil
	}
	return errors.New("图标仅支持 http(s) 图片地址或 data:image 数据")
}

func newIntegrationID() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return "intg-" + hex.EncodeToString(buf)
}

func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max])
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
