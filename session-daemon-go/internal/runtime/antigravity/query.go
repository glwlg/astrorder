package antigravity

import (
	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (a *Adapter) models(ctx context.Context) ([]map[string]any, error) {
	args := append(append([]string{}, a.config.Arguments...), "models")
	cmd := exec.Command(a.config.Executable, args...)
	cmd.Env = append(os.Environ(), a.config.Environment...)
	cmd.Dir = a.config.Workspace
	var out bytes.Buffer
	cmd.Stdout = &out
	owner, err := process.StartOwnedCommand(cmd)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer owner.Close()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = owner.Close()
		<-done
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("Antigravity model discovery failed: %w", err)
	}
	items := []map[string]any{}
	for _, line := range strings.Split(out.String(), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(parts) == 2 && parts[0] != "" {
			items = append(items, map[string]any{"id": parts[0], "name": parts[1], "label": parts[1], "provider": "antigravity", "efforts": []string{}})
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("Antigravity returned no recognizable models")
	}
	return items, nil
}
func (a *Adapter) Query(ctx context.Context, r core.Request) (map[string]any, error) {
	switch str(r.Fields, "method") {
	case "models.list":
		items, err := a.models(ctx)
		return map[string]any{"items": items}, err
	case "session.list":
		db, err := openNative(filepath.Join(a.config.Home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
		if os.IsNotExist(err) {
			return map[string]any{"items": []any{}}, nil
		}
		if err != nil {
			return nil, err
		}
		defer db.Close()
		rows, err := db.QueryContext(ctx, `SELECT conversation_id,title,last_modified_time,workspace_uris,not_fully_idle,killed FROM conversation_summaries WHERE killed=0 ORDER BY last_modified_time DESC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, title, stamp, uris string
			var active, killed bool
			if err = rows.Scan(&id, &title, &stamp, &uris, &active, &killed); err != nil {
				return nil, err
			}
			if !validID(id) {
				continue
			}
			status := "idle"
			if active {
				status = "running"
			}
			workspace := ""
			var paths []string
			if json.Unmarshal([]byte(uris), &paths) == nil && len(paths) > 0 {
				if u, e := url.Parse(paths[0]); e == nil && u.Scheme == "file" {
					workspace = u.Path
					if runtime.GOOS == "windows" {
						workspace = strings.TrimPrefix(workspace, "/")
					}
					workspace = filepath.FromSlash(workspace)
				}
			}
			if parsed, e := time.Parse("2006-01-02 15:04:05.999999999Z07:00", stamp); e == nil {
				stamp = parsed.UTC().Format(time.RFC3339Nano)
			}
			items = append(items, map[string]any{"workspace": workspace, "id": id, "agent_id": a.config.AgentID, "source_id": a.config.AgentID, "source_session_id": id, "title": title, "workspace_uris": uris, "status": status, "updated_at": stamp, "history_state": "available", "control_state": "unbound", "native_kind": "rpc"})
		}
		return map[string]any{"items": items}, rows.Err()
	default:
		return nil, fmt.Errorf("unsupported Antigravity read query")
	}
}

// RefreshCatalog publishes metadata only. Native message bodies are never copied
// into Astrorder's session catalog or its durable App projection.
func (a *Adapter) RefreshCatalog(ctx context.Context) error {
	p, err := a.Query(ctx, core.Request{Fields: map[string]any{"method": "session.list"}})
	if err != nil {
		return err
	}
	items, _ := p["items"].([]map[string]any)
	for _, item := range items {
		a.mu.Lock()
		owned := a.sessions[str(item, "id")]
		a.mu.Unlock()
		if owned != nil {
			item["status"] = a.result(owned).Status
			item["control_state"] = "owned"
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		key := str(item, "id")
		a.mu.Lock()
		previous := a.catalog[key]
		a.mu.Unlock()
		if previous == string(encoded) {
			continue
		}
		if err = a.emit("probe-antigravity-local", "connector.event", map[string]any{"agent_id": a.config.AgentID, "session_id": item["id"], "type": "session.upsert", "data": item}, ""); err != nil {
			return err
		}
		a.mu.Lock()
		a.catalog[key] = string(encoded)
		a.mu.Unlock()
	}
	return nil
}
func (a *Adapter) Observe(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		_ = a.RefreshCatalog(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
