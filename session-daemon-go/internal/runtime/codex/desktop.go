package codex

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var eventIDPattern = regexp.MustCompile(`^[a-f0-9]{32}(?:[a-f0-9]{32})?$`)

type fileInfo struct {
	mtime int64
	name  string
	path  string
}

// StartDesktopStopObserver monitors ~/.codex/astrorder-observer/events for desktop stop notifications.
func StartDesktopStopObserver(ctx context.Context, config Config, emit Emitter, pollInterval time.Duration) {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".codex")
	}
	directory := filepath.Join(home, "astrorder-observer", "events")

	type cursor struct {
		mtime int64
		name  string
	}
	currentCursor := cursor{}

	filesAfter := func(cur cursor) ([]string, cursor) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, cur
		}
		var list []fileInfo
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Size() > 8192 {
				continue
			}
			list = append(list, fileInfo{
				mtime: info.ModTime().UnixNano(),
				name:  e.Name(),
				path:  filepath.Join(directory, e.Name()),
			})
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
			latest = cursor{mtime: list[len(list)-1].mtime, name: list[len(list)-1].name}
		}
		return fresh, latest
	}

	_, currentCursor = filesAfter(currentCursor)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			paths, nextCursor := filesAfter(currentCursor)
			currentCursor = nextCursor
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var row map[string]any
				if err := json.Unmarshal(data, &row); err != nil {
					continue
				}
				if row["event"] != "Stop" {
					continue
				}
				eventID, _ := row["id"].(string)
				stem := filepath.Base(path)
				stem = stem[:len(stem)-len(filepath.Ext(stem))]
				if eventID != stem || !eventIDPattern.MatchString(eventID) {
					continue
				}
				sessionID, _ := row["session_id"].(string)
				if sessionID == "" || len(sessionID) > 256 {
					continue
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
					continue
				}
				if math.Abs(float64(time.Now().Unix())-observedAt) > 300 {
					continue
				}
				turnID, _ := row["turn_id"].(string)
				if turnID == "" {
					turnID = eventID
				}
				if emit != nil {
					frame := map[string]any{
						"method": "turn/completed",
						"params": map[string]any{
							"turn": map[string]any{
								"id":     turnID,
								"status": "completed",
							},
						},
					}
					payload := map[string]any{
						"agent_id": config.AgentID,
						"frame":    frame,
					}
					_ = emit(sessionID, "codex.notification", payload, "idle")
				}
			}
		}
	}
}
