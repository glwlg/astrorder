package dbxplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	prefProjectBindings = "dbx:project_bindings"
	maxBindingsCount    = 500
	maxTargetsPerProj   = 32
)

// ProjectDatabaseTarget binds one DBX connection + database (+ optional schema) to a project.
type ProjectDatabaseTarget struct {
	ID             string `json:"id"`
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	DBType         string `json:"db_type,omitempty"`
	Database       string `json:"database"`
	Schema         string `json:"schema,omitempty"`
	Label          string `json:"label,omitempty"`
	IsProd         bool   `json:"is_prod"`
	IsDefault      bool   `json:"is_default"`
}

// ProjectDatabaseBinding stores all DBX database targets bound to one Astrorder project.
type ProjectDatabaseBinding struct {
	ProjectKey   string                  `json:"project_key"`
	WorkspaceKey string                  `json:"workspace_key,omitempty"`
	ProjectLabel string                  `json:"project_label,omitempty"`
	Targets      []ProjectDatabaseTarget `json:"targets"`
	UpdatedAt    string                  `json:"updated_at"`
}

// SQLAuditEntry is appended to dbx-sql-audit.jsonl for every executed query attempt.
type SQLAuditEntry struct {
	Timestamp      string `json:"timestamp"`
	ProjectKey     string `json:"project_key,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name,omitempty"`
	Database       string `json:"database,omitempty"`
	Schema         string `json:"schema,omitempty"`
	IsProd         bool   `json:"is_prod"`
	SourceKind     string `json:"source_kind,omitempty"`
	SourceName     string `json:"source_name,omitempty"`
	RiskLevel      string `json:"risk_level"`
	StatementCount int    `json:"statement_count"`
	Confirmed      bool   `json:"confirmed"`
	OK             bool   `json:"ok"`
	ElapsedMs      int64  `json:"elapsed_ms"`
	TotalRows      int    `json:"total_rows"`
	SQLHash        string `json:"sql_hash"`
	SQLPreview     string `json:"sql_preview"`
	Error          string `json:"error,omitempty"`
}

func (h *handler) loadBindings(ctx context.Context) (map[string]ProjectDatabaseBinding, error) {
	result := make(map[string]ProjectDatabaseBinding)
	if h.store == nil {
		return result, nil
	}
	pref, err := h.store.GetPreference(ctx, prefProjectBindings)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return result, nil
	}
	encoded, err := json.Marshal(pref.Value)
	if err != nil {
		return nil, err
	}
	var decoded map[string]ProjectDatabaseBinding
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return result, nil
	}
	for key, b := range decoded {
		if b.ProjectKey == "" {
			b.ProjectKey = key
		}
		if b.Targets == nil {
			b.Targets = []ProjectDatabaseTarget{}
		}
		result[b.ProjectKey] = b
	}
	return result, nil
}

func (h *handler) saveBindingsMap(ctx context.Context, bindings map[string]ProjectDatabaseBinding) error {
	if h.store == nil {
		return errors.New("当前服务端未启用持久化存储，无法保存项目数据库绑定")
	}
	return h.store.SetPreference(ctx, prefProjectBindings, bindings, true)
}

func sortedBindingsList(bindings map[string]ProjectDatabaseBinding) []ProjectDatabaseBinding {
	keys := make([]string, 0, len(bindings))
	for k := range bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ProjectDatabaseBinding, 0, len(keys))
	for _, k := range keys {
		b := bindings[k]
		if b.Targets == nil {
			b.Targets = []ProjectDatabaseTarget{}
		}
		out = append(out, b)
	}
	return out
}

