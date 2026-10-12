package agentremote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"astrorder.dev/app-server/internal/agentapi"
)

const MaxMCPLineBytes = 16 << 20 // 16MB

// RunStdio serves MCP requests line-by-line over stdin and stdout.
// If client is nil, tools/call returns a structured JSON-RPC error.
// Offline requests (initialize, tools/list, ping) succeed without client.
func RunStdio(ctx context.Context, in io.Reader, out io.Writer, client *Client) error {
	scanner := bufio.NewScanner(in)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, MaxBusinessBytes)

	var mu sync.Mutex
	writeMsg := func(resp map[string]any) error {
		if resp == nil {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(resp); err != nil {
			return err
		}
		_, err := out.Write(b.Bytes())
		return err
	}

	invoker := func(capability string, payload map[string]any) (any, error) {
		if client == nil {
			return nil, errors.New("remote agent client is not configured; offline tools/call unavailable")
		}
		return client.Invoke(ctx, capability, payload)
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var raw any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			_ = writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      nil,
				"error": map[string]any{
					"code":    -32700,
					"message": "Parse error",
				},
			})
			continue
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			_ = writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      nil,
				"error": map[string]any{
					"code":    -32700,
					"message": "Parse error: trailing content",
				},
			})
			continue
		}

		req, ok := raw.(map[string]any)
		if !ok {
			_ = writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      nil,
				"error": map[string]any{
					"code":    -32600,
					"message": "Invalid Request: expected JSON object",
				},
			})
			continue
		}

		idVal, hasID := req["id"]

		// Check if it's a notification: according to JSON-RPC 2.0, notifications have no 'id' member.
		// Notifications MUST NOT be replied to, even on errors or tools/call.
		if !hasID {
			_ = agentapi.HandleRPC(req, invoker)
			continue
		}

		resp := agentapi.HandleRPC(req, invoker)
		if resp != nil {
			// Ensure response retains exact ID from request (whether string, number, or null)
			resp["id"] = idVal
			if err := writeMsg(resp); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
