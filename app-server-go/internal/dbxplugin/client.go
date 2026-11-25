package dbxplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/auth"
)

const (
	defaultMaxRows     = 200
	maxAllowedRows     = 1000
	defaultTimeoutSecs = 30
	maxTimeoutSecs     = 180
)

// ConnectionSummary is the normalized, desensitized DBX connection metadata.
type ConnectionSummary struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	DBType              string         `json:"db_type"`
	Host                string         `json:"host,omitempty"`
	Port                int            `json:"port,omitempty"`
	Database            string         `json:"database,omitempty"`
	Schema              string         `json:"schema,omitempty"`
	Environment         string         `json:"environment,omitempty"`
	IsProd              bool           `json:"is_prod"`
	ProductionDatabases []string       `json:"production_databases,omitempty"`
	Connected           bool           `json:"connected,omitempty"`
	rawConfig           map[string]any // kept server-side for auto-connect; never serialized to client
}

// IsDatabaseProd reports whether the given database on this connection is
// considered a production target by DBX.
func (c ConnectionSummary) IsDatabaseProd(database string) bool {
	if c.IsProd {
		return true
	}
	db := strings.TrimSpace(database)
	if db == "" {
		return false
	}
	for _, prodDB := range c.ProductionDatabases {
		if strings.EqualFold(strings.TrimSpace(prodDB), db) {
			return true
		}
	}
	return false
}

// ColumnMeta describes one column in a query result set.
type ColumnMeta struct {
	Name     string `json:"name"`
	DataType string `json:"data_type,omitempty"`
}

// StatementResult holds the normalized output of one SQL statement.
type StatementResult struct {
	Index        int          `json:"index"`
	SQL          string       `json:"sql,omitempty"`
	Columns      []ColumnMeta `json:"columns,omitempty"`
	Rows         [][]any      `json:"rows,omitempty"`
	RowCount     int          `json:"row_count"`
	AffectedRows *int64       `json:"affected_rows,omitempty"`
	ElapsedMs    int64        `json:"elapsed_ms"`
	Truncated    bool         `json:"truncated,omitempty"`
	Message      string       `json:"message,omitempty"`
	Error        string       `json:"error,omitempty"`
}

// ExecuteResult is the normalized response returned by POST /api/v1/plugins/dbx-exec/query.
type ExecuteResult struct {
	OK             bool              `json:"ok"`
	QueryID        string            `json:"query_id,omitempty"`
	ConnectionID   string            `json:"connection_id"`
	ConnectionName string            `json:"connection_name,omitempty"`
	Database       string            `json:"database,omitempty"`
	Schema         string            `json:"schema,omitempty"`
	IsProd         bool              `json:"is_prod"`
	Classification SQLClassification `json:"classification"`
	Results        []StatementResult `json:"results"`
	TotalElapsedMs int64             `json:"total_elapsed_ms"`
	Truncated      bool              `json:"truncated,omitempty"`
	Error          string            `json:"error,omitempty"`
}

type upstreamAuthError struct {
	status int
	detail string
}

func (e *upstreamAuthError) Error() string {
	return e.detail
}

type upstreamStatusError struct {
	status int
	detail string
}

func (e *upstreamStatusError) Error() string {
	return e.detail
}

func upstreamCookieHeader(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookies := r.Cookies()
	if len(cookies) == 0 {
		return ""
	}
	kept := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Name == auth.CookieName {
			continue
		}
		kept = append(kept, c.Name+"="+c.Value)
	}
	return strings.Join(kept, "; ")
}

func (h *handler) dbxHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: h.transport,
	}
}

func (h *handler) resolveUpstream(ctx context.Context) (*url.URL, error) {
	raw := h.configuredUpstreamRaw(ctx)
	return parseUpstreamURL(raw)
}

