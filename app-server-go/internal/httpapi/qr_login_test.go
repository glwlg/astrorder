package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
)

func qrCall(t *testing.T, handler http.Handler, action string, payload map[string]string, cookie, bearer string) *httpResponse {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Origin": "http://testserver", "Content-Type": "application/json"}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}
	response := request(t, handler, http.MethodPost, "/api/v1/auth/qr/"+action, strings.NewReader(string(body)), headers)
	data := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return &httpResponse{code: response.Code, header: response.Header(), data: data}
}

type httpResponse struct {
	code   int
	header http.Header
	data   map[string]any
}

func TestQRLoginRequiresAuthenticatedPhoneAndOriginalPC(t *testing.T) {
	handler := NewHandler(Options{Config: testConfig()})
	created := qrCall(t, handler, "create", map[string]string{}, "", "")
	if created.code != http.StatusOK {
		t.Fatalf("create = %d %v", created.code, created.data)
	}
	id, _ := created.data["id"].(string)
	secret, _ := created.data["scan_secret"].(string)
	if len(id) < 32 || len(secret) < 32 || created.data["confirmation_code"] == "" {
		t.Fatalf("missing challenge fields")
	}
	cookie := strings.Split(created.header.Get("Set-Cookie"), ";")[0]
	if !strings.Contains(created.header.Get("Set-Cookie"), "HttpOnly") {
		t.Fatal("PC binding must be HttpOnly")
	}
	poll := map[string]string{"id": id}
	scan := map[string]string{"id": id, "scan_secret": secret}
	pending := qrCall(t, handler, "poll", poll, cookie, "")
	if pending.code != 200 || pending.data["status"] != "pending" {
		t.Fatalf("pending = %v", pending)
	}
	if qrCall(t, handler, "poll", poll, "", "").code != http.StatusForbidden {
		t.Fatal("another browser can poll")
	}
	if qrCall(t, handler, "inspect", scan, "", "").code != http.StatusUnauthorized {
		t.Fatal("anonymous phone can inspect")
	}
	scan["decision"] = "approve"
	if qrCall(t, handler, "approve", scan, "", "").code != http.StatusUnauthorized {
		t.Fatal("anonymous phone can approve")
	}
	approved := qrCall(t, handler, "approve", scan, "", "browser-secret")
	if approved.code != 200 || approved.data["status"] != "approved" {
		t.Fatalf("approve = %v", approved)
	}
	if qrCall(t, handler, "poll", poll, "", "").code != http.StatusForbidden {
		t.Fatal("approval leaked to another PC")
	}
	redeemed := qrCall(t, handler, "poll", poll, cookie, "")
	if redeemed.code != 200 || redeemed.data["authenticated"] != true || !strings.Contains(redeemed.header.Get("Set-Cookie"), "astrorder_session=browser-secret") {
		t.Fatalf("redeem = %v", redeemed)
	}
	if qrCall(t, handler, "poll", poll, cookie, "").code != http.StatusGone {
		t.Fatal("challenge was reusable")
	}
	if strings.Contains(string(mustQRJSON(t, created.data)), "browser-secret") {
		t.Fatal("QR exposes permanent credential")
	}
}

func TestQRLoginExpiryRejectionAndConcurrency(t *testing.T) {
	cfg := testConfig()
	now := time.Now()
	app := &api{config: cfg, auth: auth.New(cfg, nil), qr: newQRLogins()}
	app.qr.now = func() time.Time { return now }
	create := func() (*httpResponse, string, map[string]string) {
		t.Helper()
		created := qrCall(t, app, "create", map[string]string{}, "", "")
		if created.code != 200 {
			t.Fatalf("create=%v", created)
		}
		scan := map[string]string{"id": created.data["id"].(string), "scan_secret": created.data["scan_secret"].(string)}
		return created, strings.Split(created.header.Get("Set-Cookie"), ";")[0], scan
	}
	t.Run("expiry", func(t *testing.T) {
		_, cookie, scan := create()
		now = now.Add(qrLifetime)
		expired := qrCall(t, app, "poll", map[string]string{"id": scan["id"]}, cookie, "")
		if expired.data["status"] != "expired" || expired.header.Get("Set-Cookie") != "" {
			t.Fatalf("expired=%v", expired)
		}
		scan["decision"] = "approve"
		if qrCall(t, app, "approve", scan, "", "browser-secret").code != 410 {
			t.Fatal("expired QR approved")
		}
	})
	t.Run("reject", func(t *testing.T) {
		_, cookie, scan := create()
		scan["decision"] = "reject"
		rejected := qrCall(t, app, "approve", scan, "", "browser-secret")
		if rejected.data["status"] != "rejected" {
			t.Fatalf("reject=%v", rejected)
		}
		scan["decision"] = "approve"
		if qrCall(t, app, "approve", scan, "", "browser-secret").code != 409 {
			t.Fatal("rejected QR approved")
		}
		polled := qrCall(t, app, "poll", map[string]string{"id": scan["id"]}, cookie, "")
		if polled.data["status"] != "rejected" || polled.header.Get("Set-Cookie") != "" {
			t.Fatalf("poll rejected=%v", polled)
		}
	})
	t.Run("wrong_scan_and_connector", func(t *testing.T) {
		_, _, scan := create()
		if qrCall(t, app, "inspect", scan, "", "connector-secret").code != 401 {
			t.Fatal("connector can authorize browser")
		}
		inspect := qrCall(t, app, "inspect", scan, "", "browser-secret")
		if inspect.code != 200 || inspect.data["confirmation_code"] == "" || inspect.data["device"] == "" {
			t.Fatalf("inspect=%v", inspect)
		}
		scan["scan_secret"] = strings.Repeat("0", 64)
		if qrCall(t, app, "inspect", scan, "", "browser-secret").code != 403 {
			t.Fatal("wrong scan secret accepted")
		}
	})
	t.Run("refresh_retires_old_challenge", func(t *testing.T) {
		created, cookie, scan := create()
		fresh := qrCall(t, app, "create", map[string]string{}, cookie, "")
		if fresh.code != 200 || fresh.data["id"] == created.data["id"] {
			t.Fatal("refresh did not replace challenge")
		}
		if qrCall(t, app, "poll", map[string]string{"id": scan["id"]}, cookie, "").code != 410 {
			t.Fatal("old challenge survives refresh")
		}
	})
	t.Run("concurrent_redeem", func(t *testing.T) {
		_, cookie, scan := create()
		scan["decision"] = "approve"
		if qrCall(t, app, "approve", scan, "", "browser-secret").code != 200 {
			t.Fatal("approve failed")
		}
		var wg sync.WaitGroup
		results := make(chan int, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- qrCall(t, app, "poll", map[string]string{"id": scan["id"]}, cookie, "").code
			}()
		}
		wg.Wait()
		close(results)
		accepted := 0
		for code := range results {
			if code == 200 {
				accepted++
			} else if code != 410 {
				t.Fatalf("code=%d", code)
			}
		}
		if accepted != 1 {
			t.Fatalf("redeemed %d times", accepted)
		}
	})
}

