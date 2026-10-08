package pty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	core "astrorder.dev/session-daemon/internal/runtime"
)

type EmitFunc func(sessionID, event string, payload map[string]any, status string) error

type Config struct {
	Allowed         []string
	Emit            EmitFunc
	Shell           string
	TerminalFactory TerminalFactory
}

type session struct {
	sessionID string
	terminal  Terminal

	statusMu sync.RWMutex
	status   string
	closed   bool

	writeMu   sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (s *session) close() error {
	s.closeOnce.Do(func() {
		s.statusMu.Lock()
		if s.status != "error" {
			s.status = "idle"
		}
		s.closed = true
		s.statusMu.Unlock()

		if s.terminal != nil {
			s.closeErr = s.terminal.Close()
			if s.closeErr != nil {
				s.statusMu.Lock()
				s.status = "error"
				s.statusMu.Unlock()
			}
		}
	})
	return s.closeErr
}

type Runtime struct {
	config          Config
	allowedRoots    []string
	terminalFactory TerminalFactory

	mu       sync.RWMutex
	sessions map[string]*session
	opening  map[string]bool
	closed   bool
}

// Ensure Runtime implements core.Adapter.
var _ core.Adapter = (*Runtime)(nil)

func New(config Config) (*Runtime, error) {
	if len(config.Allowed) == 0 {
		return nil, errors.New("pty allowed workspaces cannot be empty")
	}

	roots := make([]string, 0, len(config.Allowed))
	for _, root := range config.Allowed {
		clean, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return nil, fmt.Errorf("eval symlink on allowed root %q: %w", root, err)
		}
		fi, err := os.Stat(resolved)
		if err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("allowed workspace %q is not a directory", root)
		}
		roots = append(roots, resolved)
	}

	tf := config.TerminalFactory
	if tf == nil {
		tf = defaultPlatformTerminal
	}

	return &Runtime{
		config:          config,
		allowedRoots:    roots,
		terminalFactory: tf,
		sessions:        make(map[string]*session),
		opening:         map[string]bool{},
	}, nil
}

func (r *Runtime) Create(ctx context.Context, req core.Request) (core.Result, error) {
	return core.Result{}, errors.New("pty runtime does not support create; use spawn")
}

func (r *Runtime) Spawn(ctx context.Context, req core.Request) (core.Result, error) {
	sessionID := req.SessionID
	if sessionID == "" && req.Fields != nil {
		if sid, ok := req.Fields["session_id"].(string); ok {
			sessionID = sid
		}
	}
	if sessionID == "" || len(sessionID) > 256 {
		return core.Result{}, errors.New("pty session_id is invalid")
	}

	var rawCwd string
	if req.Fields != nil {
		if c, ok := req.Fields["cwd"].(string); ok {
			rawCwd = c
		} else if w, ok := req.Fields["workspace"].(string); ok {
			rawCwd = w
		}
	}
	if rawCwd == "" || strings.ContainsRune(rawCwd, 0) {
		return core.Result{}, errors.New("pty workspace is invalid")
	}

	cleanCwd, err := filepath.Abs(rawCwd)
	if err != nil {
		return core.Result{}, err
	}
	target, err := filepath.EvalSymlinks(cleanCwd)
	if err != nil {
		return core.Result{}, fmt.Errorf("pty workspace is invalid: %w", err)
	}
	fi, err := os.Stat(target)
	if err != nil || !fi.IsDir() {
		return core.Result{}, errors.New("pty workspace must be an existing directory")
	}

	allowed := false
	for _, root := range r.allowedRoots {
		rel, err := filepath.Rel(root, target)
		if err == nil && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." && !filepath.IsAbs(rel) {
			allowed = true
			break
		}
	}
	if !allowed {
		return core.Result{}, errors.New("pty workspace is outside daemon allowlist")
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return core.Result{}, errors.New("pty runtime is closed")
	}
	if _, exists := r.sessions[sessionID]; exists || r.opening[sessionID] {
		r.mu.Unlock()
		return core.Result{}, errors.New("pty session is already daemon-owned")
	}
	r.opening[sessionID] = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.opening, sessionID); r.mu.Unlock() }()
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}

	shell := r.config.Shell
	if shell == "" {
		shell = findDefaultShell()
	}

	term, err := r.terminalFactory(shell, target, terminalEnvironment(), 80, 24)
	if err != nil {
		return core.Result{}, fmt.Errorf("spawn terminal: %w", err)
	}
	r.mu.Lock()
	if r.closed || ctx.Err() != nil {
		r.mu.Unlock()
		return core.Result{}, errors.Join(errors.New("pty startup was cancelled"), term.Close())
	}

	s := &session{
		sessionID: sessionID,
		terminal:  term,
		status:    "running",
		done:      make(chan struct{}),
	}
	r.sessions[sessionID] = s
	r.mu.Unlock()

	go r.readLoop(s)

	payload := map[string]any{"status": "running"}
	if pid := terminalPID(term); pid > 0 {
		payload["owner_pid"] = pid
		if r.config.Emit != nil {
			if err := r.config.Emit(sessionID, "runtime.owner", map[string]any{"owner_pid": pid}, "running"); err != nil {
				s.statusMu.Lock()
				s.status = "error"
				s.statusMu.Unlock()
				return core.Result{}, err
			}
		}
	}
	return core.Result{
		SessionID: sessionID,
		Status:    "running",
		Payload:   payload,
	}, nil
}

