package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	prefExtRegistries       = "ext:registries:v1"
	maxRegistryIndexBytes   = 8 << 20 // 8 MiB max marketplace index JSON
	defaultRegistryTimeout  = 12 * time.Second
	defaultDownloadTimeout  = 60 * time.Second
	builtinOfficialRegistry = "astrorder-official-local"
)

// PluginRegistrySource configures one remote or local plugin marketplace index.
type PluginRegistrySource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin,omitempty"`
}

// MarketplacePluginEntry describes one plugin published in a registry index.
type MarketplacePluginEntry struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Version          string           `json:"version"`
	Category         string           `json:"category,omitempty"`
	Description      string           `json:"description,omitempty"`
	Author           string           `json:"author,omitempty"`
	Homepage         string           `json:"homepage,omitempty"`
	Icon             string           `json:"icon,omitempty"`
	DownloadURL      string           `json:"download_url,omitempty"`
	SHA256           string           `json:"sha256,omitempty"`
	FrontendMode     string           `json:"frontend_mode,omitempty"`
	BackendType      string           `json:"backend_type,omitempty"`
	Permissions      []string         `json:"permissions,omitempty"`
	Extensions       []string         `json:"extensions,omitempty"`
	DependsOn        []string         `json:"depends_on,omitempty"`
	Tags             []string         `json:"tags,omitempty"`
	Signature        *PluginSignature `json:"signature,omitempty"`
	RegistryID       string           `json:"registry_id,omitempty"`
	RegistryName     string           `json:"registry_name,omitempty"`
	Installed        bool             `json:"installed"`
	InstalledVersion string           `json:"installed_version,omitempty"`
	InstalledSource  string           `json:"installed_source,omitempty"`
	Enabled          bool             `json:"enabled"`
	UpdateAvailable  bool             `json:"update_available"`
}

// MarketplaceRegistryIndex is the wire format served by a plugin marketplace index URL.
type MarketplaceRegistryIndex struct {
	SchemaVersion int                      `json:"schema_version"`
	Name          string                   `json:"name,omitempty"`
	UpdatedAt     string                   `json:"updated_at,omitempty"`
	Plugins       []MarketplacePluginEntry `json:"plugins"`
}

// MarketplaceSourceStatus reports per-registry fetch health.
type MarketplaceSourceStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin,omitempty"`
	Count   int    `json:"count"`
	Error   string `json:"error,omitempty"`
}

// MarketplaceResponse is returned by GET /api/v1/extensions/marketplace.
type MarketplaceResponse struct {
	Items      []MarketplacePluginEntry  `json:"items"`
	Registries []MarketplaceSourceStatus `json:"registries"`
}

func defaultBuiltinRegistries() []PluginRegistrySource {
	return []PluginRegistrySource{
		{
			ID:      builtinOfficialRegistry,
			Name:    "星序官方扩展目录（本地内置）",
			URL:     "builtin://official",
			Enabled: true,
			Builtin: true,
		},
	}
}

