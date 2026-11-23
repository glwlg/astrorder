package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func parseModelsOutput(raw string) ([]map[string]any, error) {
	items := []map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
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

const modelsCacheTTL = 10 * time.Minute

func cloneModelsSlice(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, m := range items {
		cp := make(map[string]any, len(m))
		for k, v := range m {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out
}

func (a *Adapter) models(ctx context.Context) ([]map[string]any, error) {
	a.modelsMu.Lock()
	if len(a.modelsCache) > 0 && time.Since(a.modelsCachedAt) < modelsCacheTTL {
		cached := cloneModelsSlice(a.modelsCache)
		a.modelsMu.Unlock()
		return cached, nil
	}
	a.modelsMu.Unlock()

	items, err := a.fetchModelsUncached(ctx)
	if err != nil {
		return nil, err
	}
	a.modelsMu.Lock()
	a.modelsCache = cloneModelsSlice(items)
	a.modelsCachedAt = time.Now()
	a.modelsMu.Unlock()
	return items, nil
}

func (a *Adapter) fetchModelsUncached(ctx context.Context) ([]map[string]any, error) {
	baseArgs := cleanACPArgs(a.config.Arguments)
	if len(baseArgs) > 0 && baseArgs[len(baseArgs)-1] == "acp" {
		baseArgs = baseArgs[:len(baseArgs)-1]
	}
	args := append(baseArgs, "models")
	if a.sshStarter != nil {
		dir, err := a.workspace(a.config.Workspace)
		if err != nil {
			return nil, err
		}
		sess, err := a.sshStarter(ctx, ssh.CommandSpec{
			Executable: a.config.Executable,
			Args:       args,
			Dir:        dir,
		})
		if err != nil {
			return nil, fmt.Errorf("Antigravity remote model discovery failed: %w", err)
		}
		defer sess.Close()
		_ = sess.Stdin().Close()
		outBytes, readErr := io.ReadAll(sess.Stdout())
		exitStatus, waitErr := sess.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if waitErr != nil || readErr != nil || !exitStatus.ConfirmedRemoteExit || exitStatus.ExitCode != 0 {
			if waitErr != nil {
				return nil, fmt.Errorf("Antigravity remote model discovery failed: %w", waitErr)
			}
			return nil, fmt.Errorf("Antigravity remote model discovery failed (exit %d)", exitStatus.ExitCode)
		}
		return parseModelsOutput(string(outBytes))
	}
	cmd := exec.Command(a.config.Executable, args...)
	cmd.Env = process.NativeEnvironment(a.config.Environment)
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
	return parseModelsOutput(out.String())
}

func listSessionsForHome(ctx context.Context, home, agentID, connectionID string, posixPaths bool) ([]map[string]any, error) {
	db, err := openNative(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if os.IsNotExist(err) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()

	cols := tableColumns(ctx, db, "conversation_summaries")
	colExpr := func(name, fallback string) string {
		if cols[name] {
			return name
		}
		return fallback
	}
	parentFromTable := map[string]string{}
	if pRows, pErr := db.QueryContext(ctx, `SELECT conversation_id, parent_id FROM conversation_parents`); pErr == nil {
		for pRows.Next() {
			var cid, pid string
			if pRows.Scan(&cid, &pid) == nil && validID(cid) && validID(pid) && cid != pid {
				parentFromTable[cid] = pid
			}
		}
		pRows.Close()
	}

	query := fmt.Sprintf(
		`SELECT conversation_id,title,last_modified_time,workspace_uris,not_fully_idle,killed,%s,%s,%s FROM conversation_summaries WHERE killed=0 ORDER BY last_modified_time DESC`,
		colExpr("parent_conversation_id", `""`),
		colExpr("nesting_depth", `0`),
		colExpr("agent_name", `""`),
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, stamp, uris, parentID, agentName string
		var active, killed bool
		var nestingDepth int
		if err = rows.Scan(&id, &title, &stamp, &uris, &active, &killed, &parentID, &nestingDepth, &agentName); err != nil {
			return nil, err
		}
		if !validID(id) {
			continue
		}
		parentID = strings.TrimSpace(parentID)
		if parentID == "" {
			parentID = parentFromTable[id]
		}
		agentName = strings.TrimSpace(agentName)
		isChild := parentID != "" || nestingDepth > 0
		if isChild && strings.TrimSpace(title) == "" && agentName != "" {
			title = "@" + agentName
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
				if posixPaths {
					if workspace != "" {
						workspace = path.Clean(workspace)
					}
				} else {
					if runtime.GOOS == "windows" {
						workspace = strings.TrimPrefix(workspace, "/")
					}
					workspace = filepath.FromSlash(workspace)
				}
			}
		}
		if parsed, e := time.Parse("2006-01-02 15:04:05.999999999Z07:00", stamp); e == nil {
			stamp = parsed.UTC().Format(time.RFC3339Nano)
		}
		row := map[string]any{
			"workspace":         workspace,
			"id":                id,
			"agent_id":          agentID,
			"source_id":         agentID,
			"source_session_id": id,
			"title":             title,
			"workspace_uris":    uris,
			"status":            status,
			"updated_at":        stamp,
			"history_state":     "available",
			"control_state":     "unbound",
			"native_kind":       "rpc",
		}
		if isChild {
			row["ephemeral"] = true
			if parentID != "" {
				row["parent_session_id"] = parentID
			}
		}
		if connectionID != "" {
			row["connection_id"] = connectionID
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func queryParam(fields map[string]any, key string) (any, bool) {
	if fields == nil {
		return nil, false
	}
	if rp, ok := fields["request_params"].(map[string]any); ok && rp != nil {
		if v, exists := rp[key]; exists {
			return v, true
		}
	}
	v, exists := fields[key]
	return v, exists
}

func (a *Adapter) Query(ctx context.Context, r core.Request) (map[string]any, error) {
	switch str(r.Fields, "method") {
	case "account.vault.state", "account.vault.capture", "account.vault.quota", "account.vault.check", "account.vault.apply":
		if r.Action != "agy.account" {
			return nil, fmt.Errorf("unsupported Antigravity read query")
		}
		credential, _ := r.Fields["credential"].([]byte)
		return a.queryAgyAccounts(ctx, str(r.Fields, "method"), str(r.Fields, "account_id"), credential)
	case "models.list":
		items, err := a.models(ctx)
		return map[string]any{"items": items}, err
	case "quota.read":
		return a.quotaRead(ctx)
	case "session.usage":
		cid := ""
		if raw, ok := queryParam(r.Fields, "conversation_id"); ok {
			if s, ok := raw.(string); ok {
				cid = s
			}
		}
		readonly := false
		if raw, ok := queryParam(r.Fields, "readonly"); ok {
			readonly, _ = raw.(bool)
		}
		return a.readSessionUsage(ctx, cid, readonly)
	case "session.swarm":
		cid := ""
		if raw, ok := queryParam(r.Fields, "conversation_id"); ok {
			if s, ok := raw.(string); ok {
				cid = s
			}
		}
		readonly := false
		if raw, ok := queryParam(r.Fields, "readonly"); ok {
			readonly, _ = raw.(bool)
		}
		memberID := ""
		if raw, ok := queryParam(r.Fields, "member_conversation_id"); ok {
			if s, ok := raw.(string); ok {
				memberID = strings.TrimSpace(s)
			}
		}
		memberLimit := 0
		if raw, ok := queryParam(r.Fields, "member_limit"); ok {
			if n, ok := intVal(raw); ok {
				memberLimit = n
			}
		}
		return a.readSessionSwarm(ctx, cid, readonly, memberID, memberLimit)
	case "usage.summary":
		var sinceMs, untilMs int64
		if raw, ok := queryParam(r.Fields, "since_ms"); ok {
			sinceMs, _ = int64FromAny(raw)
		}
		if raw, ok := queryParam(r.Fields, "until_ms"); ok {
			untilMs, _ = int64FromAny(raw)
		}
		return a.readUsageSummary(ctx, sinceMs, untilMs)
	case "session.list":
		if a.remoteHelper != nil {
			res, err := a.remoteHelper(ctx, map[string]any{
				"domain":        "antigravity",
				"action":        "session.list",
				"agent_id":      a.config.AgentID,
				"connection_id": a.config.ConnectionID,
			})
			if err != nil {
				return nil, err
			}
			items := normalizeMapSlice(res["items"])
			for _, item := range items {
				item["agent_id"] = a.config.AgentID
				item["source_id"] = a.config.AgentID
				if a.config.ConnectionID != "" {
					item["connection_id"] = a.config.ConnectionID
				}
			}
			return map[string]any{"items": items}, nil
		}
		items, err := listSessionsForHome(ctx, a.config.Home, a.config.AgentID, a.config.ConnectionID, false)
		return map[string]any{"items": items}, err
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
	items := normalizeMapSlice(p["items"])
	probeID := a.probeSessionID()
	for _, item := range items {
		a.mu.Lock()
		owned := a.sessions[str(item, "id")]
		a.mu.Unlock()
		if owned != nil {
			ownedStatus := a.result(owned).Status
			if ownedStatus != "idle" || str(item, "status") != "running" {
				item["status"] = ownedStatus
			}
			item["control_state"] = "owned"
		}
		if a.config.ConnectionID != "" {
			item["connection_id"] = a.config.ConnectionID
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
		if err = a.emit(probeID, "connector.event", map[string]any{"agent_id": a.config.AgentID, "session_id": item["id"], "type": "session.upsert", "data": item}, ""); err != nil {
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
