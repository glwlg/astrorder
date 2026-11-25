package office

import (
	"encoding/json"
	"net/http"

	"astrorder.dev/app-server/internal/officepersona"
)

func (h *Handler) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	profile := officepersona.Load(r.Context(), h.options.Store)
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	var req officepersona.OfficePersona
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的办公画像配置 JSON")
		return
	}
	saved, err := officepersona.Save(r.Context(), h.options.Store, req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "保存办公画像失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}
