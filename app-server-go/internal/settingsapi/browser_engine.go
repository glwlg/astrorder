package settingsapi

import (
	"net/http"

	"astrorder.dev/app-server/internal/browser"
)

func (h *handler) browserEngine(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Engine string `json:"engine"`
		}
		if decode(r, &body) != nil || (body.Engine != "browser_use" && body.Engine != "jev_ultrafast") {
			fail(w, http.StatusUnprocessableEntity, "请选择浏览器引擎")
			return
		}
		if err := h.Store.SetPreference(r.Context(), browser.BrowserEnginePreference, body.Engine, true); err != nil {
			fail(w, http.StatusInternalServerError, "保存浏览器引擎失败")
			return
		}
	}
	engine, err := browser.GetBrowserEngine(r.Context(), h.Store)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取浏览器引擎失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"engine": engine})
}