func (h *handler) doDBXJSON(
	ctx context.Context,
	target *url.URL,
	cookieHeader string,
	method string,
	apiPath string,
	query url.Values,
	body any,
	timeout time.Duration,
) ([]byte, int, error) {
	var bodyBytes []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		bodyBytes = encoded
	}

	attempt := func(prefix string) ([]byte, int, error) {
		reqURL := *target
		reqURL.Path = strings.TrimRight(target.Path, "/") + prefix + apiPath
		if len(query) > 0 {
			reqURL.RawQuery = query.Encode()
		}
		var reader io.Reader
		if bodyBytes != nil {
			reader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), reader)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Accept", "application/json")
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		}
		req.Header.Set("Origin", target.Scheme+"://"+target.Host)
		req.Header.Set("Referer", target.Scheme+"://"+target.Host+BasePath+"/")

		resp, err := h.dbxHTTPClient(timeout).Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("无法连接 DBX 服务（%s）：%w", target.String(), err)
		}
		defer resp.Body.Close()

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
		if readErr != nil {
			return nil, resp.StatusCode, readErr
		}
		return raw, resp.StatusCode, nil
	}

	raw, status, err := attempt(BasePath)
	if err != nil {
		return nil, status, err
	}
	if status == http.StatusNotFound {
		// Fall back to root path in case upstream DBX runs without DBX_PUBLIC_BASE_PATH.
		if rootRaw, rootStatus, rootErr := attempt(""); rootErr == nil && rootStatus != http.StatusNotFound {
			raw, status = rootRaw, rootStatus
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return raw, status, &upstreamAuthError{
			status: status,
			detail: "DBX 已启用密码保护或登录态已失效，请先在右侧「数据库」工作台完成解锁后再重试",
		}
	}
	if status < 200 || status >= 300 {
		return raw, status, &upstreamStatusError{
			status: status,
			detail: extractDBXErrorMessage(raw, status),
		}
	}
	return raw, status, nil
}

func (h *handler) listDBXConnections(ctx context.Context, cookieHeader string) ([]ConnectionSummary, error) {
	target, err := h.resolveUpstream(ctx)
	if err != nil {
		return nil, err
	}
	raw, status, err := h.doDBXJSON(ctx, target, cookieHeader, http.MethodGet, "/api/connection/list", nil, nil, 10*time.Second)
	if err != nil && status == http.StatusNotFound {
		raw, _, err = h.doDBXJSON(ctx, target, cookieHeader, http.MethodGet, "/api/connection", nil, nil, 10*time.Second)
	}
	if err != nil {
		return nil, err
	}
	return parseDBXConnections(raw)
}

func (h *handler) ensureDBXConnected(ctx context.Context, cookieHeader string, conn ConnectionSummary) error {
	target, err := h.resolveUpstream(ctx)
	if err != nil {
		return err
	}
	cfg := make(map[string]any, len(conn.rawConfig)+4)
	for k, v := range conn.rawConfig {
		cfg[k] = v
	}
	if _, ok := cfg["id"]; !ok && conn.ID != "" {
		cfg["id"] = conn.ID
	}
	if _, ok := cfg["name"]; !ok && conn.Name != "" {
		cfg["name"] = conn.Name
	}
	if _, ok := cfg["db_type"]; !ok && conn.DBType != "" {
		cfg["db_type"] = conn.DBType
	}
	payload := map[string]any{"config": cfg}
	_, _, err = h.doDBXJSON(ctx, target, cookieHeader, http.MethodPost, "/api/connection/connect", nil, payload, 20*time.Second)
	return err
}

