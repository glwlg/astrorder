// Package dbxplugin provides an authenticated reverse proxy and health probe
// for an embedded DBX (t8y2/dbx) Web workbench mounted inside Astrorder's
// Sidecar panel at /api/v1/plugins/dbx/.
package dbxplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

const (
	BasePath           = "/api/v1/plugins/dbx"
	StatusPath         = "/api/v1/plugins/dbx-status"
	DefaultUpstreamURL = "http://127.0.0.1:14224"
	PluginID           = "dbx-viewer"
	prefPluginsConfig  = "plugins:config"
)

// BrowserAuthorizer validates browser session credentials and Origin headers.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
	IsBrowserBearer(*http.Request) bool
}

// Options configures the DBX plugin proxy and status handler.
type Options struct {
	Store           *store.Store
	Authorizer      BrowserAuthorizer
	DefaultUpstream string
	BrowserSecret   string
	Transport       http.RoundTripper
	HTTPClient      *http.Client
	Logger          *slog.Logger
}

// StatusResponse describes the current DBX upstream configuration and health.
type StatusResponse struct {
	Configured          bool   `json:"configured"`
	UpstreamURL         string `json:"upstream_url"`
	BasePath            string `json:"base_path"`
	Reachable           bool   `json:"reachable"`
	Authenticated       bool   `json:"authenticated"`
	PasswordRequired    bool   `json:"password_required"`
	SetupRequired       bool   `json:"setup_required"`
	ContextPathMismatch bool   `json:"context_path_mismatch,omitempty"`
	Error               string `json:"error,omitempty"`
}

type handler struct {
	store           *store.Store
	authorizer      BrowserAuthorizer
	defaultUpstream string
	browserSecret   string
	transport       http.RoundTripper
	httpClient      *http.Client
	logger          *slog.Logger
}

// NewHandler creates the HTTP handler for /api/v1/plugins/dbx* routes.
func NewHandler(options Options) http.Handler {
	defUpstream := strings.TrimSpace(options.DefaultUpstream)
	if defUpstream == "" {
		defUpstream = DefaultUpstreamURL
	}
	transport := options.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:   4 * time.Second,
			Transport: transport,
		}
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &handler{
		store:           options.Store,
		authorizer:      options.Authorizer,
		defaultUpstream: defUpstream,
		browserSecret:   options.BrowserSecret,
		transport:       transport,
		httpClient:      client,
		logger:          logger,
	}
}

// Matches reports whether this package owns the request route.
func Matches(method, requestPath string) bool {
	if requestPath == StatusPath {
		return method == http.MethodGet
	}
	return requestPath == BasePath || strings.HasPrefix(requestPath, BasePath+"/")
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authorizer != nil {
		if failure := h.authorizer.AuthorizeBrowser(r); failure != nil {
			writeJSON(w, failure.Status, map[string]string{"detail": failure.Detail})
			return
		}
	}
	if r.URL.Path == StatusPath {
		h.serveStatus(w, r)
		return
	}
	h.serveProxy(w, r)
}

func (h *handler) configuredUpstreamRaw(ctx context.Context) string {
	if h.store != nil {
		if pref, err := h.store.GetPreference(ctx, prefPluginsConfig); err == nil && pref.Exists {
			if configs, ok := pref.Value.(map[string]any); ok {
				if dbxCfg, ok := configs[PluginID].(map[string]any); ok {
					if raw, ok := dbxCfg["upstreamUrl"].(string); ok {
						if trimmed := strings.TrimSpace(raw); trimmed != "" {
							return trimmed
						}
					}
				}
			}
		}
	}
	return h.defaultUpstream
}

