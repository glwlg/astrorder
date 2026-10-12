package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

type Publisher interface {
	Publish(context.Context, string, map[string]any) error
}

type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
	Owner  string `json:"owner"`
}

func Registrations() []Registration {
	return []Registration{
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/audio/transcriptions", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/audio/local-plugin", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPut, Path: "/api/v1/audio/local-plugin", Auth: "browser", Owner: "go-app"},
		{Kind: "websocket", Method: http.MethodGet, Path: "/api/v1/audio/local-stream", Auth: "browser", Owner: "go-app"},
	}
}

// Matches checks whether method and path match any audio route.
func Matches(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	switch clean {
	case "/api/v1/audio/transcriptions":
		return method == http.MethodPost
	case "/api/v1/audio/local-plugin":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/audio/local-stream":
		return method == http.MethodGet || method == "WS"
	default:
		return false
	}
}

type Options struct {
	Context        context.Context // Optional app lifetime context. Defaults to context.Background().
	Store          *store.Store
	Authorizer     *auth.Authenticator
	Publisher      Publisher
	ResourceDir    string
	HTTPClient     *http.Client
	ProcessFactory ProcessFactory
}

type Service struct {
	options         Options
	authorizer      *auth.Authenticator
	plugin          *LocalR2T2Manager
	appCtx          context.Context
	activeStreamsMu sync.Mutex
	activeStreams   map[int]context.CancelFunc
	nextStreamID    int
	streamWg        sync.WaitGroup
}

func NewService(options Options) (*Service, error) {
	appCtx := options.Context
	if appCtx == nil {
		appCtx = context.Background()
	}

	manager, err := newLocalR2T2Manager(options, appCtx)
	if err != nil {
		return nil, fmt.Errorf("init local r2t2 manager: %w", err)
	}

	service := &Service{
		options:       options,
		authorizer:    options.Authorizer,
		plugin:        manager,
		appCtx:        appCtx,
		activeStreams: make(map[int]context.CancelFunc),
	}

	// Restore startup state preserving user preference without implicit download
	if err := service.plugin.Restore(appCtx); err != nil {
		_ = err
	}

	return service, nil
}

func (s *Service) Matches(method, path string) bool {
	return Matches(method, path)
}

func (s *Service) Handler() http.Handler {
	return s
}

func (s *Service) registerStream(cancel context.CancelFunc) (int, func()) {
	s.activeStreamsMu.Lock()
	s.nextStreamID++
	id := s.nextStreamID
	s.activeStreams[id] = cancel
	s.streamWg.Add(1)
	s.activeStreamsMu.Unlock()

	return id, func() {
		s.activeStreamsMu.Lock()
		delete(s.activeStreams, id)
		s.activeStreamsMu.Unlock()
		s.streamWg.Done()
	}
}

func (s *Service) Close(ctx context.Context) error {
	// 1. Cancel all active streams
	s.activeStreamsMu.Lock()
	for _, cancel := range s.activeStreams {
		cancel()
	}
	s.activeStreamsMu.Unlock()

	// 2. Wait for streams to drain, bounded by ctx
	streamsDone := make(chan struct{})
	go func() {
		s.streamWg.Wait()
		close(streamsDone)
	}()

	select {
	case <-streamsDone:
	case <-ctx.Done():
		if s.plugin != nil {
			_ = s.plugin.Close(ctx)
		}
		return ctx.Err()
	}

	// 3. Close plugin and join all plugin workers before returning
	if s.plugin != nil {
		return s.plugin.Close(ctx)
	}
	return nil
}

func (s *Service) Registrations() []Registration {
	return Registrations()
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.TrimRight(r.URL.Path, "/")
	switch cleanPath {
	case "/api/v1/audio/transcriptions":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		s.handleTranscribe(w, r)
	case "/api/v1/audio/local-plugin":
		switch r.Method {
		case http.MethodGet:
			s.handleLocalPluginGet(w, r)
		case http.MethodPut:
			s.handleLocalPluginPut(w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	case "/api/v1/audio/local-stream":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		s.handleLocalStream(w, r)
	default:
		writeJSONError(w, http.StatusNotFound, "Not Found")
	}
}

func (s *Service) handleLocalPluginGet(w http.ResponseWriter, r *http.Request) {
	if s.authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}
	status := s.plugin.Status()
	writeJSON(w, http.StatusOK, status)
}

type localPluginUpdateRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Service) handleLocalPluginPut(w http.ResponseWriter, r *http.Request) {
	if s.authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}
	var req localPluginUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Enabled {
		if err := s.plugin.Enable(false, true); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		if err := s.plugin.Disable(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	status := s.plugin.Status()
	writeJSON(w, http.StatusOK, status)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
