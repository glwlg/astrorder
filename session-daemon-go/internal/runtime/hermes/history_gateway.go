package hermes

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type gatewayHistoryCursor struct {
	Version int    `json:"version"`
	Session string `json:"session"`
	Anchor  string `json:"anchor"`
}

// Bind the continuation to an exact displayed native row and durable session,
// rather than a shifting numeric offset when new messages arrive.
func gatewayRowAnchor(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
func gatewayHistoryPage(sessionID string, response map[string]any, before string, limit int) (map[string]any, error) {
	messages, ok := response["messages"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid native history messages")
	}
	end := len(messages)
	if before != "" {
		if !strings.HasPrefix(before, "hermes-rpc:") || len(before) > 512 {
			return nil, fmt.Errorf("invalid gateway history cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(before, "hermes-rpc:"))
		if err != nil {
			return nil, fmt.Errorf("invalid gateway history cursor")
		}
		var cursor gatewayHistoryCursor
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&cursor) != nil || decoder.Decode(&struct{}{}) != io.EOF || cursor.Version != 1 || cursor.Session != sessionID || cursor.Anchor == "" {
			return nil, fmt.Errorf("foreign gateway history cursor")
		}
		end = -1
		for i, item := range messages {
			anchor, err := gatewayRowAnchor(item)
			if err != nil {
				return nil, err
			}
			if anchor == cursor.Anchor {
				if end != -1 {
					return nil, fmt.Errorf("ambiguous gateway history anchor")
				}
				end = i
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("gateway history anchor is unavailable")
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	items := append([]any{}, messages[start:end]...)
	var next any
	if start > 0 {
		anchor, err := gatewayRowAnchor(messages[start])
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(gatewayHistoryCursor{Version: 1, Session: sessionID, Anchor: anchor})
		token := "hermes-rpc:" + base64.RawURLEncoding.EncodeToString(raw)
		if len(token) > 512 {
			return nil, fmt.Errorf("gateway history cursor exceeds limit")
		}
		next = token
	}
	return map[string]any{"items": items, "next_cursor": next, "has_more": start > 0}, nil
}
