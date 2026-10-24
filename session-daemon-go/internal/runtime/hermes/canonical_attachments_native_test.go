package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// Explicit fixture opt-in; the Python bridge exercises the installed authority's
// actual image staging/admission/MessageEvent path in a private HERMES_HOME.
func TestCanonicalImageNativeAdmission(t *testing.T) {
	python, source := os.Getenv("ASTRORDER_IMAGE_CONTRACT_PYTHON"), os.Getenv("ASTRORDER_IMAGE_CONTRACT_SOURCE")
	if python == "" || source == "" {
		t.Skip("explicit private native image fixture required")
	}
	bridge, err := filepath.Abs(filepath.Join("testdata", "image_admission.py"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, bridge)
	cmd.Env = append(os.Environ(), "HERMES_HOME="+t.TempDir(), "PYTHONPATH="+source, "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{})
	a.canonical = true
	a.stdin = input
	a.processDone = make(chan struct{})
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan struct{})
	go func() {
		defer close(done)
		decoder := json.NewDecoder(output)
		for {
			var message struct {
				ID     string         `json:"id"`
				Result map[string]any `json:"result"`
			}
			if decoder.Decode(&message) != nil {
				return
			}
			a.mu.RLock()
			pending, exists := a.pending[message.ID]
			a.mu.RUnlock()
			if exists {
				pending.ch <- &rpcMessage{Result: message.Result}
			}
		}
	}()
	session := &SessionState{SessionID: "exact", Handle: "native-handle", Status: "idle", Metadata: map[string]any{}}
	a.sessions["exact"] = session
	_, sendErr := a.handleSend(ctx, session, core.Request{Fields: map[string]any{"text": "native image fixture", "attachments": []any{map[string]any{"name": "截图.png", "media_type": "image/png", "content_base64": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/ZfoAAAAASUVORK5CYII="}}}})
	input.Close()
	<-done
	waitErr := cmd.Wait()
	if sendErr != nil || waitErr != nil {
		t.Fatalf("native image admission: send=%v exit=%v stderr=%s", sendErr, waitErr, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(`"ok": true`)) {
		t.Fatalf("missing native media evidence: %s", stderr.String())
	}
	t.Log("Go send -> native staging -> durable media admission -> exact PNG MessageEvent verified")
}
