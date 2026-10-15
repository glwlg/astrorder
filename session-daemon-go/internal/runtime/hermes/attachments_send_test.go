package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestValidateWordAttachments(t *testing.T) {
	for _, test := range []struct{ name, media string }{
		{"说明.doc", "application/msword"},
		{"说明.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAttachments([]any{map[string]any{"name": test.name, "media_type": test.media, "content_base64": "eA=="}}); err != nil {
				t.Fatalf("Word attachment rejected: %v", err)
			}
		})
	}
	if err := validateAttachments([]any{map[string]any{"name": "program.exe", "media_type": "application/x-msdownload", "content_base64": "eA=="}}); err == nil {
		t.Fatal("unsupported attachment media type accepted")
	}
}

func TestSendAttachmentFailurePreventsPromptAndRollsBack(t *testing.T) {
	for _, test := range []struct {
		name        string
		attachments []any
		responses   map[string]map[string]any
		expected    []string
	}{
		{"image failure", []any{map[string]any{"name": "fail.png", "media_type": "image/png", "content_base64": "eA=="}}, nil, []string{"image.attach_bytes"}},
		{"file rejected", []any{map[string]any{"name": "no.txt", "media_type": "text/plain", "content_base64": "eA=="}}, map[string]map[string]any{"file.attach": {"attached": false}}, []string{"file.attach"}},
		{"partial rollback", []any{map[string]any{"name": "ok.png", "media_type": "image/png", "content_base64": "eA=="}, map[string]any{"name": "fail.png", "media_type": "image/png", "content_base64": "eA=="}}, map[string]map[string]any{"ok.png": {"attached": true, "path": "/native/ok.png"}, "image.detach": {"detached": true}}, []string{"image.attach_bytes", "image.attach_bytes", "image.detach"}},
		{"invalid encoding", []any{map[string]any{"name": "bad.png", "media_type": "image/png", "content_base64": "not base64"}}, nil, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := New(Config{})
			session := &SessionState{SessionID: "exact", Handle: "native-handle", Status: "idle", Metadata: map[string]any{}}
			a.sessions["exact"] = session
			reader, writer := io.Pipe()
			a.stdin = writer
			a.processDone = make(chan struct{})
			done := make(chan struct{})
			methods := []string{}
			go func() {
				defer close(done)
				dec := json.NewDecoder(reader)
				for {
					var req map[string]any
					if dec.Decode(&req) != nil {
						return
					}
					method := req["method"].(string)
					methods = append(methods, method)
					params := req["params"].(map[string]any)
					result := test.responses[method]
					if method == "image.attach_bytes" {
						result = test.responses[fmt.Sprint(params["filename"])]
					}
					a.mu.RLock()
					pending := a.pending[req["id"].(string)]
					a.mu.RUnlock()
					response := &rpcMessage{Result: result}
					if result == nil {
						response.Error = &rpcErrorObject{Code: -32000, Message: "fixture rejection"}
					}
					pending.ch <- response
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := a.handleSend(ctx, session, core.Request{SessionID: "exact", Fields: map[string]any{"text": "request", "attachments": test.attachments}})
			writer.Close()
			reader.Close()
			<-done
			if err == nil {
				t.Fatal("attachment failure was ignored")
			}
			if !reflect.DeepEqual(methods, test.expected) {
				t.Fatalf("native calls=%v expected=%v", methods, test.expected)
			}
		})
	}
}