func normalizeAndValidateBinding(in ProjectDatabaseBinding) (ProjectDatabaseBinding, error) {
	in.ProjectKey = strings.TrimSpace(in.ProjectKey)
	if in.ProjectKey == "" || utf8.RuneCountInString(in.ProjectKey) > 1024 {
		return ProjectDatabaseBinding{}, errors.New("project_key 不能为空且不超过 1024 个字符")
	}
	in.WorkspaceKey = strings.TrimSpace(in.WorkspaceKey)
	in.ProjectLabel = strings.TrimSpace(in.ProjectLabel)
	if len(in.Targets) > maxTargetsPerProj {
		return ProjectDatabaseBinding{}, fmt.Errorf("单个项目最多绑定 %d 个数据库目标", maxTargetsPerProj)
	}
	seenIDs := make(map[string]bool, len(in.Targets))
	normalized := make([]ProjectDatabaseTarget, 0, len(in.Targets))
	defaultIdx := -1
	for idx, t := range in.Targets {
		t.ConnectionID = strings.TrimSpace(t.ConnectionID)
		t.ConnectionName = strings.TrimSpace(t.ConnectionName)
		t.DBType = strings.TrimSpace(t.DBType)
		t.Database = strings.TrimSpace(t.Database)
		t.Schema = strings.TrimSpace(t.Schema)
		t.Label = strings.TrimSpace(t.Label)
		t.ID = strings.TrimSpace(t.ID)
		if t.ConnectionID == "" {
			return ProjectDatabaseBinding{}, fmt.Errorf("第 %d 个数据库目标缺少 connection_id", idx+1)
		}
		if t.ConnectionName == "" {
			t.ConnectionName = t.ConnectionID
		}
		if t.ID == "" {
			t.ID = fmt.Sprintf("%s::%s::%s", t.ConnectionID, t.Database, t.Schema)
		}
		if seenIDs[t.ID] {
			t.ID = fmt.Sprintf("%s#%d", t.ID, idx+1)
		}
		seenIDs[t.ID] = true
		if t.IsDefault && defaultIdx < 0 {
			defaultIdx = len(normalized)
		}
		t.IsDefault = false
		normalized = append(normalized, t)
	}
	if len(normalized) > 0 {
		if defaultIdx < 0 {
			defaultIdx = 0
		}
		normalized[defaultIdx].IsDefault = true
	}
	in.Targets = normalized
	in.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return in, nil
}

func findBindingTarget(bindings map[string]ProjectDatabaseBinding, projectKey, workspaceKey, connectionID, database string) *ProjectDatabaseTarget {
	check := func(b ProjectDatabaseBinding) *ProjectDatabaseTarget {
		var fallback *ProjectDatabaseTarget
		for i := range b.Targets {
			t := &b.Targets[i]
			if t.ConnectionID != connectionID {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(t.Database), strings.TrimSpace(database)) {
				return t
			}
			if fallback == nil {
				fallback = t
			}
		}
		return fallback
	}
	if pk := strings.TrimSpace(projectKey); pk != "" {
		if b, ok := bindings[pk]; ok {
			if found := check(b); found != nil {
				return found
			}
		}
	}
	if wk := strings.TrimSpace(workspaceKey); wk != "" {
		for _, b := range bindings {
			if strings.EqualFold(strings.TrimSpace(b.WorkspaceKey), wk) {
				if found := check(b); found != nil {
					return found
				}
			}
		}
	}
	// Last-resort check across all bindings for the same connection+database so a manually
	// marked PROD target is never bypassed if project_key was omitted.
	for _, b := range bindings {
		for i := range b.Targets {
			t := &b.Targets[i]
			if t.ConnectionID == connectionID && t.IsProd &&
				(t.Database == "" || strings.EqualFold(strings.TrimSpace(t.Database), strings.TrimSpace(database))) {
				return t
			}
		}
	}
	return nil
}

func (h *handler) writeAuditEntry(entry SQLAuditEntry) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	h.logger.Info(
		"dbx inline sql execution",
		"connection_id", entry.ConnectionID,
		"database", entry.Database,
		"is_prod", entry.IsProd,
		"risk_level", entry.RiskLevel,
		"confirmed", entry.Confirmed,
		"ok", entry.OK,
		"elapsed_ms", entry.ElapsedMs,
		"sql_hash", entry.SQLHash,
	)
	if strings.TrimSpace(h.auditDir) == "" {
		return
	}
	h.auditMu.Lock()
	defer h.auditMu.Unlock()
	if err := os.MkdirAll(h.auditDir, 0o755); err != nil {
		h.logger.Warn("failed to create dbx audit directory", "dir", h.auditDir, "error", err)
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(h.auditDir, "dbx-sql-audit.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		h.logger.Warn("failed to open dbx audit file", "path", path, "error", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func hashAndPreviewSQL(sql string) (string, string) {
	sum := sha256.Sum256([]byte(sql))
	hash := hex.EncodeToString(sum[:8])
	preview := strings.Join(strings.Fields(strings.TrimSpace(sql)), " ")
	if len(preview) > 240 {
		preview = preview[:240] + "…"
	}
	return hash, preview
}
