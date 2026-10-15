package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	core "astrorder.dev/session-daemon/internal/runtime"
)

type historyCursor struct {
	Thread string `json:"thread"`
	Agent  string `json:"agent"`
	Cursor string `json:"cursor"`
}

func (a *Adapter) historyPage(ctx context.Context, rt *Runtime, r core.Request) (core.Result, error) {
	limit := 0
	switch value := r.Fields["limit"].(type) {
	case int:
		limit = value
	case float64:
		if value == float64(int(value)) {
			limit = int(value)
		}
	}
	if limit < 1 || limit > 200 {
		return core.Result{}, fmt.Errorf("history limit must be between 1 and 200")
	}
	before, ok := r.Fields["before"].(string)
	if value, present := r.Fields["before"]; present && value != nil && !ok {
		return core.Result{}, fmt.Errorf("invalid history cursor")
	}
	var cursor any
	if before != "" {
		if len(before) > 512 || !strings.HasPrefix(before, "codex:") {
			return core.Result{}, fmt.Errorf("invalid history cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(before, "codex:"))
		if err != nil {
			return core.Result{}, fmt.Errorf("invalid history cursor")
		}
		var decoded historyCursor
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&decoded) != nil || decoder.Decode(&struct{}{}) != io.EOF || decoded.Thread != r.SessionID || decoded.Agent != a.config.AgentID || decoded.Cursor == "" {
			return core.Result{}, fmt.Errorf("foreign history cursor")
		}
		cursor = decoded.Cursor
	}
	response, err := rt.request(ctx, "thread/items/list", map[string]any{"threadId": r.SessionID, "cursor": cursor, "limit": limit, "sortDirection": "desc"})
	if err != nil {
		return core.Result{}, err
	}
	rows, ok := response["data"].([]any)
	if !ok || len(rows) > limit {
		return core.Result{}, fmt.Errorf("invalid native history page")
	}
	items := make([]any, len(rows))
	for i := range rows {
		items[len(rows)-1-i] = rows[i]
	}
	var next any
	if value, present := response["nextCursor"]; present && value != nil {
		text, ok := value.(string)
		if !ok || text == "" || text == cursor {
			return core.Result{}, fmt.Errorf("invalid native history continuation")
		}
		raw, _ := json.Marshal(historyCursor{Thread: r.SessionID, Agent: a.config.AgentID, Cursor: text})
		token := "codex:" + base64.RawURLEncoding.EncodeToString(raw)
		if len(token) > 512 {
			return core.Result{}, fmt.Errorf("native history cursor exceeds limit")
		}
		next = token
	}
	if before == "" {
		rt.refreshGoal(ctx, r.SessionID)
	}
	return core.Result{SessionID: r.SessionID, Status: rt.Status(), Payload: map[string]any{"items": items, "next_cursor": next, "has_more": next != nil, "native": response}}, nil
}
