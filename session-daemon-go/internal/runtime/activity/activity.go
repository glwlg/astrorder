// Package activity watches a hook spool directory and turns spooled native
// events into journal events. Codex and Hermes share it, so local, WSL and SSH
// Agents report activity the same way.
package activity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Emit has the journal's Emit signature.
type Emit func(sessionID, event string, payload map[string]any, status string) error

// ToolThrottle bounds how often high-frequency tool hooks reach the journal.
// Lifecycle hooks (prompt, stop, session start/end) are never throttled.
const ToolThrottle = 5 * time.Second

var eventIDPattern = regexp.MustCompile(`^[a-f0-9]{32}(?:[a-f0-9]{32})?$`)

// Options configures Watch.
type Options struct {
	Dir     string
	AgentID string
	Emit    Emit
	// Known reports whether the daemon already tracks a session. Activity for a
	// tracked session is committed with an empty status so it never overrides the
	// real running/waiting state; an untracked session is registered as idle.
	// A nil Known disables activity events.
	Known func(string) bool
	Poll  time.Duration
	// OnStop, when set, receives every Stop event instead of it becoming plain
	// activity. Codex uses it to emit turn/completed.
	OnStop func(sessionID, turnID, eventID string)
}

type spoolFile struct {
	mtime int64
	name  string
	path  string
}

type cursor struct {
	mtime int64
	name  string
}

func filesAfter(dir string, cur cursor) ([]string, cursor) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, cur
	}
	var list []spoolFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Size() > 8192 {
			continue
		}
		list = append(list, spoolFile{info.ModTime().UnixNano(), e.Name(), filepath.Join(dir, e.Name())})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].mtime == list[j].mtime {
			return list[i].name < list[j].name
		}
		return list[i].mtime < list[j].mtime
	})
	var fresh []string
	latest := cur
	for _, f := range list {
		if f.mtime > cur.mtime || (f.mtime == cur.mtime && f.name > cur.name) {
			fresh = append(fresh, f.path)
		}
	}
	if len(list) > 0 {
		latest = cursor{list[len(list)-1].mtime, list[len(list)-1].name}
	}
	return fresh, latest
}

// Watch blocks until ctx ends. It starts at the current end of the spool and
// never replays history; missed activity is repaired by manual or startup
// reconciliation, not by polling sessions.
func Watch(ctx context.Context, opts Options) {
	poll := opts.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	_, current := filesAfter(opts.Dir, cursor{})
	lastActivity := map[string]time.Time{}
	seen := newSeenSet(4096)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var paths []string
		paths, current = filesAfter(opts.Dir, current)
		for _, path := range paths {
			// A file still being written can report a stale mtime and look new
			// again on the next poll, so the event id is the real identity.
			if !seen.add(filepath.Base(path)) {
				continue
			}
			handle(path, opts, lastActivity)
		}
	}
}

func handle(path string, opts Options, lastActivity map[string]time.Time) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var row map[string]any
	if json.Unmarshal(data, &row) != nil {
		return
	}
	stem := filepath.Base(path)
	stem = stem[:len(stem)-len(filepath.Ext(stem))]
	if id, _ := row["id"].(string); id != stem {
		return
	}
	handleRow(row, opts, lastActivity)
}

// handleRow validates one decoded spool event and forwards it. It is shared by
// the file watcher and the streamed tail used for SSH/WSL hosts.
func handleRow(row map[string]any, opts Options, lastActivity map[string]time.Time) {
	hookEvent, _ := row["event"].(string)
	if hookEvent == "" {
		return
	}
	eventID, _ := row["id"].(string)
	if !eventIDPattern.MatchString(eventID) {
		return
	}
	sessionID, _ := row["session_id"].(string)
	if sessionID == "" || len(sessionID) > 256 {
		return
	}
	var observedAt float64
	switch v := row["observed_at"].(type) {
	case float64:
		observedAt = v
	case int64:
		observedAt = float64(v)
	case int:
		observedAt = float64(v)
	default:
		return
	}
	if math.Abs(float64(time.Now().Unix())-observedAt) > 300 {
		return
	}
	if hookEvent == "Stop" && opts.OnStop != nil {
		turnID, _ := row["turn_id"].(string)
		if turnID == "" {
			turnID = eventID
		}
		opts.OnStop(sessionID, turnID, eventID)
		return
	}
	if opts.Emit == nil || opts.Known == nil {
		return
	}
	now := time.Now()
	tool := hookEvent == "PreToolUse" || hookEvent == "PostToolUse"
	if last, ok := lastActivity[sessionID]; tool && ok && now.Sub(last) < ToolThrottle {
		return
	}
	if len(lastActivity) > 4096 {
		clear(lastActivity)
	}
	lastActivity[sessionID] = now
	status := ""
	if !opts.Known(sessionID) {
		status = "idle"
	}
	_ = opts.Emit(sessionID, "native.activity", map[string]any{
		"agent_id":    opts.AgentID,
		"session_id":  sessionID,
		"observed_at": observedAt,
		"hook_event":  hookEvent,
	}, status)
}

// maxStreamLine bounds one streamed event; real events are a few hundred bytes.
const maxStreamLine = 16 * 1024

// streamDelimiter ends one JSON-lines event.
const streamDelimiter = byte(10)

// Consume reads JSON-lines events (the output of `astrorder-helper tail`) until
// the reader ends. Malformed or oversized lines are skipped, never fatal.
func Consume(reader io.Reader, opts Options) error {
	buffered := bufio.NewReaderSize(reader, 32*1024)
	lastActivity := map[string]time.Time{}
	seen := newSeenSet(4096)
	for {
		line, err := buffered.ReadSlice(streamDelimiter)
		if errors.Is(err, bufio.ErrBufferFull) {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = buffered.ReadSlice(streamDelimiter)
			}
			if err != nil {
				return err
			}
			continue
		}
		if len(line) > 0 && len(line) <= maxStreamLine {
			var row map[string]any
			if json.Unmarshal(line, &row) == nil {
				if id, _ := row["id"].(string); id != "" && seen.add(id) {
					handleRow(row, opts, lastActivity)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// seenSet remembers recently handled event files, evicting the oldest first.
type seenSet struct {
	limit int
	order []string
	items map[string]struct{}
}

func newSeenSet(limit int) *seenSet {
	return &seenSet{limit: limit, items: map[string]struct{}{}}
}

// add reports whether name is new.
func (s *seenSet) add(name string) bool {
	if _, ok := s.items[name]; ok {
		return false
	}
	s.items[name] = struct{}{}
	s.order = append(s.order, name)
	if len(s.order) > s.limit {
		delete(s.items, s.order[0])
		s.order = s.order[1:]
	}
	return true
}
