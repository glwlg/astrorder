package audio

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Opt-in hardware integration: use existing model assets and a known mono
// PCM16/16kHz WAV. This does not read or change production preferences.
func TestLocalR2T2RealPacedStream(t *testing.T) {
	resourceDir := os.Getenv("ASTRORDER_TEST_R2T2_DIR")
	samplePath := os.Getenv("ASTRORDER_TEST_R2T2_WAV")
	if resourceDir == "" || samplePath == "" {
		t.Skip("set ASTRORDER_TEST_R2T2_DIR and ASTRORDER_TEST_R2T2_WAV for real GPU inference")
	}
	data, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatal("sample must be WAV")
	}
	var pcm []byte
	validFormat := false
	for offset := 12; offset+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		if size > len(data)-start {
			t.Fatal("invalid WAV chunk")
		}
		if string(data[offset:offset+4]) == "fmt " && size >= 16 {
			f := data[start : start+size]
			validFormat = binary.LittleEndian.Uint16(f[:2]) == 1 && binary.LittleEndian.Uint16(f[2:4]) == 1 && binary.LittleEndian.Uint32(f[4:8]) == 16000 && binary.LittleEndian.Uint16(f[14:16]) == 16
		}
		if string(data[offset:offset+4]) == "data" {
			pcm = data[start : start+size]
		}
		offset = start + size + size%2
	}
	if !validFormat || len(pcm) == 0 {
		t.Fatal("sample must contain mono PCM16 at 16kHz")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	svc, err := NewService(Options{Context: ctx, ResourceDir: resourceDir, Authorizer: testAuthenticator()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := svc.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	if err := svc.plugin.Enable(true, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("real model running: %v", svc.plugin.Status())
	warmup, err := svc.plugin.TranscribePCM(ctx, pcm)
	if err != nil || warmup == "" {
		t.Fatalf("real inference: %q, %v", warmup, err)
	}
	t.Logf("full sample: %s", warmup)
	server := httptest.NewServer(svc)
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/audio/local-stream", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer test-secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_, ready, err := conn.Read(ctx)
	if err != nil || !strings.Contains(string(ready), `"ready"`) {
		t.Fatalf("ready: %s, %v", ready, err)
	}
	started := time.Now()
	audioEnded := make(chan time.Duration, 1)
	sendError := make(chan error, 1)
	go func() {
		const block = 3200 // 100ms PCM
		for offset := 0; offset < len(pcm); offset += block {
			end := offset + block
			if end > len(pcm) {
				end = len(pcm)
			}
			if err := conn.Write(ctx, websocket.MessageBinary, pcm[offset:end]); err != nil {
				sendError <- err
				return
			}
			timer := time.NewTimer(time.Duration(end-offset) * time.Second / 32000)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				sendError <- ctx.Err()
				return
			}
		}
		audioEnded <- time.Since(started)
		sendError <- conn.Write(ctx, websocket.MessageText, []byte(`{"type":"stop"}`))
	}()
	firstText := time.Duration(0)
	finalText := ""
	for {
		_, message, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(message, &event); err != nil {
			t.Fatal(err)
		}
		t.Logf("%.3fs %s: %s", time.Since(started).Seconds(), event.Type, event.Text)
		if event.Type == "error" {
			t.Fatalf("stream error: %s", message)
		}
		if event.Type == "transcript" && event.Text != "" && firstText == 0 {
			firstText = time.Since(started)
		}
		if event.Type == "done" {
			finalText = event.Text
			break
		}
	}
	if err := <-sendError; err != nil {
		t.Fatal(err)
	}
	ended := <-audioEnded
	if firstText == 0 || firstText >= ended {
		t.Errorf("no text before audio ended: first=%s ended=%s", firstText, ended)
	}
	if finalText != warmup {
		t.Errorf("final differs from full inference: got %q want %q", finalText, warmup)
	}
	t.Logf("audio ended=%.3fs first text=%.3fs completion=%.3fs", ended.Seconds(), firstText.Seconds(), time.Since(started).Seconds())
}