func terminalPID(term Terminal) int {
	type pidSource interface{ PID() int }
	source, ok := term.(pidSource)
	if !ok {
		return 0
	}
	if pid := source.PID(); pid > 0 {
		return pid
	}
	return 0
}

func (r *Runtime) Command(ctx context.Context, req core.Request) (core.Result, error) {
	sessionID := req.SessionID
	if sessionID == "" && req.Fields != nil {
		if sid, ok := req.Fields["session_id"].(string); ok {
			sessionID = sid
		}
	}

	r.mu.RLock()
	s := r.sessions[sessionID]
	r.mu.RUnlock()

	if s == nil {
		return core.Result{}, errors.New("pty session is not daemon-owned")
	}

	action := req.Action
	if action == "" && req.Fields != nil {
		if a, ok := req.Fields["action"].(string); ok {
			action = a
		}
	}

	switch action {
	case "session.send":
		var input string
		if req.Fields != nil {
			if in, ok := req.Fields["input"].(string); ok {
				input = in
			}
		}
		if len(input) == 0 || len(input) > 65536 {
			return core.Result{}, errors.New("pty input is invalid")
		}

		s.statusMu.RLock()
		status := s.status
		closed := s.closed
		s.statusMu.RUnlock()

		if closed || status != "running" {
			return core.Result{}, errors.New("pty session is closed")
		}

		s.writeMu.Lock()
		_, err := s.terminal.Write([]byte(input))
		s.writeMu.Unlock()
		if err != nil {
			return core.Result{}, fmt.Errorf("write to pty: %w", err)
		}

		return core.Result{
			SessionID: sessionID,
			Status:    status,
			Payload:   map[string]any{"status": status, "accepted": true},
		}, nil

	case "session.resize":
		var colsVal, rowsVal any
		if req.Fields != nil {
			colsVal = req.Fields["cols"]
			rowsVal = req.Fields["rows"]
		}
		cols, okCols := toInt(colsVal)
		rows, okRows := toInt(rowsVal)
		if !okCols || !okRows || cols < 20 || cols > 400 || rows < 10 || rows > 200 {
			return core.Result{}, errors.New("pty dimensions are invalid")
		}

		s.statusMu.RLock()
		status := s.status
		closed := s.closed
		s.statusMu.RUnlock()

		if closed || status != "running" {
			return core.Result{}, errors.New("pty session is closed")
		}

		if err := s.terminal.Resize(cols, rows); err != nil {
			return core.Result{}, fmt.Errorf("resize pty: %w", err)
		}

		return core.Result{
			SessionID: sessionID,
			Status:    status,
			Payload:   map[string]any{"status": status, "accepted": true},
		}, nil

	case "session.interrupt":
		s.statusMu.RLock()
		status := s.status
		closed := s.closed
		s.statusMu.RUnlock()

		if closed || status != "running" {
			return core.Result{}, errors.New("pty session is closed")
		}

		s.writeMu.Lock()
		_, err := s.terminal.Write([]byte{'\x03'})
		s.writeMu.Unlock()
		if err != nil {
			return core.Result{}, fmt.Errorf("interrupt pty: %w", err)
		}

		return core.Result{
			SessionID: sessionID,
			Status:    status,
			Payload:   map[string]any{"status": status, "accepted": true},
		}, nil

	case "session.close":
		if err := s.close(); err != nil {
			return core.Result{}, err
		}
		// Drain the terminal's final callback before retiring/reusing its opaque ID.
		select {
		case <-s.done:
		case <-ctx.Done():
			return core.Result{}, ctx.Err()
		case <-time.After(3 * time.Second):
			return core.Result{}, errors.New("pty output drain timed out")
		}
		r.mu.Lock()
		if r.sessions[sessionID] == s {
			delete(r.sessions, sessionID)
		}
		r.mu.Unlock()
		return core.Result{
			SessionID: sessionID,
			Status:    "idle",
			Payload:   map[string]any{"status": "idle", "closed": true},
		}, nil

	default:
		return core.Result{}, errors.New("pty runtime action is unsupported")
	}
}

