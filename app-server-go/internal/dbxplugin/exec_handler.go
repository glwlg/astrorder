package dbxplugin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type executeQueryRequest struct {
	ConnectionID string `json:"connection_id"`
	Database     string `json:"database"`
	Schema       string `json:"schema"`
	SQL          string `json:"sql"`
	SourceKind   string `json:"source_kind"`
	SourceName   string `json:"source_name"`
	ProjectKey   string `json:"project_key"`
	WorkspaceKey string `json:"workspace_key"`
	SessionID    string `json:"session_id"`
	Confirmed    bool   `json:"confirmed"`
	MaxRows      int    `json:"max_rows"`
	TimeoutSecs  int    `json:"timeout_secs"`
	QueryID      string `json:"query_id"`
}

type cancelQueryRequest struct {
	QueryID string `json:"query_id"`
}

func (h *handler) serveExecAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	subpath := strings.TrimPrefix(r.URL.Path, ExecBasePath)

	switch subpath {
	case "/connections":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "method not allowed"})
			return
		}
		h.handleListConnections(w, r)

	case "/databases":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "method not allowed"})
			return
		}
		h.handleListDatabases(w, r)

	case "/schemas":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "method not allowed"})
			return
		}
		h.handleListSchemas(w, r)

	case "/bindings":
		switch r.Method {
		case http.MethodGet:
			h.handleGetBindings(w, r)
		case http.MethodPut:
			h.handlePutBinding(w, r)
		case http.MethodDelete:
			h.handleDeleteBinding(w, r)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "method not allowed"})
		}

	case "/query":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "method not allowed"})
			return
		}
		h.handleExecuteQuery(w, r)

	case "/cancel":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "method not allowed"})
			return
		}
		h.handleCancelQuery(w, r)

	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "not found"})
	}
}

func writeUpstreamError(w http.ResponseWriter, err error) {
	var authErr *upstreamAuthError
	if errors.As(err, &authErr) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": authErr.Error()})
		return
	}
	var statusErr *upstreamStatusError
	if errors.As(err, &statusErr) {
		code := statusErr.status
		if code < 400 || code >= 600 {
			code = http.StatusBadGateway
		}
		writeJSON(w, code, map[string]string{"detail": statusErr.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
}

func (h *handler) handleListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := h.listDBXConnections(r.Context(), upstreamCookieHeader(r))
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": conns})
}

func (h *handler) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	connID := strings.TrimSpace(r.URL.Query().Get("connection_id"))
	if connID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "缺少 connection_id 参数"})
		return
	}
	cookies := upstreamCookieHeader(r)
	conn, err := h.findConnection(r, cookies, connID)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	dbs, err := h.listDBXDatabases(r.Context(), cookies, conn)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": dbs})
}

func (h *handler) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	connID := strings.TrimSpace(r.URL.Query().Get("connection_id"))
	database := strings.TrimSpace(r.URL.Query().Get("database"))
	if connID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "缺少 connection_id 参数"})
		return
	}
	cookies := upstreamCookieHeader(r)
	conn, err := h.findConnection(r, cookies, connID)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	schemas, err := h.listDBXSchemas(r.Context(), cookies, conn, database)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": schemas})
}

func (h *handler) findConnection(r *http.Request, cookies, connID string) (ConnectionSummary, error) {
	conns, err := h.listDBXConnections(r.Context(), cookies)
	if err != nil {
		return ConnectionSummary{}, err
	}
	for _, c := range conns {
		if c.ID == connID {
			return c, nil
		}
	}
	return ConnectionSummary{}, &upstreamStatusError{
		status: http.StatusNotFound,
		detail: "未在 DBX 中找到指定的数据库连接，请刷新连接列表后重试",
	}
}

func (h *handler) handleGetBindings(w http.ResponseWriter, r *http.Request) {
	bindings, err := h.loadBindings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": sortedBindingsList(bindings)})
}

