package httpapi

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type draftShelf struct {
	Revision int64             `json:"revision"`
	Entries  []json.RawMessage `json:"entries"`
}

func validDraftEntries(entries []json.RawMessage) bool {
	seen := map[string]bool{}
	for _, raw := range entries {
		var row struct {
			ID      string `json:"id"`
			SavedAt string `json:"savedAt"`
			Draft   *struct {
				Text        string `json:"text"`
				Attachments []struct {
					File *struct {
						Name string `json:"name"`
						Data string `json:"data"`
					} `json:"file"`
				} `json:"attachments"`
			} `json:"draft"`
		}
		if json.Unmarshal(raw, &row) != nil || row.ID == "" || seen[row.ID] || row.Draft == nil {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, row.SavedAt); err != nil {
			return false
		}
		seen[row.ID] = true
		for _, image := range row.Draft.Attachments {
			if image.File == nil || image.File.Name == "" {
				return false
			}
			if _, err := base64.StdEncoding.Strict().DecodeString(image.File.Data); err != nil {
				return false
			}
		}
	}
	return entries != nil
}

func serveDraftShelf(a *api, w http.ResponseWriter, r *http.Request) {
	if failure := a.auth.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if a.draftStore == nil {
		writeJSONError(w, 503, "Draft store is unavailable")
		return
	}
	scope := r.URL.Query().Get("scope")
	if strings.TrimSpace(scope) == "" || len(scope) > 2048 {
		writeJSONError(w, 422, "Invalid draft scope")
		return
	}
	result := draftShelf{Entries: []json.RawMessage{}}
	if r.Method == http.MethodGet {
		var raw string
		err := a.draftStore.DB().QueryRowContext(r.Context(), "SELECT revision,entries FROM draft_shelves WHERE scope=?", scope).Scan(&result.Revision, &raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, 500, "Unable to read drafts")
			return
		}
		if err == nil && json.Unmarshal([]byte(raw), &result.Entries) != nil {
			writeJSONError(w, 500, "Unable to decode drafts")
			return
		}
	} else {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF || result.Revision < 0 || !validDraftEntries(result.Entries) {
			writeJSONError(w, 422, "Invalid draft data")
			return
		}
		raw, _ := json.Marshal(result.Entries)
		conflict := errors.New("draft revision conflict")
		err := a.draftStore.WithTx(r.Context(), func(tx *sql.Tx) error {
			var revision int64
			err := tx.QueryRowContext(r.Context(), "SELECT revision FROM draft_shelves WHERE scope=?", scope).Scan(&revision)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if revision != result.Revision {
				return conflict
			}
			result.Revision++
			_, err = tx.ExecContext(r.Context(), "INSERT INTO draft_shelves(scope,revision,entries) VALUES(?,?,?) ON CONFLICT(scope) DO UPDATE SET revision=excluded.revision,entries=excluded.entries", scope, result.Revision, string(raw))
			return err
		})
		if errors.Is(err, conflict) {
			writeJSONError(w, 409, "草稿箱已在其他窗口更新，请重新打开后重试，输入内容已保留。")
			return
		}
		if err != nil {
			writeJSONError(w, 500, "Unable to save drafts")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