func (h *handler) listDBXDatabases(ctx context.Context, cookieHeader string, conn ConnectionSummary) ([]string, error) {
	target, err := h.resolveUpstream(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("connection_id", conn.ID)
	raw, _, err := h.doDBXJSON(ctx, target, cookieHeader, http.MethodGet, "/api/schema/databases", query, nil, 15*time.Second)
	if err != nil && looksLikeNotConnectedError(err) {
		if connectErr := h.ensureDBXConnected(ctx, cookieHeader, conn); connectErr != nil {
			return nil, connectErr
		}
		raw, _, err = h.doDBXJSON(ctx, target, cookieHeader, http.MethodGet, "/api/schema/databases", query, nil, 15*time.Second)
	}
	if err != nil {
		return nil, err
	}
	return parseNameList(raw, "databases", "items", "data"), nil
}

func (h *handler) listDBXSchemas(ctx context.Context, cookieHeader string, conn ConnectionSummary, database string) ([]string, error) {
	target, err := h.resolveUpstream(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("connection_id", conn.ID)
	if database != "" {
		query.Set("database", database)
	}
	raw, _, err := h.doDBXJSON(ctx, target, cookieHeader, http.MethodGet, "/api/schema/schemas", query, nil, 15*time.Second)
	if err != nil && looksLikeNotConnectedError(err) {
		if connectErr := h.ensureDBXConnected(ctx, cookieHeader, conn); connectErr != nil {
			return nil, connectErr
		}
		raw, _, err = h.doDBXJSON(ctx, target, cookieHeader, http.MethodGet, "/api/schema/schemas", query, nil, 15*time.Second)
	}
	if err != nil {
		return nil, err
	}
	return parseNameList(raw, "schemas", "items", "data"), nil
}

type dbxExecuteParams struct {
	Conn        ConnectionSummary
	Database    string
	Schema      string
	SQL         string
	QueryID     string
	MaxRows     int
	TimeoutSecs int
	MultiStmt   bool
}

func (h *handler) executeDBXSQL(ctx context.Context, cookieHeader string, params dbxExecuteParams) ([]StatementResult, int64, bool, error) {
	target, err := h.resolveUpstream(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	maxRows := params.MaxRows
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}
	if maxRows > maxAllowedRows {
		maxRows = maxAllowedRows
	}
	timeoutSecs := params.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = defaultTimeoutSecs
	}
	if timeoutSecs > maxTimeoutSecs {
		timeoutSecs = maxTimeoutSecs
	}
	httpTimeout := time.Duration(timeoutSecs+15) * time.Second

	payload := map[string]any{
		"connectionId":    params.Conn.ID,
		"sql":             params.SQL,
		"maxRows":         maxRows,
		"timeoutSecs":     timeoutSecs,
		"continueOnError": false,
	}
	if params.Database != "" {
		payload["database"] = params.Database
	}
	if params.Schema != "" {
		payload["schema"] = params.Schema
	}
	if params.QueryID != "" {
		payload["queryId"] = params.QueryID
	}

	callExec := func() ([]byte, error) {
		primaryPath := "/api/query/execute-multi"
		fallbackPath := "/api/query/execute"
		if !params.MultiStmt {
			// Prefer execute-multi as well because a single statement can still return
			// rich timing/affected metadata, falling back to execute on 404.
			primaryPath = "/api/query/execute-multi"
		}
		raw, status, execErr := h.doDBXJSON(ctx, target, cookieHeader, http.MethodPost, primaryPath, nil, payload, httpTimeout)
		if execErr != nil && status == http.StatusNotFound {
			raw, _, execErr = h.doDBXJSON(ctx, target, cookieHeader, http.MethodPost, fallbackPath, nil, payload, httpTimeout)
		}
		return raw, execErr
	}

	started := time.Now()
	raw, execErr := callExec()
	if execErr != nil && looksLikeNotConnectedError(execErr) {
		if connectErr := h.ensureDBXConnected(ctx, cookieHeader, params.Conn); connectErr != nil {
			return nil, time.Since(started).Milliseconds(), false, connectErr
		}
		raw, execErr = callExec()
	}
	elapsed := time.Since(started).Milliseconds()
	if execErr != nil {
		return nil, elapsed, false, execErr
	}

	results, reportedElapsed, truncated, parseErr := parseDBXExecuteResponse(raw, maxRows)
	if parseErr != nil {
		return nil, elapsed, false, parseErr
	}
	if reportedElapsed > 0 {
		elapsed = reportedElapsed
	}
	return results, elapsed, truncated, nil
}

func (h *handler) cancelDBXQuery(ctx context.Context, cookieHeader string, queryID string) error {
	target, err := h.resolveUpstream(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"queryId":  queryID,
		"query_id": queryID,
	}
	_, _, err = h.doDBXJSON(ctx, target, cookieHeader, http.MethodPost, "/api/query/cancel", nil, payload, 10*time.Second)
	return err
}

func looksLikeNotConnectedError(err error) bool {
	if err == nil {
		return false
	}
	var authErr *upstreamAuthError
	if errors.As(err, &authErr) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"not connected",
		"connection not found",
		"no active connection",
		"pool not found",
		"connection is closed",
		"未连接",
		"尚未连接",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func extractDBXErrorMessage(raw []byte, status int) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return fmt.Sprintf("DBX 请求失败（HTTP %d）", status)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, key := range []string{"error", "message", "detail", "msg"} {
			if msg := asString(obj[key]); msg != "" {
				return msg
			}
			if nested, ok := obj[key].(map[string]any); ok {
				if msg := firstNonEmptyString(nested, "message", "detail", "error"); msg != "" {
					return msg
				}
			}
		}
	}
	if len(trimmed) > 500 {
		trimmed = trimmed[:500] + "…"
	}
	return trimmed
}

