package git

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowBrowser struct{}

func (allowBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

type nilStore struct{}

func (nilStore) FindSessionByID(context.Context, string) (map[string]any, error)  { return nil, nil }
func (nilStore) GetSSHConnection(context.Context, string) (map[string]any, error) { return nil, nil }

type eventRecorder struct{ names []string }

func (p *eventRecorder) Publish(_ context.Context, name string, _ map[string]any) error {
	p.names = append(p.names, name)
	return nil
}

func TestRegistrationsExposeGitRoutes(t *testing.T) {
	registrations := Registrations()
	if len(registrations) != 3 {
		t.Fatalf("registrations=%#v", registrations)
	}
	for _, registration := range registrations {
		if registration.Kind != "http" || registration.Auth != "browser" || registration.Owner != "go-app" {
			t.Fatalf("registration=%#v", registration)
		}
	}
}

func TestGitHTTPStatusPreservesQuotedUnicodeAndCountsBothColumns(t *testing.T) {
	repository := initRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "空 格.txt"), []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, repository, "add", "空 格.txt")
	if err := os.WriteFile(filepath.Join(repository, "空 格.txt"), []byte("staged\nthen modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "未跟踪.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := testGitHandler(repository, nil)
	response := gitCall(t, handler, http.MethodGet, "/api/v1/git/status?workspace="+queryEscape(repository), nil)
	if response.Code != 200 {
		t.Fatalf("status=%d %s", response.Code, response.Body.String())
	}
	var result statusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ChangedFiles != 2 || result.StagedCount != 1 || result.UnstagedCount != 1 || result.UntrackedCount != 1 || result.Clean {
		t.Fatalf("counts=%+v", result)
	}
	if result.Files[0].Path != "空 格.txt" || result.Files[1].Path != "未跟踪.txt" {
		t.Fatalf("files=%+v", result.Files)
	}
}

func TestGitHTTPBranchMutatesOnlyTemporaryRepository(t *testing.T) {
	repository := initRepository(t)
	publisher := &eventRecorder{}
	handler := testGitHandler(repository, publisher)
	body := `{"workspace":` + quote(repository) + `,"branch":"feature/测试","create":true}`
	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/git/diff-raw?workspace="+queryEscape(repository)+"&path=missing.txt", nil)
	handler.ServeHTTP(missing, missingRequest)
	if missing.Code != 404 || !strings.Contains(missing.Body.String(), `"detail"`) {
		t.Fatalf("missing diff=%d %s", missing.Code, missing.Body.String())
	}
	traversal := httptest.NewRecorder()
	traversalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/git/diff-raw?workspace="+queryEscape(repository)+"&path=../outside.txt", nil)
	handler.ServeHTTP(traversal, traversalRequest)
	if traversal.Code != 400 {
		t.Fatalf("traversal diff=%d %s", traversal.Code, traversal.Body.String())
	}

	response := gitCall(t, handler, http.MethodPost, "/api/v1/git/branch", strings.NewReader(body))
	if response.Code != 200 {
		t.Fatalf("branch=%d %s", response.Code, response.Body.String())
	}
	current := strings.TrimSpace(runGitCommand(t, repository, "branch", "--show-current"))
	if current != "feature/测试" {
		t.Fatalf("branch=%q", current)
	}
	if len(publisher.names) != 1 || publisher.names[0] != "git.branch.changed" {
		t.Fatalf("events=%v", publisher.names)
	}
}

func TestGitHTTPDiffRawHandlesUntrackedUnicodeWithoutArgumentInterpolation(t *testing.T) {
	repository := initRepository(t)
	name := "new name ü.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("第一行\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := "/api/v1/git/diff-raw?workspace=" + queryEscape(repository) + "&path=" + queryEscape(name)
	response := gitCall(t, testGitHandler(repository, nil), http.MethodGet, target, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "+++ b/"+name) || !strings.Contains(response.Body.String(), "+第一行") {
		t.Fatalf("diff=%d %s", response.Code, response.Body.String())
	}
}

func TestGitHTTPFailsForNonRepositoryAndUnknownRemote(t *testing.T) {
	root := t.TempDir()
	// GOTMPDIR may be inside a checkout. Stop discovery at the fixture parent
	// so this empty directory cannot accidentally select the real repository.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	handler := testGitHandler(root, nil)
	response := gitCall(t, handler, http.MethodGet, "/api/v1/git/status?workspace="+queryEscape(root), nil)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "Git 状态") {
		t.Fatalf("nonrepo=%d %s", response.Code, response.Body.String())
	}
	response = gitCall(t, handler, http.MethodGet, "/api/v1/git/status?workspace="+queryEscape(root)+"&connection_id=missing", nil)
	if response.Code != 404 {
		t.Fatalf("remote=%d %s", response.Code, response.Body.String())
	}
}

func initRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGitCommand(t, repository, "init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, repository, "add", "base.txt")
	runGitCommand(t, repository, "-c", "user.name=Astrorder Test", "-c", "user.email=test@invalid", "commit", "-qm", "base")
	return repository
}

func runGitCommand(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func testGitHandler(root string, publisher Publisher) http.Handler {
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: nilStore{}, DefaultWorkspace: root, AllowedRoots: []string{root}})
	return NewHandler(Options{Authorizer: allowBrowser{}, Resolver: resolver, Publisher: publisher})
}

func gitCall(t *testing.T, handler http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
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
	replacer := strings.NewReplacer("%", "%25", " ", "%20", "\\", "%5C", "/", "%2F", "ü", "%C3%BC", "测", "%E6%B5%8B", "试", "%E8%AF%95")
	return replacer.Replace(value)
}
