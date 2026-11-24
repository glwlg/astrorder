package dbxplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func newTestAuthenticator() *auth.Authenticator {
	return auth.New(config.Config{BrowserSecret: "top-secret-token"}, nil)
}

func TestMatchesRoutes(t *testing.T) {
	if !Matches(http.MethodGet, StatusPath) {
		t.Fatalf("expected GET %s to match", StatusPath)
	}
	if Matches(http.MethodPost, StatusPath) {
		t.Fatalf("expected POST %s not to match", StatusPath)
	}
	if !Matches(http.MethodGet, BasePath) || !Matches(http.MethodPost, BasePath+"/api/query") {
		t.Fatalf("expected %s prefix routes to match", BasePath)
	}
	if Matches(http.MethodGet, "/api/v1/plugins/other") {
		t.Fatalf("unexpected match on unrelated plugin path")
	}
}

func TestProxyRequiresAuthAndStripsAstrorderCredentials(t *testing.T) {
	var seenPath, seenQuery, seenCookie, seenAuth, seenOrigin string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		seenCookie = r.Header.Get("Cookie")
		seenAuth = r.Header.Get("Authorization")
		seenOrigin = r.Header.Get("Origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.Header().Add("Set-Cookie", "dbx_session=abc123; Path=/api/v1/plugins/dbx; HttpOnly; Secure; SameSite=Lax")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer upstream.Close()

	h := NewHandler(Options{
		Authorizer:      newTestAuthenticator(),
		DefaultUpstream: upstream.URL,
		BrowserSecret:   "top-secret-token",
	})

	// 1. Unauthenticated request must fail with 401.
	unauthReq := httptest.NewRequest(http.MethodGet, BasePath+"/", nil)
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", unauthRec.Code)
	}

	// 2. Base path without trailing slash redirects to BasePath + "/".
	redirReq := httptest.NewRequest(http.MethodGet, BasePath+"?token=top-secret-token&tab=1", nil)
	redirRec := httptest.NewRecorder()
	h.ServeHTTP(redirRec, redirReq)
	if redirRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect, got %d", redirRec.Code)
	}
	if loc := redirRec.Header().Get("Location"); loc != BasePath+"/?tab=1" {
		t.Fatalf("expected redirect without token, got %q", loc)
	}

	// 3. Authenticated proxied request strips astrorder_session + Bearer + token, keeps dbx_session, and strips X-Frame-Options.
	req := httptest.NewRequest(http.MethodPost, BasePath+"/api/query?token=top-secret-token&foo=bar", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:8787"
	req.Header.Set("Origin", "http://127.0.0.1:8787")
	req.Header.Set("Authorization", "Bearer top-secret-token")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "top-secret-token"})
	req.AddCookie(&http.Cookie{Name: "dbx_session", Value: "dbx-val"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if seenPath != BasePath+"/api/query" {
		t.Fatalf("expected upstream path %s/api/query, got %q", BasePath, seenPath)
	}
	if seenQuery != "foo=bar" {
		t.Fatalf("expected token stripped from query, got %q", seenQuery)
	}
	if seenCookie != "dbx_session=dbx-val" {
		t.Fatalf("expected only dbx_session cookie forwarded, got %q", seenCookie)
	}
	if seenAuth != "" {
		t.Fatalf("expected Astrorder Bearer header stripped, got %q", seenAuth)
	}
	if seenOrigin != upstream.URL {
		t.Fatalf("expected Origin rewritten to %q, got %q", upstream.URL, seenOrigin)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("expected X-Frame-Options stripped, got %q", got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(strings.ToLower(csp), "frame-ancestors") {
		t.Fatalf("expected frame-ancestors stripped from CSP, got %q", csp)
	}
	if setCookie := rec.Header().Get("Set-Cookie"); strings.Contains(strings.ToLower(setCookie), "secure") {
		t.Fatalf("expected Secure stripped on plain HTTP Set-Cookie, got %q", setCookie)
	}
}

func TestStatusReadsDynamicPluginConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == BasePath+"/api/auth/check" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"authenticated":true,"required":false,"setup_required":false}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.SetPreference(context.Background(), prefPluginsConfig, map[string]any{
		PluginID: map[string]any{"upstreamUrl": upstream.URL},
	}, true); err != nil {
		t.Fatalf("save plugin config: %v", err)
	}

	h := NewHandler(Options{
		Store:           st,
		Authorizer:      newTestAuthenticator(),
		DefaultUpstream: "http://127.0.0.1:1", // should be overridden by store config
	})

	req := httptest.NewRequest(http.MethodGet, StatusPath, nil)
	req.Header.Set("Authorization", "Bearer top-secret-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var status StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Reachable || !status.Authenticated || status.PasswordRequired || status.UpstreamURL != upstream.URL {
		t.Fatalf("unexpected status response: %+v", status)
	}
}
