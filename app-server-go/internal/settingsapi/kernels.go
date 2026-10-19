package settingsapi

import (
	"astrorder.dev/app-server/internal/kernelcontrol"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const kernelsPrefix = "/api/v1/services/kernels/"

func matchesKernels(method, path string) bool {
	switch path {
	case kernelsPrefix + "status":
		return method == http.MethodGet
	case kernelsPrefix + "operations":
		return method == http.MethodPost
	}
	id := strings.TrimPrefix(path, kernelsPrefix+"operations/")
	return method == http.MethodGet && id != path && id != "" && !strings.Contains(id, "/")
}
func (h *handler) kernels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !matchesKernels(r.Method, r.URL.Path) {
		fail(w, 404, "内核管理接口不存在")
		return
	}
	if h.Authorizer == nil {
		fail(w, 503, "内核管理认证不可用")
		return
	}
	if r.URL.Query().Has("token") {
		fail(w, 401, "请先登录星序")
		return
	}
	if r.Method == http.MethodPost && r.Header.Get("Origin") == "" {
		fail(w, 403, "必须提供请求来源")
		return
	}
	if e := h.Authorizer.AuthorizeBrowser(r); e != nil {
		fail(w, e.Status, e.Detail)
		return
	}
	if h.Kernels == nil {
		fail(w, 503, kernelcontrol.ErrUnavailable.Error())
		return
	}
	if r.URL.Path == kernelsPrefix+"status" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		status, err := h.Kernels.Status(ctx)
		if err != nil {
			fail(w, 503, err.Error())
			return
		}
		writeJSON(w, 200, status)
		return
	}
	if r.Method == http.MethodGet {
		op, err := h.Kernels.Get(strings.TrimPrefix(r.URL.Path, kernelsPrefix+"operations/"))
		if errors.Is(err, os.ErrNotExist) {
			fail(w, 404, "内核操作不存在")
			return
		}
		if err != nil {
			fail(w, 500, "无法读取内核操作")
			return
		}
		writeJSON(w, 200, op)
		return
	}
	var payload *struct {
		Target        string `json:"target"`
		Action        string `json:"action"`
		ConfirmActive bool   `json:"confirm_active"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		fail(w, 422, "内核操作请求无效")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !payload.ConfirmActive || !((payload.Target == "app" || payload.Target == "daemon") && payload.Action == "restart" || payload.Target == "daemon" && payload.Action == "update") {
		fail(w, 422, "必须明确确认有效的内核操作")
		return
	}
	op, err := h.Kernels.Start(payload.Target, payload.Action, payload.ConfirmActive)
	if err != nil {
		code := 503
		if errors.Is(err, kernelcontrol.ErrBusy) {
			code = 409
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 202, op)
}