func parseUpstreamURL(raw string) (*url.URL, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" || strings.ContainsAny(cleaned, "\r\n\x00") {
		return nil, errors.New("DBX 服务地址不能为空")
	}
	parsed, err := url.Parse(cleaned)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, errors.New("DBX 服务地址必须是完整的 http 或 https 地址")
	}
	if parsed.User != nil {
		return nil, errors.New("DBX 服务地址不能包含内嵌凭据")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func (h *handler) serveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	raw := h.configuredUpstreamRaw(r.Context())
	target, err := parseUpstreamURL(raw)
	if err != nil {
		writeJSON(w, http.StatusOK, StatusResponse{
			Configured:  false,
			UpstreamURL: raw,
			BasePath:    BasePath,
			Reachable:   false,
			Error:       err.Error(),
		})
		return
	}

	probe := func(pathSuffix string) (*http.Response, error) {
		probeURL := *target
		probeURL.Path = strings.TrimRight(target.Path, "/") + pathSuffix
		req, reqErr := http.NewRequestWithContext(r.Context(), http.MethodGet, probeURL.String(), nil)
		if reqErr != nil {
			return nil, reqErr
		}
		return h.httpClient.Do(req)
	}

	resp, probeErr := probe(BasePath + "/api/auth/check")
	if probeErr != nil {
		writeJSON(w, http.StatusOK, StatusResponse{
			Configured:  true,
			UpstreamURL: target.String(),
			BasePath:    BasePath,
			Reachable:   false,
			Error:       fmt.Sprintf("无法连接 DBX 服务（%s）：%v", target.String(), probeErr),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Check whether DBX is running at root without DBX_PUBLIC_BASE_PATH.
		if rootResp, rootErr := probe("/api/auth/check"); rootErr == nil {
			defer rootResp.Body.Close()
			if rootResp.StatusCode == http.StatusOK {
				writeJSON(w, http.StatusOK, StatusResponse{
					Configured:          true,
					UpstreamURL:         target.String(),
					BasePath:            BasePath,
					Reachable:           true,
					ContextPathMismatch: true,
					Error:               fmt.Sprintf("DBX 服务已启动但未配置 DBX_PUBLIC_BASE_PATH=%s，请在容器环境变量中补充后重启", BasePath),
				})
				return
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusOK, StatusResponse{
			Configured:  true,
			UpstreamURL: target.String(),
			BasePath:    BasePath,
			Reachable:   false,
			Error:       fmt.Sprintf("DBX 状态检查返回 HTTP %d", resp.StatusCode),
		})
		return
	}

	var check struct {
		Authenticated bool `json:"authenticated"`
		Required      bool `json:"required"`
		SetupRequired bool `json:"setup_required"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&check); err != nil {
		writeJSON(w, http.StatusOK, StatusResponse{
			Configured:  true,
			UpstreamURL: target.String(),
			BasePath:    BasePath,
			Reachable:   false,
			Error:       "DBX 返回的状态响应格式异常",
		})
		return
	}

	writeJSON(w, http.StatusOK, StatusResponse{
		Configured:       true,
		UpstreamURL:      target.String(),
		BasePath:         BasePath,
		Reachable:        true,
		Authenticated:    check.Authenticated,
		PasswordRequired: check.Required,
		SetupRequired:    check.SetupRequired,
	})
}

func (h *handler) serveProxy(w http.ResponseWriter, r *http.Request) {
	// Ensure iframe sub-requests can reuse the HttpOnly session cookie when the
	// initial iframe navigation was authenticated via ?token= or Bearer header.
	if h.browserSecret != "" {
		if _, err := r.Cookie(auth.CookieName); err != nil {
			http.SetCookie(w, &http.Cookie{
				Name:     auth.CookieName,
				Value:    h.browserSecret,
				Path:     "/",
				MaxAge:   60 * 60 * 24 * 30,
				HttpOnly: true,
				Secure:   false,
				SameSite: http.SameSiteLaxMode,
			})
		}
	}

	if r.URL.Path == BasePath {
		query := r.URL.Query()
		query.Del("token")
		redirectTo := BasePath + "/"
		if encoded := query.Encode(); encoded != "" {
			redirectTo += "?" + encoded
		}
		http.Redirect(w, r, redirectTo, http.StatusTemporaryRedirect)
		return
	}

	raw := h.configuredUpstreamRaw(r.Context())
	target, err := parseUpstreamURL(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
		return
	}

	stripBearer := h.authorizer != nil && h.authorizer.IsBrowserBearer(r)
	isHTTPS := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	origHost := r.Host
	origProto := "http"
	if isHTTPS {
		origProto = "https"
	}

	proxy := &httputil.ReverseProxy{
		Transport:     h.transport,
		FlushInterval: -1,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			if target.Path != "" && !strings.HasPrefix(req.URL.Path, target.Path+"/") && req.URL.Path != target.Path {
				req.URL.Path = strings.TrimRight(target.Path, "/") + req.URL.Path
				req.URL.RawPath = ""
			}
			// Remove Astrorder query token so it is never leaked upstream.
			if req.URL.RawQuery != "" {
				q := req.URL.Query()
				if q.Has("token") {
					q.Del("token")
					req.URL.RawQuery = q.Encode()
				}
			}
			req.Host = target.Host

			// Strip Astrorder session cookie while preserving dbx_session and other DBX cookies.
			filterUpstreamCookies(req)
			if stripBearer {
				req.Header.Del("Authorization")
			}

			// Align Origin/Referer with upstream target so DBX CSRF checks accept proxied writes.
			if origin := req.Header.Get("Origin"); origin != "" {
				req.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			if referer := req.Header.Get("Referer"); referer != "" {
				if refURL, parseErr := url.Parse(referer); parseErr == nil {
					refURL.Scheme = target.Scheme
					refURL.Host = target.Host
					req.Header.Set("Referer", refURL.String())
				}
			}
			if origHost != "" {
				req.Header.Set("X-Forwarded-Host", origHost)
			}
			req.Header.Set("X-Forwarded-Proto", origProto)
			req.Header.Set("X-Forwarded-Prefix", BasePath)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Frame-Options")
			if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
				resp.Header.Set("Content-Security-Policy", stripFrameAncestors(csp))
			}
			if !isHTTPS {
				if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
					resp.Header.Del("Set-Cookie")
					for _, rawCookie := range cookies {
						resp.Header.Add("Set-Cookie", stripSecureCookieAttr(rawCookie))
					}
				}
			}
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			h.logger.Warn("dbx upstream proxy error", "upstream", target.String(), "path", req.URL.Path, "error", proxyErr)
			if strings.Contains(req.Header.Get("Accept"), "text/html") {
				rw.Header().Set("Content-Type", "text/html; charset=utf-8")
				rw.Header().Set("Cache-Control", "no-store")
				rw.WriteHeader(http.StatusBadGateway)
				_, _ = fmt.Fprintf(rw, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>DBX 未连接</title><style>body{font-family:system-ui,sans-serif;padding:32px;color:#334155;background:#f8fafc;margin:0}code{background:#e2e8f0;padding:2px 6px;border-radius:4px}</style></head><body><h3>无法连接 DBX 服务</h3><p>上游地址：<code>%s</code></p><p>请确认 DBX 容器已启动，或在星序「设置 → 插件 → DBX 数据库工作台」检查服务地址。</p></body></html>`, target.String())
				return
			}
			writeJSON(rw, http.StatusBadGateway, map[string]string{
				"detail": fmt.Sprintf("无法连接 DBX 服务（%s）", target.String()),
			})
		},
	}

	proxy.ServeHTTP(w, r)
}

func filterUpstreamCookies(req *http.Request) {
	cookies := req.Cookies()
	if len(cookies) == 0 {
		return
	}
	kept := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Name == auth.CookieName {
			continue
		}
		kept = append(kept, c.Name+"="+c.Value)
	}
	if len(kept) == 0 {
		req.Header.Del("Cookie")
	} else {
		req.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

func stripFrameAncestors(csp string) string {
	parts := strings.Split(csp, ";")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "frame-ancestors" || strings.HasPrefix(lower, "frame-ancestors ") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; ")
}

func stripSecureCookieAttr(raw string) string {
	parts := strings.Split(raw, ";")
	kept := make([]string, 0, len(parts))
	for i, part := range parts {
		if i > 0 && strings.EqualFold(strings.TrimSpace(part), "secure") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ";")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
