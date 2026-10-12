package audio

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	MaxStreamBytes = 16000 * 2 * 60 * 10 // 10 minutes of 16kHz 16-bit mono = 19,200,000 bytes
)

type wsControlMessage struct {
	Type string `json:"type"`
}

func (s *Service) handleLocalStream(w http.ResponseWriter, r *http.Request) {
	if s.authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Origin is validated by AuthorizeBrowser
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// Check if local plugin is running and verified healthy
	status := s.plugin.Status()
	if status["state"] != "running" || !s.plugin.IsHealthy(r.Context()) {
		_ = writeWSJSON(conn, map[string]any{
			"type":    "error",
			"message": "R2T2 本地流式听写插件未运行。",
		})
		_ = conn.Close(1013, "Try Again Later")
		return
	}

	// Send ready
	if err := writeWSJSON(conn, map[string]any{
		"type":        "ready",
		"sample_rate": 16000,
	}); err != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	_, unregister := s.registerStream(cancel)
	defer unregister()

	var pcmMu sync.Mutex
	pcm := make([]byte, 0, 32000*10)
	var transcript string
	stopping := false
	changed := make(chan struct{}, 1)
	signalChanged := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}

	inferDone := make(chan struct{})

	// Independent inference worker
	go func() {
		defer close(inferDone)
		lastInferenceSize := 0

		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}

			pcmMu.Lock()
			currentPCM := append([]byte(nil), pcm...)
			isStopping := stopping
			pcmMu.Unlock()

			size := len(currentPCM)
			if size > lastInferenceSize && (isStopping || size-lastInferenceSize >= 32000) {
				t, err := s.plugin.TranscribePCM(ctx, currentPCM)
				if err != nil {
					if ctx.Err() == nil {
						_ = writeWSJSON(conn, map[string]any{
							"type":    "error",
							"message": "本地流式听写失败，请重试。",
						})
						_ = conn.Close(1011, "Internal Error")
					}
					return
				}
				lastInferenceSize = size
				transcript = t
				_ = writeWSJSON(conn, map[string]any{
					"type": "transcript",
					"text": transcript,
				})
			}

			if isStopping && len(currentPCM) == lastInferenceSize {
				_ = writeWSJSON(conn, map[string]any{
					"type": "done",
					"text": transcript,
				})
				_ = conn.Close(websocket.StatusNormalClosure, "Done")
				return
			}
		}
	}()

	defer func() {
		cancel()
		<-inferDone
	}()

	// Read loop from WebSocket
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			cancel()
			return
		}

		if msgType == websocket.MessageBinary {
			pcmMu.Lock()
			if len(pcm)+len(data) > MaxStreamBytes {
				pcmMu.Unlock()
				_ = writeWSJSON(conn, map[string]any{
					"type":    "error",
					"message": "单次流式听写不能超过 10 分钟。",
				})
				_ = conn.Close(1009, "Message Too Big")
				cancel()
				return
			}
			pcm = append(pcm, data...)
			pcmMu.Unlock()
			signalChanged()
			continue
		}

		if msgType == websocket.MessageText {
			var ctrl wsControlMessage
			if err := json.Unmarshal(data, &ctrl); err != nil {
				continue
			}
			if ctrl.Type == "cancel" {
				_ = writeWSJSON(conn, map[string]any{
					"type": "cancelled",
				})
				_ = conn.Close(websocket.StatusNormalClosure, "Cancelled")
				cancel()
				return
			}
			if ctrl.Type == "stop" {
				pcmMu.Lock()
				stopping = true
				pcmMu.Unlock()
				signalChanged()
				// Wait for infer worker to finish or socket to close
				select {
				case <-inferDone:
				case <-ctx.Done():
				}
				return
			}
		}
	}
}

func writeWSJSON(conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}
