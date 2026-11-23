package files

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowBrowser struct{}

func (allowBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

type fakeStore struct {
	session    map[string]any
	connection map[string]any
}

func (s fakeStore) FindSessionByID(context.Context, string) (map[string]any, error) {
	return s.session, nil
}
func (s fakeStore) GetSSHConnection(context.Context, string) (map[string]any, error) {
	return s.connection, nil
}

type eventRecorder struct{ events []string }

func (p *eventRecorder) Publish(_ context.Context, event string, _ map[string]any) error {
	p.events = append(p.events, event)
	return nil
}

type openRecorder struct{ path, action string }

func (o *openRecorder) Open(_ context.Context, path, action string) error {
	o.path, o.action = path, action
	return nil
}

func TestRegistrationsExposeAllOwnedRoutes(t *testing.T) {
	registrations := Registrations()
	if len(registrations) != 8 {
		t.Fatalf("registrations=%d: %#v", len(registrations), registrations)
	}
	for _, registration := range registrations {
		if registration.Kind != "http" || registration.Auth != "browser" || registration.Owner != "go-app" {
			t.Fatalf("invalid registration: %#v", registration)
		}
	}
}

func TestFilesHTTPBlackBoxLocalRoutes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "目录", "file one.txt"), []byte("hello\x00world"), 0o600); err != nil {
		t.Fatal(err)
	}
	publisher := &eventRecorder{}
	opener := &openRecorder{}
	handler := testHandler(root, fakeStore{}, publisher, opener)

	response := call(t, handler, http.MethodGet, "/api/v1/files/tree?path="+queryEscape(root)+"&depth=2", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "file one.txt") || !strings.Contains(response.Body.String(), "目录") {
		t.Fatalf("tree=%d %s", response.Code, response.Body.String())
	}
	response = call(t, handler, http.MethodGet, "/api/v1/files/search?q=one&session_id=session", nil)
	if response.Code != 404 {
		t.Fatalf("unknown session must fail closed: %d %s", response.Code, response.Body.String())
	}
	response = call(t, handler, http.MethodGet, "/api/v1/files/search?q=one", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "file one.txt") {
		t.Fatalf("default search=%d %s", response.Code, response.Body.String())
	}

	body := `{"parent_path":` + quote(root) + `,"name":"new dir"}`
	response = call(t, handler, http.MethodPost, "/api/v1/files/mkdir", strings.NewReader(body))
	if response.Code != 200 {
		t.Fatalf("mkdir=%d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "new dir")); err != nil {
		t.Fatal(err)
	}

	body = `{"workspace":` + quote(root) + `,"paths":["目录/file one.txt","missing.txt"]}`
	response = call(t, handler, http.MethodPost, "/api/v1/files/exists", strings.NewReader(body))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "目录/file one.txt") || strings.Contains(response.Body.String(), "missing.txt") {
		t.Fatalf("exists=%d %s", response.Code, response.Body.String())
	}

	response = call(t, handler, http.MethodGet, "/api/v1/files/raw?download=true&path="+queryEscape(filepath.Join(root, "目录", "file one.txt")), nil)
	if response.Code != 200 || response.Body.String() != "hello\x00world" || !strings.Contains(response.Header().Get("Content-Disposition"), "file one.txt") {
		t.Fatalf("raw=%d headers=%v body=%q", response.Code, response.Header(), response.Body.Bytes())
	}

	content := "line 1\n第二行"
	body = `{"path":` + quote(filepath.Join(root, "目录", "save.go")) + `,"content":` + quote(content) + `}`
	response = call(t, handler, http.MethodPost, "/api/v1/system/write-file", strings.NewReader(body))
	if response.Code != 200 {
		t.Fatalf("write=%d %s", response.Code, response.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "目录", "save.go"))
	if err != nil || string(got) != content {
		t.Fatalf("written=%q err=%v", got, err)
	}

	body = `{"path":` + quote(filepath.Join(root, "目录", "save.go")) + `,"action":"reveal"}`
	response = call(t, handler, http.MethodPost, "/api/v1/system/open-file", strings.NewReader(body))
	if response.Code != 200 || opener.path != filepath.Join(root, "目录", "save.go") || opener.action != "reveal" {
		t.Fatalf("open=%d %s recorder=%+v", response.Code, response.Body.String(), opener)
	}
	if !reflect.DeepEqual(publisher.events, []string{"files.changed", "files.changed"}) {
		t.Fatalf("events=%v", publisher.events)
	}
}