func parseDBXConnections(raw []byte) ([]ConnectionSummary, error) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("解析 DBX 连接列表失败：%w", err)
	}
	var items []any
	switch typed := parsed.(type) {
	case []any:
		items = typed
	case map[string]any:
		for _, key := range []string{"connections", "data", "items", "list"} {
			if arr, ok := typed[key].([]any); ok {
				items = arr
				break
			}
		}
	}
	out := make([]ConnectionSummary, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmptyString(m, "id", "connection_id", "connectionId")
		if id == "" {
			continue
		}
		name := firstNonEmptyString(m, "name", "display_name", "displayName")
		if name == "" {
			name = id
		}
		env := firstNonEmptyString(m, "environment", "env", "env_tag", "envTag")
		prodDBs := asStringSlice(firstPresent(m, "production_databases", "productionDatabases"))
		isProd := asBool(firstPresent(m, "is_production", "isProduction", "is_prod", "isProd", "production")) ||
			isProdEnvLabel(env)
		summary := ConnectionSummary{
			ID:                  id,
			Name:                name,
			DBType:              firstNonEmptyString(m, "db_type", "dbType", "type", "driver"),
			Host:                firstNonEmptyString(m, "host", "hostname"),
			Port:                int(asInt64(firstPresent(m, "port"))),
			Database:            firstNonEmptyString(m, "database", "default_database", "defaultDatabase"),
			Schema:              firstNonEmptyString(m, "schema", "default_schema", "defaultSchema"),
			Environment:         env,
			IsProd:              isProd,
			ProductionDatabases: prodDBs,
			Connected:           asBool(firstPresent(m, "connected", "is_connected", "isConnected")),
			rawConfig:           m,
		}
		out = append(out, summary)
	}
	return out, nil
}

func isProdEnvLabel(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production", "prd", "online", "生产", "生产环境":
		return true
	default:
		return false
	}
}

