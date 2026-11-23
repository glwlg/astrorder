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

func TestDraftShelfMetaEndpoint(t *testing.T) {
	database := filepath.Join(t.TempDir(), "app.db")
	st, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	handler := NewHandler(Options{Config: config.Config{BrowserSecret: "fixture"}, Store: st})
	callURL := func(method, url, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer fixture")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	res := callURL("GET", "/api/v1/draft-shelf?scope=agent::meta&meta=1", "")
	if res.Code != http.StatusOK {
		t.Fatalf("empty meta=%d %s", res.Code, res.Body.String())
	}
	var emptyMeta struct {
		Revision int `json:"revision"`
		Count    int `json:"count"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &emptyMeta); err != nil {
		t.Fatal(err)
	}
	if emptyMeta.Revision != 0 || emptyMeta.Count != 0 {
		t.Fatalf("unexpected empty meta: %+v", emptyMeta)
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"entries"`)) {
		t.Fatalf("meta response must omit entries: %s", res.Body.String())
	}

	payload := `{"revision":0,"entries":[{"id":"saved","savedAt":"2026-10-19T00:00:00Z","draft":{"text":"截图草稿","sessionRefs":[],"attachments":[{"key":"image","file":{"name":"shot.png","type":"image/png","lastModified":123,"data":"AAECA/8="}}]}}]}`
	if putRes := callURL("PUT", "/api/v1/draft-shelf?scope=agent::meta", payload); putRes.Code != http.StatusOK {
		t.Fatalf("save=%d %s", putRes.Code, putRes.Body.String())
	}

	res = callURL("GET", "/api/v1/draft-shelf?scope=agent::meta&meta=1", "")
	if res.Code != http.StatusOK {
		t.Fatalf("populated meta=%d %s", res.Code, res.Body.String())
	}
	var populatedMeta struct {
		Revision int `json:"revision"`
		Count    int `json:"count"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &populatedMeta); err != nil {
		t.Fatal(err)
	}
	if populatedMeta.Revision != 1 || populatedMeta.Count != 1 {
		t.Fatalf("unexpected populated meta: %+v", populatedMeta)
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`AAECA/8=`)) || bytes.Contains(res.Body.Bytes(), []byte(`"entries"`)) {
		t.Fatalf("meta response leaked attachment payload: %s", res.Body.String())
	}
}