func (r *Runtime) Snapshot() map[string]core.Result {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make(map[string]core.Result, len(r.sessions))
	for id, s := range r.sessions {
		s.statusMu.RLock()
		st := s.status
		s.statusMu.RUnlock()
		res[id] = core.Result{
			SessionID: id,
			Status:    st,
			Payload:   map[string]any{"status": st},
		}
	}
	return res
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	sessions := make([]*session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()

	var err error
	for _, s := range sessions {
		err = errors.Join(err, s.close())
	}
	return err
}

func (r *Runtime) readLoop(s *session) {
	buf := make([]byte, 4096)
	var pending []byte
	for {
		n, err := s.terminal.Read(buf)
		data := append(pending, buf[:n]...)
		prefix := 0
		for prefix < len(data) && utf8.FullRune(data[prefix:]) {
			_, size := utf8.DecodeRune(data[prefix:])
			prefix += size
		}
		if err != nil {
			prefix = len(data)
		}
		chunk := strings.ToValidUTF8(string(data[:prefix]), "\ufffd")
		pending = append([]byte(nil), data[prefix:]...)
		if chunk != "" {
			if r.config.Emit != nil {
				if emitErr := r.config.Emit(s.sessionID, "pty.output", map[string]any{"data": chunk}, "running"); emitErr != nil {
					s.statusMu.Lock()
					s.status = "error"
					s.statusMu.Unlock()
					break
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				s.statusMu.Lock()
				if !s.closed {
					s.status = "error"
				}
				s.statusMu.Unlock()
			}
			break
		}
	}

	s.close()
	s.statusMu.RLock()
	status := s.status
	s.statusMu.RUnlock()

	if r.config.Emit != nil {
		if err := r.config.Emit(s.sessionID, "pty.closed", map[string]any{}, status); err != nil {
			s.statusMu.Lock()
			s.status = "error"
			s.statusMu.Unlock()
		}
	}

	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int32:
		return int(val), true
	case int64:
		return int(val), true
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) || math.Trunc(val) != val || val < 0 || val > 400 {
			return 0, false
		}
		return int(val), true
	case float32:
		return toInt(float64(val))
	default:
		return 0, false
	}
}
