package telemetry

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/auth"
)

const (
	ClientPerfPath      = "/api/v1/telemetry/client-perf"
	defaultMaxFileBytes = 5 * 1024 * 1024 // 5 MB active + 5 MB rotated (.1) = 10 MB cap
	maxBodyBytes        = 128 * 1024
	maxTrackedClients   = 64
	maxRecentRecords    = 100
)

// Options configures the local client performance telemetry collector.
type Options struct {
	Authorizer   *auth.Authenticator
	TelemetryDir string
	MaxFileBytes int64
	Logger       *slog.Logger
}

type clientRecord struct {
	ClientID        string         `json:"client_id"`
	Surface         string         `json:"surface"`
	FirstSeenAt     string         `json:"first_seen_at"`
	LastSeenAt      string         `json:"last_seen_at"`
	ReportsReceived int            `json:"reports_received"`
	Latest          map[string]any `json:"latest"`
}

// Handler ingests and exposes local-only client performance telemetry for Agent inspection.
type Handler struct {
	authorizer   *auth.Authenticator
	telemetryDir string
	maxFileBytes int64
	logger       *slog.Logger

	mu        sync.Mutex
	clients   map[string]*clientRecord
	recent    []json.RawMessage
	updatedAt string
}

// Matches returns true when method and path belong to the telemetry package.
func Matches(method, path string) bool {
	if path != ClientPerfPath {
		return false
	}
	return method == http.MethodPost || method == http.MethodGet || method == http.MethodDelete
}

// NewHandler constructs a local telemetry HTTP handler.
func NewHandler(opts Options) *Handler {
	maxBytes := opts.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxFileBytes
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Handler{
		authorizer:   opts.Authorizer,
		telemetryDir: strings.TrimSpace(opts.TelemetryDir),
		maxFileBytes: maxBytes,
		logger:       logger,
		clients:      make(map[string]*clientRecord),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authorizer != nil {
		if failure := h.authorizer.AuthorizeBrowser(r); failure != nil {
			writeJSON(w, failure.Status, map[string]any{"detail": failure.Detail})
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodPost:
		h.handleIngest(w, r)
	case http.MethodGet:
		h.handleSummary(w, r)
	case http.MethodDelete:
		h.handleReset(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"detail": "Method Not Allowed"})
	}
}

func (h *Handler) handleIngest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"detail": "Telemetry payload too large"})
		return
	}
	var payload map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": "Invalid telemetry JSON"})
		return
	}
	clientID := strings.TrimSpace(stringField(payload, "client_id"))
	surface := strings.TrimSpace(stringField(payload, "surface"))
	if clientID == "" || len(clientID) > 128 || surface == "" || len(surface) > 64 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": "client_id and surface are required"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	serverRuntime := captureServerRuntime()
	payload["server_received_at"] = now
	payload["server_runtime"] = serverRuntime

	encoded, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to encode telemetry"})
		return
	}

	h.mu.Lock()
	rec, exists := h.clients[clientID]
	if !exists {
		if len(h.clients) >= maxTrackedClients {
			h.evictOldestClientLocked()
		}
		rec = &clientRecord{
			ClientID:    clientID,
			Surface:     surface,
			FirstSeenAt: now,
		}
		h.clients[clientID] = rec
	}
	rec.Surface = surface
	rec.LastSeenAt = now
	rec.ReportsReceived++
	rec.Latest = payload
	h.updatedAt = now

	h.recent = append(h.recent, json.RawMessage(encoded))
	if len(h.recent) > maxRecentRecords {
		h.recent = append([]json.RawMessage(nil), h.recent[len(h.recent)-maxRecentRecords:]...)
	}

	summarySnapshot := h.buildSummaryLocked(0, serverRuntime)
	logErr := h.persistLocked(encoded, summarySnapshot)
	h.mu.Unlock()

	if logErr != nil {
		h.logger.Warn("failed to persist client perf telemetry", "error", logErr)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h *Handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil {
			if parsed < 0 {
				parsed = 0
			} else if parsed > maxRecentRecords {
				parsed = maxRecentRecords
			}
			limit = parsed
		}
	}
	serverRuntime := captureServerRuntime()
	h.mu.Lock()
	summary := h.buildSummaryLocked(limit, serverRuntime)
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) handleReset(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.clients = make(map[string]*clientRecord)
	h.recent = nil
	h.updatedAt = ""
	if h.telemetryDir != "" {
		for _, name := range []string{"client-perf.jsonl", "client-perf.1.jsonl", "latest-summary.json"} {
			_ = os.Remove(filepath.Join(h.telemetryDir, name))
		}
	}
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": true})
}

func (h *Handler) evictOldestClientLocked() {
	var oldestKey, oldestSeen string
	for k, v := range h.clients {
		if oldestKey == "" || v.LastSeenAt < oldestSeen {
			oldestKey = k
			oldestSeen = v.LastSeenAt
		}
	}
	if oldestKey != "" {
		delete(h.clients, oldestKey)
	}
}

func (h *Handler) buildSummaryLocked(recentLimit int, serverRuntime map[string]any) map[string]any {
	clients := make([]clientRecord, 0, len(h.clients))
	for _, rec := range h.clients {
		clients = append(clients, *rec)
	}
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].LastSeenAt > clients[j].LastSeenAt
	})

	result := map[string]any{
		"updated_at":     h.updatedAt,
		"server_runtime": serverRuntime,
		"client_count":   len(clients),
		"clients":        clients,
	}
	if h.telemetryDir != "" {
		result["telemetry_dir"] = h.telemetryDir
		result["log_path"] = filepath.Join(h.telemetryDir, "client-perf.jsonl")
		result["summary_path"] = filepath.Join(h.telemetryDir, "latest-summary.json")
	}
	if recentLimit > 0 {
		start := len(h.recent) - recentLimit
		if start < 0 {
			start = 0
		}
		tail := make([]json.RawMessage, len(h.recent)-start)
		copy(tail, h.recent[start:])
		result["recent"] = tail
	}
	return result
}

func (h *Handler) persistLocked(encodedLine []byte, summary map[string]any) error {
	if h.telemetryDir == "" {
		return nil
	}
	if err := os.MkdirAll(h.telemetryDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(h.telemetryDir, "client-perf.jsonl")
	if info, err := os.Stat(logPath); err == nil && info.Size()+int64(len(encodedLine))+1 > h.maxFileBytes {
		rotated := filepath.Join(h.telemetryDir, "client-perf.1.jsonl")
		_ = os.Remove(rotated)
		_ = os.Rename(logPath, rotated)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(append([]byte(nil), encodedLine...), '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	summaryBytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	summaryPath := filepath.Join(h.telemetryDir, "latest-summary.json")
	tmpPath := summaryPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(summaryBytes, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, summaryPath); err != nil {
		// Windows rename over existing target fallback.
		_ = os.Remove(summaryPath)
		return os.Rename(tmpPath, summaryPath)
	}
	return nil
}

func captureServerRuntime() map[string]any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return map[string]any{
		"heap_alloc_mb": round1(float64(m.HeapAlloc) / (1024 * 1024)),
		"heap_inuse_mb": round1(float64(m.HeapInuse) / (1024 * 1024)),
		"sys_mb":        round1(float64(m.Sys) / (1024 * 1024)),
		"num_gc":        m.NumGC,
		"goroutines":    runtime.NumGoroutine(),
	}
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
