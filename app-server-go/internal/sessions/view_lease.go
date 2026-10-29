package sessions

import (
	"net/http"
	"sync"
	"time"
)

// Viewing is separate from native ownership. A disappearing client expires
// naturally; one tab cannot release another tab's protection.
type viewLeases struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (v *viewLeases) touch(key string, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.until == nil {
		v.until = make(map[string]time.Time)
	}
	for k, t := range v.until {
		if !t.After(now) {
			delete(v.until, k)
		}
	}
	v.until[key] = now.Add(90 * time.Second)
}
func (v *viewLeases) active(key string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.until[key].After(now)
}
func (h *Handler) handleViewLease(w http.ResponseWriter, r *http.Request, sid string) {
	aid := r.URL.Query().Get("agent_id")
	if aid == "" {
		writeJSONError(w, 422, "缺少 Agent 标识")
		return
	}
	row, err := h.store.GetSession(r.Context(), aid, sid)
	if err != nil {
		writeJSONError(w, 500, "无法读取会话")
		return
	}
	if row == nil {
		writeJSONError(w, 404, "会话不存在")
		return
	}
	h.views.touch(aid+"::"+sid, time.Now())
	writeJSON(w, 200, map[string]any{"ok": true, "expires_in_seconds": 90})
}