func TestFilesHTTPRejectsUnknownRemoteWithoutLocalFallback(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("must-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := testHandler(root, fakeStore{}, nil, nil)
	response := call(t, handler, http.MethodGet, "/api/v1/files/raw?path="+queryEscape(secret)+"&connection_id=missing", nil)
	if response.Code != 404 || strings.Contains(response.Body.String(), "must-not-read") {
		t.Fatalf("remote fallback=%d %s", response.Code, response.Body.String())
	}
}

func TestFilesHTTPRejectsScopeEscapeAndInvalidJSON(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := testHandler(root, fakeStore{}, nil, nil)
	response := call(t, handler, http.MethodGet, "/api/v1/files/raw?path="+queryEscape(outside), nil)
	if response.Code != 403 || !strings.Contains(response.Body.String(), `"detail"`) {
		t.Fatalf("scope=%d %s", response.Code, response.Body.String())
	}
	response = call(t, handler, http.MethodPost, "/api/v1/files/mkdir", strings.NewReader("{"))
	if response.Code != 400 || response.Body.String() != "{\"detail\":\"Invalid JSON\"}\n" {
		t.Fatalf("json=%d %q", response.Code, response.Body.String())
	}
}

func TestFilesHTTPAllowsTempDirsAndSkipsForbiddenInExists(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside.txt")
	inWs := filepath.Join(ws, "note.txt")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inWs, []byte("in-ws"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Even when strict scope rejects `outside`, /api/v1/files/exists skips forbidden entries
	//    instead of failing the entire batch containing valid workspace files.
	strictHandler := testHandler(ws, fakeStore{}, nil, nil)
	existsBody := `{"workspace":` + quote(ws) + `,"paths":[` + quote(outside) + `,` + quote(inWs) + `]}`
	res := call(t, strictHandler, http.MethodPost, "/api/v1/files/exists", strings.NewReader(existsBody))
	var existsResp struct {
		Existing []string `json:"existing"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &existsResp) != nil || !reflect.DeepEqual(existsResp.Existing, []string{inWs}) {
		t.Fatalf("mixed exists failed: %d %s", res.Code, res.Body.String())
	}

	// 2. When AllowTempDirs is enabled (matching production httpapi), files in os.TempDir()
	//    work across /api/v1/files/exists, /api/v1/files/tree, and /api/v1/files/raw.
	tmpDir, err := os.MkdirTemp("", "astrorder-http-temp-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	tmpReport := filepath.Join(tmpDir, "temp-report.txt")
	if err := os.WriteFile(tmpReport, []byte("temp-report-body"), 0o600); err != nil {
		t.Fatal(err)
	}

	tempResolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            fakeStore{},
		DefaultWorkspace: ws,
		AllowedRoots:     []string{ws},
		AllowTempDirs:    true,
		LocalStageRoots:  []string{ws},
	})
	tempHandler := NewHandler(Options{Authorizer: allowBrowser{}, Resolver: tempResolver, StagingRoot: filepath.Join(ws, "stage")})

	existsTempBody := `{"workspace":` + quote(ws) + `,"paths":[` + quote(tmpReport) + `]}`
	res = call(t, tempHandler, http.MethodPost, "/api/v1/files/exists", strings.NewReader(existsTempBody))
	var tempExistsResp struct {
		Existing []string `json:"existing"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &tempExistsResp) != nil || !reflect.DeepEqual(tempExistsResp.Existing, []string{tmpReport}) {
		t.Fatalf("temp exists failed: %d %s", res.Code, res.Body.String())
	}

	res = call(t, tempHandler, http.MethodGet, "/api/v1/files/tree?depth=2&path="+queryEscape(tmpDir), nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"temp-report.txt"`) {
		t.Fatalf("temp tree failed: %d %s", res.Code, res.Body.String())
	}

	res = call(t, tempHandler, http.MethodGet, "/api/v1/files/raw?download=true&path="+queryEscape(tmpReport), nil)
	if res.Code != 200 || res.Body.String() != "temp-report-body" {
		t.Fatalf("temp raw failed: %d %q", res.Code, res.Body.String())
	}
}

func TestFilesRawSupportsVideoRangeAndHead(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "demo.mp4")
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	if err := os.WriteFile(videoPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := testHandler(root, fakeStore{}, &eventRecorder{}, &openRecorder{})
	rawURL := "/api/v1/files/raw?path=" + queryEscape(videoPath)

	// HEAD request returns video MIME type, Accept-Ranges, Content-Length, and empty body.
	headRes := call(t, handler, http.MethodHead, rawURL, nil)
	if headRes.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d body=%s", headRes.Code, headRes.Body.String())
	}
	if headRes.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("HEAD Content-Type=%q, want video/mp4", headRes.Header().Get("Content-Type"))
	}
	if headRes.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("HEAD Accept-Ranges=%q, want bytes", headRes.Header().Get("Accept-Ranges"))
	}
	if headRes.Header().Get("Content-Length") != "36" || headRes.Body.Len() != 0 {
		t.Fatalf("HEAD len=%q body=%d", headRes.Header().Get("Content-Length"), headRes.Body.Len())
	}

	// iOS Safari initial probe: Range: bytes=0-1 -> 206 Partial Content.
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("Range", "bytes=0-1")
	probeRes := httptest.NewRecorder()
	handler.ServeHTTP(probeRes, req)
	if probeRes.Code != http.StatusPartialContent {
		t.Fatalf("Range 0-1 status=%d body=%q", probeRes.Code, probeRes.Body.String())
	}
	if probeRes.Header().Get("Content-Range") != "bytes 0-1/36" || probeRes.Body.String() != "01" {
		t.Fatalf("Range 0-1 headers=%v body=%q", probeRes.Header(), probeRes.Body.String())
	}

	// Mid-file seek range: Range: bytes=10-15 -> "abcdef".
	req = httptest.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("Range", "bytes=10-15")
	midRes := httptest.NewRecorder()
	handler.ServeHTTP(midRes, req)
	if midRes.Code != http.StatusPartialContent || midRes.Body.String() != "abcdef" || midRes.Header().Get("Content-Range") != "bytes 10-15/36" {
		t.Fatalf("Range 10-15 status=%d headers=%v body=%q", midRes.Code, midRes.Header(), midRes.Body.String())
	}

	// Out-of-bounds range -> 416 Range Not Satisfiable.
	req = httptest.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("Range", "bytes=999-1000")
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, req)
	if badRes.Code != http.StatusRequestedRangeNotSatisfiable || badRes.Header().Get("Content-Range") != "bytes */36" {
		t.Fatalf("Range 999-1000 status=%d headers=%v", badRes.Code, badRes.Header())
	}
}

func testHandler(root string, store fakeStore, publisher Publisher, opener FileOpener) http.Handler {
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: store, DefaultWorkspace: root, AllowedRoots: []string{root}, LocalStageRoots: []string{root}})
	return NewHandler(Options{Authorizer: allowBrowser{}, Resolver: resolver, Publisher: publisher, Opener: opener, StagingRoot: filepath.Join(root, "stage")})
}

func call(t *testing.T, handler http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func queryEscape(value string) string {
	replacer := strings.NewReplacer("%", "%25", " ", "%20", "\\", "%5C", "目录", "%E7%9B%AE%E5%BD%95")
	return replacer.Replace(value)
}

var _ = errors.Is
