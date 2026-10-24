package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestDraftShelfDurableImagesAndConflict(t *testing.T) {
	database := filepath.Join(t.TempDir(), "app.db")
	st, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	handler := NewHandler(Options{Config: config.Config{BrowserSecret: "fixture"}, Store: st})
	call := func(method, scope, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/draft-shelf?scope="+scope, bytes.NewBufferString(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	if res := call("GET", "agent::one", "", ""); res.Code != 401 {
		t.Fatalf("unauthenticated=%d", res.Code)
	}
	if res := call("GET", "agent::one", "", "fixture"); res.Code != 200 || !bytes.Contains(res.Body.Bytes(), []byte(`"revision":0`)) {
		t.Fatalf("empty=%d %s", res.Code, res.Body.String())
	}
	payload := `{"revision":0,"entries":[{"id":"saved","savedAt":"2026-10-19T00:00:00Z","draft":{"text":"文字截图","sessionRefs":[],"attachments":[{"key":"image","file":{"name":"shot.png","type":"image/png","lastModified":123,"data":"AAECA/8="}}]}}]}`
	if res := call("PUT", "agent::one", payload, "fixture"); res.Code != 200 {
		t.Fatalf("save=%d %s", res.Code, res.Body.String())
	}
	if res := call("PUT", "agent::one", `{"revision":0,"entries":[]}`, "fixture"); res.Code != 409 {
		t.Fatalf("stale write=%d", res.Code)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler = NewHandler(Options{Config: config.Config{BrowserSecret: "fixture"}, Store: st})
	res := call("GET", "agent::one", "", "fixture")
	var got struct {
		Revision int               `json:"revision"`
		Entries  []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || len(got.Entries) != 1 || !bytes.Contains(got.Entries[0], []byte(`"data":"AAECA/8="`)) {
		t.Fatalf("reopen lost draft: %s", res.Body.String())
	}
	if res = call("GET", "other::one", "", "fixture"); !bytes.Contains(res.Body.Bytes(), []byte(`"entries":[]`)) {
		t.Fatalf("scope leaked=%s", res.Body.String())
	}
	if res = call("PUT", "agent::one", `{"revision":1,"entries":[{"id":"bad","draft":{"attachments":[{"file":{"data":"invalid"}}]}}]}`, "fixture"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid image=%d", res.Code)
	}
	if res = call("PUT", "agent::one", `{"revision":1,"entries":[]}`, "fixture"); res.Code != 200 {
		t.Fatalf("delete=%d", res.Code)
	}
	if res = call("GET", "agent::one", "", "fixture"); !bytes.Contains(res.Body.Bytes(), []byte(`"entries":[]`)) {
		t.Fatalf("delete readback=%s", res.Body.String())
	}
}