// ListRegistries returns built-in + user-configured marketplace registries.
func (m *Manager) ListRegistries(ctx context.Context) ([]PluginRegistrySource, error) {
	out := defaultBuiltinRegistries()
	if m.storage == nil || m.storage.store == nil {
		return out, nil
	}
	pref, err := m.storage.store.GetPreference(ctx, prefExtRegistries)
	if err != nil {
		return nil, err
	}
	if !pref.Exists {
		return out, nil
	}
	rawBytes, err := json.Marshal(pref.Value)
	if err != nil {
		return out, nil
	}
	var saved []PluginRegistrySource
	if err := json.Unmarshal(rawBytes, &saved); err != nil {
		return out, nil
	}
	seen := map[string]struct{}{builtinOfficialRegistry: {}}
	for _, src := range saved {
		id := strings.TrimSpace(src.ID)
		if id == "" || id == builtinOfficialRegistry {
			if id == builtinOfficialRegistry {
				out[0].Enabled = src.Enabled
			}
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		src.ID = id
		src.Name = strings.TrimSpace(src.Name)
		src.URL = strings.TrimSpace(src.URL)
		src.Builtin = false
		if src.URL != "" {
			out = append(out, src)
		}
	}
	return out, nil
}

// SaveRegistries validates and persists the configured marketplace registries.
func (m *Manager) SaveRegistries(ctx context.Context, sources []PluginRegistrySource) ([]PluginRegistrySource, error) {
	clean := make([]PluginRegistrySource, 0, len(sources)+1)
	seen := map[string]struct{}{}
	builtinEnabled := true

	for _, src := range sources {
		id := strings.TrimSpace(src.ID)
		if id == builtinOfficialRegistry {
			builtinEnabled = src.Enabled
			continue
		}
		rawURL := strings.TrimSpace(src.URL)
		if rawURL == "" {
			return nil, fmt.Errorf("%w: registry url is required", ErrInvalid)
		}
		if err := validateRegistryURL(rawURL); err != nil {
			return nil, err
		}
		if id == "" {
			sum := sha256.Sum256([]byte(rawURL))
			id = "reg-" + hex.EncodeToString(sum[:6])
		}
		if err := ValidatePluginID(id); err != nil {
			return nil, fmt.Errorf("%w: invalid registry id %q", ErrInvalid, id)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%w: duplicate registry id %q", ErrInvalid, id)
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(src.Name)
		if name == "" {
			name = id
		}
		clean = append(clean, PluginRegistrySource{
			ID:      id,
			Name:    name,
			URL:     rawURL,
			Enabled: src.Enabled,
			Builtin: false,
		})
	}

	result := append([]PluginRegistrySource{{
		ID:      builtinOfficialRegistry,
		Name:    "星序官方扩展目录（本地内置）",
		URL:     "builtin://official",
		Enabled: builtinEnabled,
		Builtin: true,
	}}, clean...)

	if m.storage != nil && m.storage.store != nil {
		if err := m.storage.store.SetPreference(ctx, prefExtRegistries, result, true); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// validateRegistryURL allows http/https URLs (blocking link-local cloud metadata endpoints)
// and local file:// JSON index paths for offline/enterprise testing.
func validateRegistryURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: URL is required", ErrInvalid)
	}
	if trimmed == "builtin://official" {
		return nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: invalid URL %q: %v", ErrInvalid, raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		if host == "" {
			return fmt.Errorf("%w: URL host is required", ErrInvalid)
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
			return fmt.Errorf("%w: link-local metadata address %q is not allowed", ErrPermissionDenied, host)
		}
		if host == "metadata.google.internal" || host == "metadata" {
			return fmt.Errorf("%w: metadata host %q is not allowed", ErrPermissionDenied, host)
		}
		return nil
	case "file":
		if strings.TrimSpace(u.Path) == "" {
			return fmt.Errorf("%w: file registry URL requires a path", ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported registry URL scheme %q (expected https, http, or file)", ErrInvalid, u.Scheme)
	}
}

func marketplaceHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err == nil {
				if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
					return nil, fmt.Errorf("%w: blocked link-local address %s", ErrPermissionDenied, host)
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validateRegistryURL(req.URL.String())
		},
	}
}

// FetchMarketplace aggregates plugins from all enabled registries and annotates installation status.
func (m *Manager) FetchMarketplace(ctx context.Context) (MarketplaceResponse, error) {
	registries, err := m.ListRegistries(ctx)
	if err != nil {
		return MarketplaceResponse{}, err
	}
	installedList, err := m.ListPlugins(ctx)
	if err != nil {
		return MarketplaceResponse{}, err
	}
	installedByID := make(map[string]PluginView, len(installedList))
	for _, item := range installedList {
		installedByID[item.ID] = item
	}

	statuses := make([]MarketplaceSourceStatus, 0, len(registries))
	mergedByKey := map[string]MarketplacePluginEntry{}
	var order []string

	for _, reg := range registries {
		st := MarketplaceSourceStatus{
			ID:      reg.ID,
			Name:    reg.Name,
			URL:     reg.URL,
			Enabled: reg.Enabled,
			Builtin: reg.Builtin,
		}
		if !reg.Enabled {
			statuses = append(statuses, st)
			continue
		}

		var entries []MarketplacePluginEntry
		var fetchErr error
		if reg.URL == "builtin://official" {
			entries = m.buildBuiltinMarketplaceEntries(installedList, reg)
		} else {
			entries, fetchErr = fetchRemoteRegistryEntries(ctx, reg)
		}
		if fetchErr != nil {
			st.Error = fetchErr.Error()
			statuses = append(statuses, st)
			continue
		}

		st.Count = len(entries)
		statuses = append(statuses, st)

		for _, entry := range entries {
			entry.ID = strings.TrimSpace(entry.ID)
			if entry.ID == "" {
				continue
			}
			entry.RegistryID = reg.ID
			entry.RegistryName = reg.Name
			if entry.Category == "" {
				entry.Category = "general"
			}
			entry.Extensions = normalizeFileExtensions(entry.Extensions)
			if inst, ok := installedByID[entry.ID]; ok {
				entry.Installed = true
				entry.InstalledVersion = inst.Version
				entry.InstalledSource = inst.Source
				entry.Enabled = inst.Enabled
				if entry.Version != "" && inst.Version != "" && entry.Version != inst.Version && inst.Source == SourceExternal {
					entry.UpdateAvailable = true
				}
			}
			key := reg.ID + ":" + entry.ID
			if _, exists := mergedByKey[key]; !exists {
				order = append(order, key)
			}
			mergedByKey[key] = entry
		}
	}

	items := make([]MarketplacePluginEntry, 0, len(order))
	for _, key := range order {
		items = append(items, mergedByKey[key])
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Installed != items[j].Installed {
			return !items[i].Installed
		}
		return items[i].ID < items[j].ID
	})

	return MarketplaceResponse{
		Items:      items,
		Registries: statuses,
	}, nil
}