func (h *handler) handlePutBinding(w http.ResponseWriter, r *http.Request) {
	var payload ProjectDatabaseBinding
	if err := json.NewDecoder(io.LimitReader(r.Body, 512*1024)).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "请求体 JSON 格式无效"})
		return
	}
	normalized, err := normalizeAndValidateBinding(payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	// Best-effort lock: never downgrade a DBX-marked PROD connection/database.
	if conns, connErr := h.listDBXConnections(r.Context(), upstreamCookieHeader(r)); connErr == nil {
		byID := make(map[string]ConnectionSummary, len(conns))
		for _, c := range conns {
			byID[c.ID] = c
		}
		for i := range normalized.Targets {
			if c, ok := byID[normalized.Targets[i].ConnectionID]; ok {
				if c.IsDatabaseProd(normalized.Targets[i].Database) {
					normalized.Targets[i].IsProd = true
				}
				if normalized.Targets[i].DBType == "" && c.DBType != "" {
					normalized.Targets[i].DBType = c.DBType
				}
				if normalized.Targets[i].ConnectionName == normalized.Targets[i].ConnectionID && c.Name != "" {
					normalized.Targets[i].ConnectionName = c.Name
				}
			}
		}
	}

	h.bindingsMu.Lock()
	defer h.bindingsMu.Unlock()

	bindings, err := h.loadBindings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	if _, exists := bindings[normalized.ProjectKey]; !exists && len(bindings) >= maxBindingsCount {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "已达到项目数据库绑定数量上限"})
		return
	}
	if len(normalized.Targets) == 0 {
		delete(bindings, normalized.ProjectKey)
	} else {
		bindings[normalized.ProjectKey] = normalized
	}
	if err := h.saveBindingsMap(r.Context(), bindings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"binding":  normalized,
		"bindings": sortedBindingsList(bindings),
	})
}

func (h *handler) handleDeleteBinding(w http.ResponseWriter, r *http.Request) {
	projectKey := strings.TrimSpace(r.URL.Query().Get("project_key"))
	if projectKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "缺少 project_key 参数"})
		return
	}
	h.bindingsMu.Lock()
	defer h.bindingsMu.Unlock()

	bindings, err := h.loadBindings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	_, existed := bindings[projectKey]
	if existed {
		delete(bindings, projectKey)
		if err := h.saveBindingsMap(r.Context(), bindings); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":  existed,
		"bindings": sortedBindingsList(bindings),
	})
}

