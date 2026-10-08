package protocol

import (
	"astrorder.dev/session-daemon/internal/events"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"strings"
	"time"
)

func frameEnvelope(f events.Frame) map[string]any {
	return map[string]any{"session_id": f.SessionID, "seq_id": f.SeqID, "timestamp": f.Timestamp, "event": f.Event, "payload": f.Payload}
}

// One writer owns response/live ordering; the reader never holds a journal lock.
func (d *Daemon) serveStream(parent context.Context, socket *websocket.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	incoming := make(chan []byte, 1)
	completed := make(chan map[string]any, 32)
	pendingControls := 0
	go func() {
		defer cancel()
		for {
			_, raw, err := socket.Read(ctx)
			if err != nil {
				return
			}
			select {
			case incoming <- raw:
			case <-ctx.Done():
				return
			}
		}
	}()
	authenticated := d.secret == ""
	var subscription *events.Subscription
	defer func() {
		if subscription != nil {
			d.journal.Unsubscribe(subscription)
		}
	}()
	active := false
	watermarks := map[string]int64{}
	pendingPages := map[string]bool{}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	write := func(value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		bounded, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		return socket.Write(bounded, websocket.MessageText, raw)
	}
	for {
		var frames <-chan events.Frame
		if active && subscription != nil {
			frames = subscription.Frames()
		}
		select {
		case response := <-completed:
			pendingControls--
			if err := write(response); err != nil {
				return
			}
		case <-ctx.Done():
			return
		case <-ticker.C:
			if subscription != nil && !subscription.Healthy() {
				socket.CloseNow()
				return
			}
		case raw := <-incoming:
			var request map[string]any
			_ = json.Unmarshal(raw, &request)
			action, _ := request["action"].(string)
			if authenticated && strings.HasPrefix(action, "session.") && action != "session.sync" && action != "session.event" {
				if pendingControls >= 32 {
					if err := write(errorResponse(request["request_id"], "too many pending runtime controls")); err != nil {
						return
					}
					continue
				}
				pendingControls++
				go func(request map[string]any) {
					response := d.control(ctx, request)
					select {
					case completed <- response:
					case <-ctx.Done():
					}
				}(request)
				continue
			}
			syncing := authenticated && request["action"] == "session.sync"
			preparing := authenticated && request["action"] == "daemon.status" && request["prepare_replay"] == true
			if preparing {
				active = false
				if subscription == nil {
					subscription = d.journal.SubscribeWithCapacity(256)
				}
			}
			if syncing {
				active = false
				if subscription == nil {
					subscription = d.journal.SubscribeWithCapacity(256)
				}
			}
			response, ok := d.handleContext(ctx, raw, &authenticated)
			if syncing && request["defer_live"] == false && len(pendingPages) > 0 {
				checkpoints, _ := request["sessions"].(map[string]any)
				if len(checkpoints) == 0 {
					response = errorResponse(request["request_id"], "replay pages remain unfinished")
				}
			}
			if err := write(response); err != nil || !ok && !authenticated {
				return
			}
			if response["action"] == "daemon.shutdown.result" {
				d.shutdownOnce.Do(func() { close(d.shutdownRequested) })
				return
			}
			if syncing && response["action"] == "session.sync.result" {
				sessions, _ := response["sessions"].(map[string]any)
				for id, value := range sessions {
					page, _ := value.(map[string]any)
					boundary, _ := page["max_seq_id"].(int64)
					watermarks[id] = boundary
					if page["has_more"] == true {
						pendingPages[id] = true
					} else {
						delete(pendingPages, id)
					}
				}
				active = len(pendingPages) == 0 && request["defer_live"] != true
			}
		case f := <-frames:
			if !subscription.Healthy() {
				socket.CloseNow()
				return
			}
			if f.SeqID <= watermarks[f.SessionID] {
				continue
			}
			if err := write(frameEnvelope(f)); err != nil {
				return
			}
			watermarks[f.SessionID] = f.SeqID
		}
	}
}
