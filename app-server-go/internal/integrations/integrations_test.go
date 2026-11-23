package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func newTestHandler(t *testing.T, client *http.Client) http.Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewHandler(Options{Store: st, HTTPClient: client})
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIntegrationsCRUDAndReorder(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/logo.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7"/></svg>`))
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Magpie Gateway</title><link rel="icon" type="image/svg+xml" href="/logo.svg"></head><body></body></html>`))
		}
	}))
	defer site.Close()

	h := newTestHandler(t, site.Client())

	// Fetch metadata discovers title and SVG favicon as data URL
	metaResp := call(h, http.MethodPost, "/api/v1/integrations/fetch-meta", `{"url":"`+site.URL+`"}`)
	if metaResp.Code != http.StatusOK {
		t.Fatalf("fetch-meta failed: %d %s", metaResp.Code, metaResp.Body.String())
	}
	var meta SiteMeta
	if err := json.Unmarshal(metaResp.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Magpie Gateway" || !strings.HasPrefix(meta.IconDataURL, "data:image/svg+xml;base64,") {
		t.Fatalf("unexpected site meta: %+v", meta)
	}

	// Create first integration with auto-discovery
	c1 := call(h, http.MethodPost, "/api/v1/integrations", `{"url":"`+site.URL+`"}`)
	if c1.Code != http.StatusCreated {
		t.Fatalf("create 1 failed: %d %s", c1.Code, c1.Body.String())
	}
	var item1 Integration
	_ = json.Unmarshal(c1.Body.Bytes(), &item1)
	if item1.ID == "" || item1.Name != "Magpie Gateway" || !item1.Visible || item1.OpenMode != "embed" || !strings.HasPrefix(item1.IconValue, "data:image/svg+xml;base64,") {
		t.Fatalf("unexpected created item1: %+v", item1)
	}

	// Create second integration with custom builtin icon
	c2 := call(h, http.MethodPost, "/api/v1/integrations", `{"name":"Grafana","url":"https://grafana.example.com","icon_type":"builtin","icon_value":"chart","open_mode":"external","visible":true}`)
	if c2.Code != http.StatusCreated {
		t.Fatalf("create 2 failed: %d %s", c2.Code, c2.Body.String())
	}
	var item2 Integration
	_ = json.Unmarshal(c2.Body.Bytes(), &item2)

	// Reorder [item2, item1]
	ro := call(h, http.MethodPost, "/api/v1/integrations/reorder", `{"ids":["`+item2.ID+`","`+item1.ID+`"]}`)
	if ro.Code != http.StatusOK {
		t.Fatalf("reorder failed: %d %s", ro.Code, ro.Body.String())
	}
	var listResp struct {
		Items []Integration `json:"items"`
	}
	_ = json.Unmarshal(ro.Body.Bytes(), &listResp)
	if len(listResp.Items) != 2 || listResp.Items[0].ID != item2.ID || listResp.Items[1].ID != item1.ID {
		t.Fatalf("unexpected order: %+v", listResp.Items)
	}

	// Update visibility to false, empty name (icon-only), and custom emoji icon
	up := call(h, http.MethodPut, "/api/v1/integrations/"+item1.ID, `{"name":"","visible":false,"icon_type":"emoji","icon_value":"🚀"}`)
	if up.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", up.Code, up.Body.String())
	}
	var updated Integration
	_ = json.Unmarshal(up.Body.Bytes(), &updated)
	if updated.Name != "" || updated.Visible || updated.IconType != "emoji" || updated.IconValue != "🚀" {
		t.Fatalf("unexpected updated item: %+v", updated)
	}

	// Explicitly creating with empty name preserves icon-only empty name
	cEmpty := call(h, http.MethodPost, "/api/v1/integrations", `{"name":"","url":"`+site.URL+`"}`)
	if cEmpty.Code != http.StatusCreated {
		t.Fatalf("create empty name failed: %d %s", cEmpty.Code, cEmpty.Body.String())
	}
	var emptyItem Integration
	_ = json.Unmarshal(cEmpty.Body.Bytes(), &emptyItem)
	if emptyItem.Name != "" {
		t.Fatalf("expected empty name to stay empty, got %q", emptyItem.Name)
	}
	_ = call(h, http.MethodDelete, "/api/v1/integrations/"+emptyItem.ID, "")

	// Reject unsafe URLs
	for _, bad := range []string{
		`{"name":"Bad","url":"javascript:alert(1)"}`,
		`{"name":"Creds","url":"https://user:pass@example.com"}`,
	} {
		if w := call(h, http.MethodPost, "/api/v1/integrations", bad); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for %s, got %d", bad, w.Code)
		}
	}

	// Delete item2
	del := call(h, http.MethodDelete, "/api/v1/integrations/"+item2.ID, "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", del.Code, del.Body.String())
	}
	after := call(h, http.MethodGet, "/api/v1/integrations", "")
	_ = json.Unmarshal(after.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 || listResp.Items[0].ID != item1.ID {
		t.Fatalf("unexpected items after delete: %+v", listResp.Items)
	}
}

func TestFetchSiteMetaSupportsInlineSVGAndFaviconFallback(t *testing.T) {
	inlineSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><title>Inline App</title><link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'/>"></head>`))
	}))
	defer inlineSite.Close()

	meta, err := FetchSiteMeta(t.Context(), inlineSite.Client(), inlineSite.URL)
	if err != nil || meta.Title != "Inline App" || !strings.HasPrefix(meta.IconDataURL, "data:image/svg+xml;base64,") {
		t.Fatalf("inline SVG icon failed: meta=%+v err=%v", meta, err)
	}

	icoBytes := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}
	fallbackSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(icoBytes)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><title>Plain Site</title></head>`))
	}))
	defer fallbackSite.Close()

	meta2, err2 := FetchSiteMeta(t.Context(), fallbackSite.Client(), fallbackSite.URL)
	if err2 != nil || meta2.Title != "Plain Site" || !strings.HasPrefix(meta2.IconDataURL, "data:image/x-icon;base64,") {
		t.Fatalf("favicon.ico fallback failed: meta=%+v err=%v", meta2, err2)
	}
}

func TestFetchSiteMetaCarriesSessionCookieAcross303Redirect(t *testing.T) {
	// Mirrors Magpie webGuard: GET /?k=secret -> 303 Set-Cookie Location: /
	guarded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/icons/favicon.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<!-- magpie --><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M0 0h16v16H0z"/></svg>`))
		case "/":
			if r.URL.Query().Get("k") == "secret-key" {
				http.SetCookie(w, &http.Cookie{Name: "magpie_web", Value: "secret-key", Path: "/", HttpOnly: true})
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			cookie, err := r.Cookie("magpie_web")
			if err != nil || cookie.Value != "secret-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>magpie</title><link rel="icon" href="icons/favicon.svg" type="image/svg+xml"></head><body></body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer guarded.Close()

	meta, err := FetchSiteMeta(t.Context(), guarded.Client(), guarded.URL+"/?k=secret-key")
	if err != nil || meta.Title != "magpie" || !strings.HasPrefix(meta.IconDataURL, "data:image/svg+xml;base64,") {
		t.Fatalf("cookie redirect fetch failed: meta=%+v err=%v", meta, err)
	}
}
