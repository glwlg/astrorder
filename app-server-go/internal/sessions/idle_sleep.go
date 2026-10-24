package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"log/slog"
	"sync"
	"time"
)

// RunIdleSleep is deliberately limited to Codex, whose transport has a native
// work check. Other runtimes remain loaded until equivalent checks exist.
func (h *Handler) RunIdleSleep(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	since := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c, cancel := context.WithTimeout(ctx, 25*time.Second)
			if err := h.reclaimIdle(c, now, since); err != nil {
				slog.Debug("idle reclamation deferred", "error", err)
			}
			cancel()
		}
	}
}
func (h *Handler) reclaimIdle(ctx context.Context, now time.Time, since map[string]time.Time) error {
	caps, err := h.client.Daemon.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	if err != nil {
		return err
	}
	if caps.GetProtocolMinor() < 2 {
		return nil
	}
	live, err := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil {
		clear(since)
		return err
	}
	if live == nil || live.DaemonId != h.client.DaemonID() || live.StorageError != nil {
		clear(since)
		return nil
	}
	rows, err := h.store.ListSessions(ctx, "")
	if err != nil {
		return err
	}
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	commands, err := h.store.ActiveCommands(ctx)
	if err != nil {
		return err
	}
	tasks, err := h.store.ActiveTasks(ctx)
	if err != nil {
		return err
	}
	blocked := map[string]bool{}
	for _, list := range [][]map[string]any{commands, tasks} {
		for _, row := range list {
			blocked[stringValue(row, "agent_id")+"::"+stringValue(row, "session_id")] = true
		}
	}
	byID := map[string]map[string]any{}
	for _, a := range agents {
		byID[stringValue(a, "id")] = a
	}
	seen := map[string]bool{}
	for _, s := range live.Sessions {
		if s == nil || s.Session == nil {
			continue
		}
		aid := s.Session.GetAgentId()
		a := byID[aid]
		if stringValue(a, "kind") != "codex" {
			continue
		}
		row := matchLiveOwnedSession(rows, a, s)
		if row == nil {
			continue
		}
		sid := stringValue(row, "id")
		key := aid + "::" + sid
		seen[key] = true
		if s.Status != pb.SessionStatus_SESSION_STATUS_IDLE || blocked[key] || h.views.active(key, now) {
			delete(since, key)
			continue
		}
		start, ok := since[key]
		if !ok {
			since[key] = now
			continue
		}
		if now.Sub(start) < 10*time.Minute {
			continue
		}
		value, _ := nativeAttachLocks.LoadOrStore("handoff-send\x00"+aid+"\x00"+sid, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		if !lock.TryLock() {
			delete(since, key)
			continue
		}
		// Hold the viewer lock through close: a newly opened tab renews after close
		// and its binding path resumes the exact native identity.
		h.views.mu.Lock()
		if h.views.until[key].After(now) {
			h.views.mu.Unlock()
			lock.Unlock()
			delete(since, key)
			continue
		}
		if err := h.store.SetPreference(ctx, sleepPreferenceKey(aid, sid), true, true); err != nil {
			h.views.mu.Unlock()
			lock.Unlock()
			return err
		}
		receipt, closeErr := h.client.Session.CloseSession(ctx, &pb.CloseSessionRequest{Meta: newCommandMeta("", ""), Session: s.Session})
		if closeErr != nil || confirmedReceipt(receipt) != nil {
			_, _ = h.store.DeletePreference(ctx, sleepPreferenceKey(aid, sid))
		}
		h.views.mu.Unlock()
		lock.Unlock()
		delete(since, key)
		if closeErr == nil && confirmedReceipt(receipt) == nil {
			slog.Info("idle session suspended", "agent_id", aid, "session_id", sid)
		}
	}
	for key := range since {
		if !seen[key] {
			delete(since, key)
		}
	}
	return nil
}
