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
