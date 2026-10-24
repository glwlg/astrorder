package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestCanonicalImageMissingReceiptPreventsPrompt(t *testing.T) {
	for _, receipt := range []map[string]any{
		{"attached": true},
		{"attached": true, "path": "C:/cache/image.png"},
		{"attached": true, "path": "C:/cache/image.png", "mime": "text/plain"},
	} {
		a := New(Config{})
		a.canonical = true
		session := &SessionState{SessionID: "exact", Handle: "native-handle", Status: "idle", Metadata: map[string]any{}}
		a.sessions["exact"] = session
		reader, writer := io.Pipe()
		a.stdin = writer
		a.processDone = make(chan struct{})
		var methods []string
		done := make(chan struct{})
		go func() {
			defer close(done)
			decoder := json.NewDecoder(reader)
			for {
				var request map[string]any
				if decoder.Decode(&request) != nil {
					return
				}
				methods = append(methods, fmt.Sprint(request["method"]))
				a.mu.RLock()
				pending := a.pending[request["id"].(string)]
				a.mu.RUnlock()
				pending.ch <- &rpcMessage{Result: receipt}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := a.handleSend(ctx, session, core.Request{Fields: map[string]any{"text": "request", "attachments": []any{map[string]any{"name": "image.png", "media_type": "image/png", "content_base64": "eA=="}}}})
		cancel()
		writer.Close()
		reader.Close()
		<-done
		if err == nil {
			t.Fatal("invalid staging receipt accepted")
		}
		if !reflect.DeepEqual(methods, []string{"image.attach_bytes"}) {
			t.Fatalf("prompt or legacy detach dispatched after invalid staging: %v", methods)
		}
	}
}

func TestSendImageSubmissionContract(t *testing.T) {
	for _, canonical := range []bool{true, false} {
		for _, text := range []string{"请看截图", ""} {
			t.Run(fmt.Sprintf("canonical=%t/text=%t", canonical, text != ""), func(t *testing.T) {
				a := New(Config{})
				a.canonical = canonical
				session := &SessionState{SessionID: "exact", Handle: "native-handle", Status: "idle", Metadata: map[string]any{}}
				a.sessions["exact"] = session
				reader, writer := io.Pipe()
				a.stdin = writer
				a.processDone = make(chan struct{})
				var submission map[string]any
				done := make(chan struct{})
				go func() {
					defer close(done)
					decoder := json.NewDecoder(reader)
					for {
						var request map[string]any
						if decoder.Decode(&request) != nil {
							return
						}
						result := map[string]any{}
						switch request["method"] {
						case "image.attach_bytes":
							// Canonical upload stages bytes only: it does not queue an image.
							result = map[string]any{"attached": true, "path": `C:\profile\cache\images\截图 1.png`, "mime": "image/png"}
						case "prompt.submit":
							submission = request["params"].(map[string]any)
							result = map[string]any{"status": "streaming"}
						}
						a.mu.RLock()
						pending := a.pending[request["id"].(string)]
						a.mu.RUnlock()
						pending.ch <- &rpcMessage{Result: result}
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_, err := a.handleSend(ctx, session, core.Request{Fields: map[string]any{"text": text, "attachments": []any{map[string]any{"name": "截图.png", "media_type": "image/png", "content_base64": "eA=="}}}})
				writer.Close()
				reader.Close()
				<-done
				if err != nil {
					t.Fatal(err)
				}
				if submission["text"] != text {
					t.Fatalf("changed user text: %#v", submission)
				}
				if canonical {
					expected := []any{map[string]any{"path": `C:\profile\cache\images\截图 1.png`, "mime": "image/png"}}
					if !reflect.DeepEqual(submission["attachments"], expected) {
						t.Fatalf("staged image omitted from canonical admission: %#v", submission)
					}
				} else if _, exists := submission["attachments"]; exists {
					t.Fatal("legacy queued-image protocol changed")
				}
			})
		}
	}
}
