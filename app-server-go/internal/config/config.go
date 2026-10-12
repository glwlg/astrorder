// Package config loads the small App Server foundation configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var defaultOrigins = []string{
	"http://127.0.0.1:30001",
	"http://localhost:30001",
	"http://127.0.0.1:30002",
	"http://localhost:30002",
}

const DefaultMaxAttachmentSize int64 = 10 * 1024 * 1024

func DefaultAttachmentTypes() []string {
	return []string{"application/json", "application/octet-stream", "application/pdf", "audio/aac", "audio/mp4", "audio/mpeg", "audio/ogg", "audio/wav", "audio/webm", "audio/x-wav", "image/gif", "image/jpeg", "image/png", "image/webp", "text/csv", "text/plain", "text/markdown"}
}

// Config contains only settings owned by the P4 HTTP foundation. Dynamic
// workspace preferences are supplied to httpapi through an injected lookup.
type Config struct {
	SourcePath             string
	Listen                 string
	StaticDir              string
	BrowserSecret          string
	ConnectorSecret        string
	AllowedOrigins         []string
	Migration              MigrationConfig
	DatabasePath           string
	AttachmentsDir         string
	MaxAttachmentSize      int64
	AllowedAttachmentTypes []string
	StagingDir             string
	DefaultWorkspace       string
	Daemon                 DaemonConfig
}

// DaemonConfig configures the independently managed Session Daemon transport.
type DaemonConfig struct {
	Target         string
	Credential     string
	RequestTimeout time.Duration
}

// MigrationConfig describes an optional, temporary, loopback-only upstream.
// No routes are implicitly assigned to it.
type MigrationConfig struct {
	Upstream            string
	BrowserCredential   string
	ConnectorCredential string
	Routes              []MigrationRoute
}

// MigrationRoute is an explicit METHOD+path ownership entry. Path parameters
// use the {name} form and match exactly one path segment.
type MigrationRoute struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	WebSocket bool   `json:"websocket,omitempty"`
	Audience  string `json:"audience"`
}

type productionEnvelope struct {
	Environment map[string]any `json:"environment"`
}