func TestQRLoginRejectsUntrustedOriginsAndLimitsMemory(t *testing.T) {
	handler := NewHandler(Options{Config: testConfig()})
	for _, origin := range []string{"", "https://evil.example"} {
		response := request(t, handler, http.MethodPost, "/api/v1/auth/qr/create", strings.NewReader(`{}`), map[string]string{"Origin": origin})
		if response.Code != 403 {
			t.Fatalf("origin %q = %d", origin, response.Code)
		}
	}
	for _, body := range []string{"null", `{"extra":true}`, `{} {}`} {
		response := request(t, handler, http.MethodPost, "/api/v1/auth/qr/create", strings.NewReader(body), map[string]string{"Origin": "http://testserver"})
		if response.Code != 422 {
			t.Fatalf("invalid create %q = %d", body, response.Code)
		}
	}
	for _, action := range []string{"inspect", "approve"} {
		body := `{"id":"` + strings.Repeat("a", 64) + `","scan_secret":"` + strings.Repeat("b", 64) + `"}`
		response := request(t, handler, http.MethodPost, "/api/v1/auth/qr/"+action+"?token=browser-secret", strings.NewReader(body), map[string]string{"Origin": "http://testserver"})
		if response.Code != 401 {
			t.Fatalf("query credential accepted on %s: %d", action, response.Code)
		}
	}
	for i := 0; i < 20; i++ {
		if qrCall(t, handler, "create", map[string]string{}, "", "").code != 200 {
			t.Fatal("rate limit premature")
		}
	}
	if qrCall(t, handler, "create", map[string]string{}, "", "").code != 429 {
		t.Fatal("unbounded challenges")
	}
}

func TestQRLoginOverRealHTTPCookieJars(t *testing.T) {
	server := httptest.NewServer(NewHandler(Options{Config: testConfig()}))
	defer server.Close()
	newClient := func() *http.Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Client{Jar: jar, Timeout: 5 * time.Second}
	}
	pc, phone, stranger := newClient(), newClient(), newClient()
	call := func(client *http.Client, method, path string, payload any) (int, map[string]any) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(mustQRJSON(t, payload))
		}
		req, err := http.NewRequest(method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Origin", server.URL)
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data := map[string]any{}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data
	}
	_, login := call(phone, "POST", "/api/v1/auth/session", map[string]string{"token": "browser-secret"})
	if login["authenticated"] != true {
		t.Fatal("phone not logged in")
	}
	_, created := call(pc, "POST", "/api/v1/auth/qr/create", map[string]string{})
	scan := map[string]string{"id": created["id"].(string), "scan_secret": created["scan_secret"].(string)}
	if code, _ := call(stranger, "POST", "/api/v1/auth/qr/inspect", scan); code != 401 {
		t.Fatal("stranger inspected")
	}
	_, inspected := call(phone, "POST", "/api/v1/auth/qr/inspect", scan)
	if inspected["confirmation_code"] != created["confirmation_code"] {
		t.Fatal("confirmation code mismatch")
	}
	_, before := call(pc, "GET", "/api/v1/auth/session", nil)
	if before["authenticated"] != false {
		t.Fatal("PC authorized before confirmation")
	}
	scan["decision"] = "approve"
	_, approved := call(phone, "POST", "/api/v1/auth/qr/approve", scan)
	if approved["status"] != "approved" {
		t.Fatal("phone did not approve")
	}
	_, afterApprove := call(pc, "GET", "/api/v1/auth/session", nil)
	if afterApprove["authenticated"] != false {
		t.Fatal("approval bypassed PC binding")
	}
	_, polled := call(pc, "POST", "/api/v1/auth/qr/poll", map[string]string{"id": scan["id"]})
	if polled["authenticated"] != true {
		t.Fatal("PC did not redeem")
	}
	_, session := call(pc, "GET", "/api/v1/auth/session", nil)
	if session["authenticated"] != true {
		t.Fatal("login cookie was not retained")
	}
	_, other := call(stranger, "GET", "/api/v1/auth/session", nil)
	if other["authenticated"] != false {
		t.Fatal("stranger logged in")
	}
}

func mustQRJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
