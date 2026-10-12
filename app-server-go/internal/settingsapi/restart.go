package settingsapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

type restartOperations struct {
	sync.Mutex
	items  map[string]map[string]any
	active bool
}

func (h *handler) restart(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/services/daemon/restart"
	if r.Method == http.MethodGet {
		id := strings.TrimPrefix(r.URL.Path, prefix+"/")
		h.operations.Lock()
		defer h.operations.Unlock()
		item := h.operations.items[id]
		if item == nil {
			fail(w, 404, "小内核重启任务不存在")
			return
		}
		writeJSON(w, 200, item)
		return
	}
	var payload struct {
		ConfirmActive bool `json:"confirm_active"`
	}
	if decode(r, &payload) != nil || !payload.ConfirmActive {
		fail(w, 422, "必须明确确认中断小内核中的活跃会话")
		return
	}
	if h.RestartDaemon == nil {
		fail(w, 503, "小内核生命周期配置不可用")
		return
	}
	h.operations.Lock()
	if h.operations.active {
		h.operations.Unlock()
		fail(w, 409, "小内核重启任务已在运行")
		return
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		h.operations.Unlock()
		fail(w, 500, "无法创建重启任务")
		return
	}
	id := "daemon-restart-" + hex.EncodeToString(random[:])
	if h.operations.items == nil {
		h.operations.items = map[string]map[string]any{}
	}
	// Completed operation history is bounded for this App process.
	if len(h.operations.items) >= 100 {
		for key := range h.operations.items {
			delete(h.operations.items, key)
			break
		}
	}
	h.operations.items[id] = map[string]any{"id": id, "status": "queued"}
	h.operations.active = true
	h.operations.Unlock()
	go func() {
		h.operations.Lock()
		h.operations.items[id]["status"] = "running"
		h.operations.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := h.RestartDaemon(ctx, true)
		h.operations.Lock()
		defer h.operations.Unlock()
		h.operations.active = false
		if err != nil {
			h.operations.items[id]["status"] = "failed"
			h.operations.items[id]["error"] = err.Error()
			h.operations.items[id]["detail"] = err.Error()
		} else {
			h.operations.items[id]["status"] = "success"
		}
	}()
	writeJSON(w, 202, map[string]any{"id": id, "status": "queued"})
}