// Load reads an optional production.json-style file and overlays the current
// process ASTRORDER_* environment. It never generates credentials or reads a
// credential store.
func Load(path string) (Config, error) {
	values := make(map[string]string)
	if path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		var envelope productionEnvelope
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		if err := decoder.Decode(&envelope); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		if envelope.Environment == nil {
			return Config{}, errors.New("decode config: environment object is required")
		}
		for key, raw := range envelope.Environment {
			if !strings.HasPrefix(key, "ASTRORDER_") {
				return Config{}, fmt.Errorf("decode config: invalid environment key %q", key)
			}
			if strings.HasSuffix(key, "SECRET") || strings.HasSuffix(key, "CREDENTIAL") {
				return Config{}, fmt.Errorf("decode config: %s must be supplied through the process environment", key)
			}
			value, err := environmentString(raw)
			if err != nil {
				return Config{}, fmt.Errorf("decode config: %s: %w", key, err)
			}
			values[key] = value
		}
	}

	for _, key := range []string{
		"ASTRORDER_HOST",
		"ASTRORDER_PORT",
		"ASTRORDER_STATIC_DIR",
		"ASTRORDER_BROWSER_SECRET",
		"ASTRORDER_SECRET",
		"ASTRORDER_CONNECTOR_SECRET",
		"ASTRORDER_ALLOWED_ORIGINS",
		"ASTRORDER_MIGRATION_UPSTREAM",
		"ASTRORDER_MIGRATION_BROWSER_SECRET",
		"ASTRORDER_MIGRATION_CONNECTOR_SECRET",
		"ASTRORDER_MIGRATION_ROUTES",
		"ASTRORDER_DATABASE_URL",
		"ASTRORDER_DB_PATH",
		"ASTRORDER_DATA_DIR",
		"ASTRORDER_ATTACHMENTS_DIR",
		"ASTRORDER_MAX_ATTACHMENT_SIZE",
		"ASTRORDER_ALLOWED_ATTACHMENT_TYPES",
		"ASTRORDER_STAGING_DIR",
		"ASTRORDER_WORKSPACE",
		"ASTRORDER_SESSION_DAEMON_GRPC_TARGET",
		"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL",
	} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}

	host := valueOr(values, "ASTRORDER_HOST", "127.0.0.1")
	port, err := parsePort(valueOr(values, "ASTRORDER_PORT", "30002"))
	if err != nil {
		return Config{}, err
	}
	origins := csv(values["ASTRORDER_ALLOWED_ORIGINS"], defaultOrigins)
	for _, origin := range origins {
		if origin == "*" {
			return Config{}, errors.New("ASTRORDER_ALLOWED_ORIGINS must not contain a wildcard")
		}
	}

	browserSecret := values["ASTRORDER_BROWSER_SECRET"]
	if browserSecret == "" {
		browserSecret = values["ASTRORDER_SECRET"]
	}
	migrationRoutes, err := parseMigrationRoutes(values["ASTRORDER_MIGRATION_ROUTES"])
	if err != nil {
		return Config{}, err
	}

	databasePath := resolveDatabasePath(values)
	dataRoot := resolveDataRoot(values)

	attachmentsDir := values["ASTRORDER_ATTACHMENTS_DIR"]
	if attachmentsDir == "" && dataRoot != "" {
		attachmentsDir = filepath.Join(dataRoot, "attachments")
	}

	stagingDir := values["ASTRORDER_STAGING_DIR"]
	if stagingDir == "" && dataRoot != "" {
		stagingDir = filepath.Join(dataRoot, "staging")
	}

	defaultWorkspace := values["ASTRORDER_WORKSPACE"]
	if defaultWorkspace == "" {
		defaultWorkspace, _ = os.Getwd()
	}
	maxAttachmentSize := DefaultMaxAttachmentSize
	if raw, exists := values["ASTRORDER_MAX_ATTACHMENT_SIZE"]; exists {
		maxAttachmentSize, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || maxAttachmentSize < 1 {
			return Config{}, errors.New("ASTRORDER_MAX_ATTACHMENT_SIZE must be a positive integer")
		}
	}
	allowedAttachmentTypes := DefaultAttachmentTypes()
	if raw, exists := values["ASTRORDER_ALLOWED_ATTACHMENT_TYPES"]; exists {
		allowedAttachmentTypes = csv(raw, []string{})
		if len(allowedAttachmentTypes) == 0 {
			return Config{}, errors.New("ASTRORDER_ALLOWED_ATTACHMENT_TYPES must contain at least one content type")
		}
	}

	return Config{
		Listen:                 net.JoinHostPort(host, strconv.Itoa(port)),
		StaticDir:              values["ASTRORDER_STATIC_DIR"],
		BrowserSecret:          browserSecret,
		ConnectorSecret:        values["ASTRORDER_CONNECTOR_SECRET"],
		AllowedOrigins:         origins,
		DatabasePath:           databasePath,
		AttachmentsDir:         attachmentsDir,
		MaxAttachmentSize:      maxAttachmentSize,
		AllowedAttachmentTypes: allowedAttachmentTypes,
		StagingDir:             stagingDir,
		DefaultWorkspace:       defaultWorkspace,
		Daemon: DaemonConfig{
			Target:         values["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"],
			Credential:     values["ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL"],
			RequestTimeout: 10 * time.Second,
		},
		Migration: MigrationConfig{
			Upstream:            strings.TrimSpace(values["ASTRORDER_MIGRATION_UPSTREAM"]),
			BrowserCredential:   values["ASTRORDER_MIGRATION_BROWSER_SECRET"],
			ConnectorCredential: values["ASTRORDER_MIGRATION_CONNECTOR_SECRET"],
			Routes:              migrationRoutes,
		},
	}, nil
}

func resolveDataRoot(values map[string]string) string {
	if raw, ok := values["ASTRORDER_DATA_DIR"]; ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "Astrorder", "data")
	}
	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".local", "share", "astrorder")
	}
	return ""
}

func resolveDatabasePath(values map[string]string) string {
	if raw, ok := values["ASTRORDER_DB_PATH"]; ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	if raw, ok := values["ASTRORDER_DATABASE_URL"]; ok && strings.TrimSpace(raw) != "" {
		raw = strings.TrimSpace(raw)
		if strings.HasPrefix(raw, "sqlite:///") {
			return strings.TrimPrefix(raw, "sqlite:///")
		}
		if strings.Contains(raw, "://") {
			// Not a SQLite file URL
			return ""
		}
		return raw
	}
	dataRoot := resolveDataRoot(values)
	if dataRoot != "" {
		return filepath.Join(dataRoot, "astrorder.sqlite3")
	}
	return ""
}

func environmentString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case json.Number:
		return typed.String(), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	default:
		return "", errors.New("value must be a string, number, or boolean")
	}
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("ASTRORDER_PORT must be between 1 and 65535")
	}
	return port, nil
}

func parseMigrationRoutes(raw string) ([]MigrationRoute, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var routes []MigrationRoute
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&routes); err != nil {
		return nil, fmt.Errorf("ASTRORDER_MIGRATION_ROUTES must be a JSON array: %w", err)
	}
	if routes == nil {
		return nil, errors.New("ASTRORDER_MIGRATION_ROUTES must be a JSON array")
	}
	return routes, nil
}

func csv(value string, defaults []string) []string {
	if value == "" {
		return append([]string(nil), defaults...)
	}
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func valueOr(values map[string]string, key, fallback string) string {
	if value, ok := values[key]; ok {
		return value
	}
	return fallback
}