func parseNameList(raw []byte, wrapperKeys ...string) []string {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	var arr []any
	switch typed := parsed.(type) {
	case []any:
		arr = typed
	case map[string]any:
		for _, key := range wrapperKeys {
			if list, ok := typed[key].([]any); ok {
				arr = list
				break
			}
		}
	}
	seen := make(map[string]bool, len(arr))
	out := make([]string, 0, len(arr))
	for _, entry := range arr {
		var name string
		switch v := entry.(type) {
		case string:
			name = strings.TrimSpace(v)
		case map[string]any:
			name = firstNonEmptyString(v, "name", "database", "schema", "value", "label")
		}
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func parseDBXExecuteResponse(raw []byte, maxRows int) ([]StatementResult, int64, bool, error) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, false, fmt.Errorf("解析 DBX 查询结果失败：%w", err)
	}

	var entries []map[string]any
	var totalElapsed int64

	switch typed := parsed.(type) {
	case []any:
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				entries = append(entries, m)
			}
		}
	case map[string]any:
		totalElapsed = asInt64(firstPresent(typed, "total_elapsed_ms", "totalElapsedMs", "total_execution_time_ms", "totalExecutionTimeMs", "elapsed_ms", "elapsedMs", "execution_time_ms", "executionTimeMs"))
		foundList := false
		for _, key := range []string{"results", "result_sets", "resultSets", "statements", "data"} {
			if arr, ok := typed[key].([]any); ok {
				for _, item := range arr {
					if m, ok := item.(map[string]any); ok {
						entries = append(entries, m)
					}
				}
				foundList = true
				break
			}
		}
		if !foundList {
			// Single-statement flat payload.
			entries = []map[string]any{typed}
		}
	default:
		return nil, 0, false, errors.New("DBX 返回了不支持的查询结果结构")
	}

	results := make([]StatementResult, 0, len(entries))
	anyTruncated := false
	var sumElapsed int64

	for idx, entry := range entries {
		cols := parseColumns(firstPresent(entry, "columns", "fields", "headers"))
		rows := parseRows(firstPresent(entry, "rows", "records", "values"), cols)
		truncated := asBool(firstPresent(entry, "truncated", "has_more", "hasMore"))
		if maxRows > 0 && len(rows) > maxRows {
			rows = rows[:maxRows]
			truncated = true
		}
		if truncated {
			anyTruncated = true
		}
		elapsed := asInt64(firstPresent(entry, "elapsed_ms", "elapsedMs", "execution_time_ms", "executionTimeMs", "duration_ms", "durationMs"))
		sumElapsed += elapsed

		var affected *int64
		if rawAff := firstPresent(entry, "affected_rows", "affectedRows", "rows_affected", "rowsAffected", "affected"); rawAff != nil {
			val := asInt64(rawAff)
			affected = &val
		}

		res := StatementResult{
			Index:        idx,
			SQL:          firstNonEmptyString(entry, "sql", "statement", "query"),
			Columns:      cols,
			Rows:         rows,
			RowCount:     len(rows),
			AffectedRows: affected,
			ElapsedMs:    elapsed,
			Truncated:    truncated,
			Message:      firstNonEmptyString(entry, "message", "status_message", "statusMessage"),
			Error:        firstNonEmptyString(entry, "error", "error_message", "errorMessage"),
		}
		results = append(results, res)
	}

	if totalElapsed <= 0 {
		totalElapsed = sumElapsed
	}
	return results, totalElapsed, anyTruncated, nil
}

func parseColumns(raw any) []ColumnMeta {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	cols := make([]ColumnMeta, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			cols = append(cols, ColumnMeta{Name: v})
		case map[string]any:
			name := firstNonEmptyString(v, "name", "column_name", "columnName", "label", "key")
			dtype := firstNonEmptyString(v, "data_type", "dataType", "type", "typeName")
			cols = append(cols, ColumnMeta{Name: name, DataType: dtype})
		}
	}
	return cols
}

func parseRows(raw any, cols []ColumnMeta) [][]any {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	rows := make([][]any, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case []any:
			rows = append(rows, v)
		case map[string]any:
			row := make([]any, len(cols))
			for i, col := range cols {
				row[i] = v[col.Name]
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if val, ok := m[key]; ok && val != nil {
			return val
		}
	}
	return nil
}

func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := asString(m[key]); s != "" {
			return s
		}
	}
	return ""
}

func asString(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func asBool(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return b
	case float64:
		return typed != 0
	default:
		return false
	}
}

func asInt64(v any) int64 {
	switch typed := v.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		n, _ := typed.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n
	default:
		return 0
	}
}

func asStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s := asString(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}