func (m *Manager) buildBuiltinMarketplaceEntries(installed []PluginView, reg PluginRegistrySource) []MarketplacePluginEntry {
	entries := make([]MarketplacePluginEntry, 0, len(installed))
	for _, item := range installed {
		if item.Source != SourceOfficial {
			continue
		}
		var exts []string
		for _, v := range item.Contributes.Viewers {
			exts = append(exts, v.Extensions...)
		}
		frontendMode := ""
		if item.Frontend != nil {
			frontendMode = item.Frontend.Mode
		}
		entries = append(entries, MarketplacePluginEntry{
			ID:               item.ID,
			Name:             item.Name,
			Version:          item.Version,
			Category:         item.Category,
			Description:      item.Description,
			Author:           item.Author,
			Homepage:         item.Homepage,
			Icon:             item.Icon,
			FrontendMode:     frontendMode,
			BackendType:      item.BackendType,
			Permissions:      append([]string(nil), item.Permissions...),
			Extensions:       normalizeFileExtensions(exts),
			DependsOn:        append([]string(nil), item.DependsOn...),
			RegistryID:       reg.ID,
			RegistryName:     reg.Name,
			Installed:        true,
			InstalledVersion: item.Version,
			InstalledSource:  item.Source,
			Enabled:          item.Enabled,
		})
	}
	return entries
}

