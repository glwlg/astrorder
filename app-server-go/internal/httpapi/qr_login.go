package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const qrCookieName = "astrorder_qr_request"
const qrLifetime = 3 * time.Minute

type qrLogin struct {
	binding  [32]byte
	scanHash [32]byte
	expires  time.Time
	status   string
	code     string
	device   string
	ip       string
}

type qrLogins struct {
	mu    sync.Mutex
	items map[string]*qrLogin
	now   func() time.Time
}

func newQRLogins() *qrLogins { return &qrLogins{items: map[string]*qrLogin{}, now: time.Now} }

func qrRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validQRKey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func serveQRLogin(a *api, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if a.config.BrowserSecret == "" {
		writeJSONError(w, 503, "Authentication is not configured")
		return
	}
	// These browser-only POSTs require an explicit origin, including for cookies.
	if r.Header.Get("Origin") == "" || !a.auth.IsAllowedOrigin(r.Context(), r.Header.Get("Origin"), r.Host) {
		writeJSONError(w, 403, "Origin is not allowed")
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/auth/qr/")
	if action == "inspect" || action == "approve" {
		// Scan links cannot authenticate the phone through a URL token. Only an
		// existing browser cookie or same-origin Bearer session is accepted.
		if r.URL.Query().Has("token") {
			writeJSONError(w, http.StatusUnauthorized, "请先在手机端登录星序")
			return
		}
		if failure := a.auth.AuthorizeBrowser(r); failure != nil {
			writeAuthError(w, failure)
			return
		}
	}
	var payload *struct {
		ID         string `json:"id"`
		ScanSecret string `json:"scan_secret"`
		Decision   string `json:"decision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload == nil || decodeHasTrailingValue(decoder) {
		writeJSONError(w, 422, "Invalid request")
		return
	}
	if action == "create" {
		if payload.ID != "" || payload.ScanSecret != "" || payload.Decision != "" {
			writeJSONError(w, 422, "Invalid request")
			return
		}
		a.createQRLogin(w, r)
		return
	}
	if !validQRKey(payload.ID) || (action != "poll" && !validQRKey(payload.ScanSecret)) || (action == "poll" && (payload.ScanSecret != "" || payload.Decision != "")) || (action == "inspect" && payload.Decision != "") || (action == "approve" && payload.Decision != "approve" && payload.Decision != "reject") {
		writeJSONError(w, 422, "Invalid request")
		return
	}
	q := a.qr
	q.mu.Lock()
	defer q.mu.Unlock()
	item := q.items[payload.ID]
	if item == nil {
		writeJSONError(w, http.StatusGone, "二维码已失效，请在电脑上刷新")
		return
	}
	if action == "poll" {
		cookie, err := r.Cookie(qrCookieName)
		if err != nil || sha256.Sum256([]byte(cookie.Value)) != item.binding {
			writeJSONError(w, 403, "Login request belongs to another browser")
			return
		}
	} else if sha256.Sum256([]byte(payload.ScanSecret)) != item.scanHash {
		writeJSONError(w, 403, "Invalid scan secret")
		return
	}
	if !q.now().Before(item.expires) {
		delete(q.items, payload.ID)
		writeJSON(w, 200, map[string]any{"status": "expired"})
		return
	}
	if action == "poll" {
		result := map[string]any{"status": item.status}
		if item.status == "approved" {
			delete(q.items, payload.ID)
			setBrowserCookie(w, a.config.BrowserSecret)
			http.SetCookie(w, &http.Cookie{Name: qrCookieName, Path: "/api/v1/auth/qr", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
			result["authenticated"] = true
		}
		writeJSON(w, 200, result)
		return
	}
	if action == "inspect" {
		writeJSON(w, 200, map[string]any{"status": item.status, "expires_at": item.expires.UTC().Format(time.RFC3339), "confirmation_code": item.code, "device": item.device})
		return
	}
	if item.status != "pending" {
		writeJSONError(w, 409, "二维码已处理，请在电脑上重新发起登录")
		return
	}
	item.status = "rejected"
	if payload.Decision == "approve" {
		item.status = "approved"
	}
	writeJSON(w, 200, map[string]any{"status": item.status})
}

func (a *api) createQRLogin(w http.ResponseWriter, r *http.Request) {
	id, err := qrRandom()
	if err != nil {
		writeJSONError(w, 500, "Unable to create login request")
		return
	}
	secret, err := qrRandom()
	if err != nil {
		writeJSONError(w, 500, "Unable to create login request")
		return
	}
	binding, err := qrRandom()
	if err != nil {
		writeJSONError(w, 500, "Unable to create login request")
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	q := a.qr
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	count := 0
	for key, item := range q.items {
		if !now.Before(item.expires) {
			delete(q.items, key)
			continue
		}
		if item.ip == ip {
			count++
		}
	}
	// Bound unauthenticated memory allocation; refreshing the same browser retires
	// its old challenge instead of consuming another outstanding slot.
	if cookie, err := r.Cookie(qrCookieName); err == nil {
		hash := sha256.Sum256([]byte(cookie.Value))
		for key, item := range q.items {
			if item.binding == hash {
				delete(q.items, key)
				if item.ip == ip {
					count--
				}
			}
		}
	}
	if count >= 20 || len(q.items) >= 1024 {
		w.Header().Set("Retry-After", "180")
		writeJSONError(w, 429, "登录请求过多，请稍后重试")
		return
	}
	device := "电脑浏览器"
	ua := r.UserAgent()
	if strings.Contains(ua, "Edg/") {
		device = "Microsoft Edge"
	} else if strings.Contains(ua, "Chrome/") {
		device = "Chrome"
	} else if strings.Contains(ua, "Firefox/") {
		device = "Firefox"
	} else if strings.Contains(ua, "Safari/") {
		device = "Safari"
	}
	sum := sha256.Sum256([]byte(id))
	code := fmt.Sprintf("%06d", (uint32(sum[0])<<16|uint32(sum[1])<<8|uint32(sum[2]))%1000000)
	item := &qrLogin{binding: sha256.Sum256([]byte(binding)), scanHash: sha256.Sum256([]byte(secret)), expires: now.Add(qrLifetime), status: "pending", code: code, device: device, ip: ip}
	q.items[id] = item
	http.SetCookie(w, &http.Cookie{Name: qrCookieName, Value: binding, Path: "/api/v1/auth/qr", MaxAge: int(qrLifetime.Seconds()), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"id": id, "scan_secret": secret, "expires_at": item.expires.UTC().Format(time.RFC3339), "confirmation_code": code})
}
