package settingsapi

import (
	"astrorder.dev/app-server/internal/kernelcontrol"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	if h.Kernels != nil {
		if r.Method == http.MethodGet {
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/services/daemon/restart/")
			r.URL.Path = kernelsPrefix + "operations/" + id
			h.kernels(w, r)
			return
		}
		// Preserve the old explicit confirmation contract while dispatching independently.
		if h.Authorizer == nil {
			fail(w, 503, "内核管理认证不可用")
			return
		}
		if r.URL.Query().Has("token") {
			fail(w, 401, "请先登录星序")
			return
		}
		if r.Header.Get("Origin") == "" {
			fail(w, 403, "必须提供请求来源")
			return
		}
		if failure := h.Authorizer.AuthorizeBrowser(r); failure != nil {
			fail(w, failure.Status, failure.Detail)
			return
		}
		var body struct {
			ConfirmActive bool `json:"confirm_active"`
		}
		if decode(r, &body) != nil || !body.ConfirmActive {
			fail(w, 422, "必须明确确认中断小内核中的活跃会话")
			return
		}
		op, err := h.Kernels.Start("daemon", "restart", true)
		if err != nil {
			code := 503
			if errors.Is(err, kernelcontrol.ErrBusy) {
				code = 409
			}
			fail(w, code, err.Error())
			return
		}
		writeJSON(w, 202, op)
		return
	}
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