func fetchRemoteRegistryEntries(ctx context.Context, reg PluginRegistrySource) ([]MarketplacePluginEntry, error) {
	if err := validateRegistryURL(reg.URL); err != nil {
		return nil, err
	}
	u, _ := url.Parse(reg.URL)
	var raw []byte
	var err error

	if strings.EqualFold(u.Scheme, "file") {
		localPath := fileURLToPath(u)
		raw, err = os.ReadFile(localPath)
		if err != nil {
			return nil, fmt.Errorf("read local registry %q: %w", localPath, err)
		}
	} else {
		client := marketplaceHTTPClient(defaultRegistryTimeout)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, reg.URL, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Accept", "application/json")
		resp, doErr := client.Do(req)
		if doErr != nil {
			return nil, fmt.Errorf("fetch registry %q: %w", reg.Name, doErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("registry %q returned HTTP %d", reg.Name, resp.StatusCode)
		}
		raw, err = io.ReadAll(io.LimitReader(resp.Body, maxRegistryIndexBytes+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > maxRegistryIndexBytes {
			return nil, fmt.Errorf("registry index exceeds %d byte limit", maxRegistryIndexBytes)
		}
	}

	var idx MarketplaceRegistryIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("invalid registry JSON: %w", err)
	}
	if idx.SchemaVersion != 0 && idx.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported registry schema_version %d", idx.SchemaVersion)
	}

	out := make([]MarketplacePluginEntry, 0, len(idx.Plugins))
	for _, p := range idx.Plugins {
		if err := ValidatePluginID(p.ID); err != nil {
			continue
		}
		if strings.TrimSpace(p.Name) == "" {
			p.Name = p.ID
		}
		// Resolve relative download_url against the registry URL when appropriate.
		if dl := strings.TrimSpace(p.DownloadURL); dl != "" && !strings.Contains(dl, "://") && u != nil {
			if rel, relErr := url.Parse(dl); relErr == nil {
				p.DownloadURL = u.ResolveReference(rel).String()
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func fileURLToPath(u *url.URL) string {
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// InstallFromRemote downloads a plugin archive from downloadURL (http/https/file),
// verifies expectedSHA256 when provided, and installs it atomically via InstallFromZip.
func (m *Manager) InstallFromRemote(ctx context.Context, downloadURL, expectedSHA256, expectedPluginID string) (PluginView, error) {
	cleanURL := strings.TrimSpace(downloadURL)
	if err := validateRegistryURL(cleanURL); err != nil {
		return PluginView{}, err
	}
	if cleanURL == "builtin://official" {
		return PluginView{}, fmt.Errorf("%w: builtin official plugins are already bundled locally", ErrInvalid)
	}

	u, _ := url.Parse(cleanURL)
	var data []byte
	var err error

	if strings.EqualFold(u.Scheme, "file") {
		localPath := fileURLToPath(u)
		info, statErr := os.Stat(localPath)
		if statErr != nil {
			return PluginView{}, fmt.Errorf("%w: archive not found: %v", ErrInvalid, statErr)
		}
		if info.Size() > maxArchiveUploadSz {
			return PluginView{}, fmt.Errorf("%w: plugin archive exceeds %d byte limit", ErrInvalid, maxArchiveUploadSz)
		}
		data, err = os.ReadFile(localPath)
		if err != nil {
			return PluginView{}, fmt.Errorf("read local plugin archive: %w", err)
		}
	} else {
		client := marketplaceHTTPClient(defaultDownloadTimeout)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, cleanURL, nil)
		if reqErr != nil {
			return PluginView{}, reqErr
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			return PluginView{}, fmt.Errorf("download plugin archive: %w", doErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return PluginView{}, fmt.Errorf("download plugin archive failed with HTTP %d", resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxArchiveUploadSz+1))
		if err != nil {
			return PluginView{}, fmt.Errorf("read plugin archive stream: %w", err)
		}
		if int64(len(data)) > maxArchiveUploadSz {
			return PluginView{}, fmt.Errorf("%w: downloaded plugin archive exceeds %d byte limit", ErrInvalid, maxArchiveUploadSz)
		}
	}

	if expected := strings.ToLower(strings.TrimSpace(expectedSHA256)); expected != "" {
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if actual != expected {
			return PluginView{}, fmt.Errorf("%w: archive sha256 mismatch (expected %s, got %s)", ErrPermissionDenied, expected, actual)
		}
	}

	view, err := m.InstallFromZip(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return PluginView{}, err
	}
	if expID := strings.TrimSpace(expectedPluginID); expID != "" && view.ID != expID {
		_ = m.Uninstall(ctx, view.ID)
		return PluginView{}, fmt.Errorf("%w: downloaded archive contains plugin %q instead of expected %q", ErrInvalid, view.ID, expID)
	}
	return view, nil
}
