// Package observe records native Agent lifecycle events into a spool directory.
//
// It replaces the Python observer.py hook: one static binary that Codex hooks
// (and the Hermes plugin) can call on Windows, WSL and SSH hosts alike. The
// spool file format is the one the session daemon already consumes.
package observe

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// MaxInput bounds the hook payload read from stdin.
const MaxInput = 2 * 1024 * 1024

const (
	maxSpoolFiles = 2000
	spoolMaxAge   = 24 * time.Hour
)

// Events are the Codex hook events Astrorder subscribes to.
var Events = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"PermissionRequest", "SubagentStart", "SubagentStop", "Stop", "Interrupt",
}

var eventSet = func() map[string]bool {
	set := make(map[string]bool, len(Events))
	for _, name := range Events {
		set[name] = true
	}
	return set
}()

// Observation is one spooled event. It carries no message bodies.
type Observation struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"session_id"`
	Event      string  `json:"event"`
	ObservedAt float64 `json:"observed_at"`
	TurnID     string  `json:"turn_id,omitempty"`
	ToolCallID string  `json:"tool_call_id,omitempty"`
	ToolName   string  `json:"tool_name,omitempty"`
	SubagentID string  `json:"subagent_id,omitempty"`
}

// Parse validates a Codex hook payload and builds the observation.
func Parse(raw []byte, now time.Time) (Observation, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Observation{}, fmt.Errorf("hook payload is not JSON: %w", err)
	}
	name, _ := payload["hook_event_name"].(string)
	if !eventSet[name] {
		return Observation{}, errors.New("unsupported hook event")
	}
	sessionID := cleanText(payload["session_id"], 256)
	if sessionID == "" {
		return Observation{}, errors.New("native session ID missing")
	}
	row := Observation{
		SessionID:  sessionID,
		Event:      name,
		ObservedAt: float64(now.UnixMicro()) / 1e6,
		TurnID:     cleanText(payload["turn_id"], 160),
		ToolCallID: cleanText(payload["tool_call_id"], 160),
		ToolName:   cleanText(payload["tool_name"], 160),
		SubagentID: cleanText(payload["agent_id"], 160),
	}
	row.ID = row.identity()
	return row, nil
}

// identity is stable for turn-scoped events, so a retried hook cannot create a
// duplicate; other events get a random identity.
func (o Observation) identity() string {
	stable := o.TurnID != "" && (o.Event == "UserPromptSubmit" || o.Event == "Stop" || o.Event == "Interrupt" || o.ToolCallID != "")
	if !stable {
		buffer := make([]byte, 16)
		_, _ = rand.Read(buffer)
		return hex.EncodeToString(buffer)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", o.SessionID, o.Event, o.TurnID, o.ToolCallID, o.ToolName, o.SubagentID)))
	return hex.EncodeToString(sum[:])
}

func cleanText(value any, limit int) string {
	text, _ := value.(string)
	if text == "" || len(text) > limit {
		return ""
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return text
}

// Write stores the observation atomically. The spool is bounded: a stopped
// server must not make it grow without bound.
func Write(spool string, row Observation, now time.Time) (bool, error) {
	if err := os.MkdirAll(spool, 0o700); err != nil {
		return false, err
	}
	count := prune(spool, now)
	target := filepath.Join(spool, row.ID+".json")
	if _, err := os.Stat(target); err == nil {
		return true, nil
	}
	// A busy day produces more events than the cap. Dropping new events would
	// silence the very activity this spool exists to report, so make room by
	// evicting the oldest ones instead.
	if count >= maxSpoolFiles {
		evictOldest(spool, count-maxSpoolFiles+1)
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(spool, "*.tmp")
	if err != nil {
		return false, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	_ = os.Chmod(tempName, 0o600)
	return true, os.Rename(tempName, target)
}

// prune drops expired events and returns the number of files kept.
func prune(spool string, now time.Time) int {
	entries, err := os.ReadDir(spool)
	if err != nil {
		return 0
	}
	kept := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > spoolMaxAge {
			_ = os.Remove(filepath.Join(spool, entry.Name()))
			continue
		}
		kept++
	}
	return kept
}

// Run reads one hook payload from input and spools it. The caller swallows any
// failure: a broken bridge must never block the Agent, and the hook prints
// nothing, so Codex keeps its native approval flow.
func Run(spool string, input io.Reader, now time.Time) error {
	raw, err := io.ReadAll(io.LimitReader(input, MaxInput+1))
	if err != nil {
		return err
	}
	if len(raw) > MaxInput {
		return errors.New("hook payload is too large")
	}
	row, err := Parse(raw, now)
	if err != nil {
		return err
	}
	_, err = Write(spool, row, now)
	return err
}

// Cursor marks how far a reader has consumed a spool directory.
type Cursor struct {
	MTime int64
	Name  string
}

// After returns the spool files newer than cur, oldest first, and the new cursor.
func After(spool string, cur Cursor) ([]string, Cursor) {
	entries, err := os.ReadDir(spool)
	if err != nil {
		return nil, cur
	}
	type item struct {
		mtime int64
		name  string
	}
	var list []item
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Size() > 8192 {
			continue
		}
		list = append(list, item{info.ModTime().UnixNano(), entry.Name()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].mtime == list[j].mtime {
			return list[i].name < list[j].name
		}
		return list[i].mtime < list[j].mtime
	})
	var fresh []string
	latest := cur
	for _, entry := range list {
		if entry.mtime > cur.MTime || (entry.mtime == cur.MTime && entry.name > cur.Name) {
			fresh = append(fresh, filepath.Join(spool, entry.name))
		}
	}
	if len(list) > 0 {
		latest = Cursor{MTime: list[len(list)-1].mtime, Name: list[len(list)-1].name}
	}
	return fresh, latest
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}(?:[a-f0-9]{32})?$`)

// valid applies the checks the session daemon relies on: the event id must
// match both the file name and the spool id format, and the session id must be
// bounded. A forged or truncated file therefore cannot become an event.
func (o Observation) valid(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return o.ID == stem && idPattern.MatchString(o.ID) &&
		o.SessionID != "" && len(o.SessionID) <= 256 && o.Event != "" && o.ObservedAt > 0
}

// evictOldest removes the n oldest spooled events by modification time.
func evictOldest(spool string, n int) {
	entries, err := os.ReadDir(spool)
	if err != nil || n <= 0 {
		return
	}
	type item struct {
		mtime time.Time
		name  string
	}
	var list []item
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if info, err := entry.Info(); err == nil {
			list = append(list, item{info.ModTime(), entry.Name()})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mtime.Before(list[j].mtime) })
	for i := 0; i < n && i < len(list); i++ {
		_ = os.Remove(filepath.Join(spool, list[i].name))
	}
}