func (h *handler) handleExecuteQuery(w http.ResponseWriter, r *http.Request) {
	var req executeQueryRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "请求体 JSON 格式无效"})
		return
	}
	req.ConnectionID = strings.TrimSpace(req.ConnectionID)
	req.Database = strings.TrimSpace(req.Database)
	req.Schema = strings.TrimSpace(req.Schema)
	if req.ConnectionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "缺少 connection_id"})
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "SQL 内容不能为空"})
		return
	}

	classification := ClassifySQL(req.SQL)
	if classification.StatementCount == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "未检测到可执行的 SQL 语句"})
		return
	}

	cookies := upstreamCookieHeader(r)
	conn, err := h.findConnection(r, cookies, req.ConnectionID)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	effectiveDB := req.Database
	if effectiveDB == "" {
		effectiveDB = conn.Database
	}

	// Double-check PROD status from both live DBX metadata and saved project bindings.
	effectiveIsProd := conn.IsDatabaseProd(effectiveDB)
	if bindings, loadErr := h.loadBindings(r.Context()); loadErr == nil {
		if target := findBindingTarget(bindings, req.ProjectKey, req.WorkspaceKey, conn.ID, effectiveDB); target != nil && target.IsProd {
			effectiveIsProd = true
		}
	}

	// Server-side safety enforcement:
	// Non-PROD single read-only query runs directly; PROD or any non-read-only/multi-statement SQL MUST be confirmed.
	requiresConfirmation := effectiveIsProd || !classification.ReadOnlyAllowed
	if requiresConfirmation && !req.Confirmed {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"detail":                "该操作涉及生产环境或写入/多语句 SQL，需要二次确认后才能执行",
			"confirmation_required": true,
			"is_prod":               effectiveIsProd,
			"connection_name":       conn.Name,
			"database":              effectiveDB,
			"classification":        classification,
		})
		return
	}

	results, elapsedMs, truncated, execErr := h.executeDBXSQL(r.Context(), cookies, dbxExecuteParams{
		Conn:        conn,
		Database:    effectiveDB,
		Schema:      req.Schema,
		SQL:         req.SQL,
		QueryID:     strings.TrimSpace(req.QueryID),
		MaxRows:     req.MaxRows,
		TimeoutSecs: req.TimeoutSecs,
		MultiStmt:   classification.StatementCount > 1,
	})

	sqlHash, sqlPreview := hashAndPreviewSQL(req.SQL)

	if execErr != nil {
		var authErr *upstreamAuthError
		if errors.As(execErr, &authErr) {
			writeUpstreamError(w, execErr)
			return
		}
		var statusErr *upstreamStatusError
		if errors.As(execErr, &statusErr) {
			// Database / SQL execution failure returned by DBX: return structured ExecuteResult
			// so the UI renders the error card inline and lets the user send it back to chat.
			h.writeAuditEntry(SQLAuditEntry{
				ProjectKey:     req.ProjectKey,
				SessionID:      req.SessionID,
				ConnectionID:   conn.ID,
				ConnectionName: conn.Name,
				Database:       effectiveDB,
				Schema:         req.Schema,
				IsProd:         effectiveIsProd,
				SourceKind:     req.SourceKind,
				SourceName:     req.SourceName,
				RiskLevel:      classification.RiskLevel,
				StatementCount: classification.StatementCount,
				Confirmed:      req.Confirmed,
				OK:             false,
				ElapsedMs:      elapsedMs,
				SQLHash:        sqlHash,
				SQLPreview:     sqlPreview,
				Error:          statusErr.Error(),
			})
			writeJSON(w, http.StatusOK, ExecuteResult{
				OK:             false,
				QueryID:        strings.TrimSpace(req.QueryID),
				ConnectionID:   conn.ID,
				ConnectionName: conn.Name,
				Database:       effectiveDB,
				Schema:         req.Schema,
				IsProd:         effectiveIsProd,
				Classification: classification,
				Results:        []StatementResult{},
				TotalElapsedMs: elapsedMs,
				Error:          statusErr.Error(),
			})
			return
		}
		writeUpstreamError(w, execErr)
		return
	}

	ok := true
	firstErr := ""
	totalRows := 0
	for _, stmt := range results {
		totalRows += stmt.RowCount
		if stmt.Error != "" && ok {
			ok = false
			firstErr = stmt.Error
		}
	}

	h.writeAuditEntry(SQLAuditEntry{
		ProjectKey:     req.ProjectKey,
		SessionID:      req.SessionID,
		ConnectionID:   conn.ID,
		ConnectionName: conn.Name,
		Database:       effectiveDB,
		Schema:         req.Schema,
		IsProd:         effectiveIsProd,
		SourceKind:     req.SourceKind,
		SourceName:     req.SourceName,
		RiskLevel:      classification.RiskLevel,
		StatementCount: classification.StatementCount,
		Confirmed:      req.Confirmed,
		OK:             ok,
		ElapsedMs:      elapsedMs,
		TotalRows:      totalRows,
		SQLHash:        sqlHash,
		SQLPreview:     sqlPreview,
		Error:          firstErr,
	})

	writeJSON(w, http.StatusOK, ExecuteResult{
		OK:             ok,
		QueryID:        strings.TrimSpace(req.QueryID),
		ConnectionID:   conn.ID,
		ConnectionName: conn.Name,
		Database:       effectiveDB,
		Schema:         req.Schema,
		IsProd:         effectiveIsProd,
		Classification: classification,
		Results:        results,
		TotalElapsedMs: elapsedMs,
		Truncated:      truncated,
		Error:          firstErr,
	})
}

func (h *handler) handleCancelQuery(w http.ResponseWriter, r *http.Request) {
	var req cancelQueryRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "请求体 JSON 格式无效"})
		return
	}
	req.QueryID = strings.TrimSpace(req.QueryID)
	if req.QueryID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "缺少 query_id"})
		return
	}
	if err := h.cancelDBXQuery(r.Context(), upstreamCookieHeader(r), req.QueryID); err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "query_id": req.QueryID})
}
