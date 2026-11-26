package gitlabci

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowBrowserAuth struct{}

func (allowBrowserAuth) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func TestParseGitRemoteURL(t *testing.T) {
	cases := []struct {
		raw      string
		wantHost string
		wantPort string
		wantProj string
		wantOK   bool
	}{
		{"https://gitlab.company.internal/ops/opscore.git", "gitlab.company.internal", "", "ops/opscore", true},
		{"http://10.0.0.12:8088/team/sub/service.git", "10.0.0.12", "8088", "team/sub/service", true},
		{"https://oauth2:glpat-secret@git.internal.local/a/b/c", "git.internal.local", "", "a/b/c", true},
		{"git@gitlab.company.internal:ops/opscore.git", "gitlab.company.internal", "", "ops/opscore", true},
		{"ssh://git@gitlab.company.internal:2222/ops/sub/opscore.git", "gitlab.company.internal", "2222", "ops/sub/opscore", true},
		{"not-a-valid-url", "", "", "", false},
	}
	for _, tc := range cases {
		h, p, proj, ok := ParseGitRemoteURL(tc.raw)
		if ok != tc.wantOK || h != tc.wantHost || p != tc.wantPort || proj != tc.wantProj {
			t.Errorf("ParseGitRemoteURL(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)", tc.raw, h, p, proj, ok, tc.wantHost, tc.wantPort, tc.wantProj, tc.wantOK)
		}
	}
}

func TestGitLabCRUDAutoDetectPipelinesJobsTraceAndActions(t *testing.T) {
	// Mock self-hosted GitLab v4 API server.
	var retriedPipeline, retriedJob bool
	glServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-selfhosted-123456" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		switch {
		case r.URL.Path == "/api/v4/user":
			_, _ = w.Write([]byte(`{"username":"luwei","name":"Lu Wei"}`))
		case r.URL.Path == "/api/v4/version":
			_, _ = w.Write([]byte(`{"version":"17.5.2-ee"}`))
		case r.Method == http.MethodGet && r.URL.RawPath == "/api/v4/projects/ops%2Fopscore/pipelines" || (r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/projects/ops%2Fopscore/pipelines")):
			_, _ = w.Write([]byte(`[
				{"id":501,"iid":88,"status":"running","ref":"master","sha":"abcdef1234567890","web_url":"http://gitlab.test/ops/opscore/-/pipelines/501","created_at":"2026-11-26T10:00:00Z","duration":42},
				{"id":500,"iid":87,"status":"failed","ref":"master","sha":"1111111122222222","web_url":"http://gitlab.test/ops/opscore/-/pipelines/500","created_at":"2026-11-26T09:00:00Z","duration":90}
			]`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/501"):
			_, _ = w.Write([]byte(`{"id":501,"iid":88,"status":"failed","ref":"master","sha":"abcdef1234567890","web_url":"http://gitlab.test/ops/opscore/-/pipelines/501","duration":42}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/501/jobs"):
			_, _ = w.Write([]byte(`[
				{"id":9001,"name":"check-backend","stage":"build","status":"success","duration":18,"commit":{"title":"feat: gitlab integration","author_name":"Lu Wei"},"runner":{"description":"docker-runner-1"}},
				{"id":9002,"name":"test-frontend","stage":"test","status":"failed","duration":24,"failure_reason":"script_failure"}
			]`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/501/bridges"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/jobs/9002/trace"):
			_, _ = w.Write([]byte("\x1b[32mline 1\x1b[0m\nline 2\n\x1b[31mFAIL: assertion error\x1b[0m\n"))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.EscapedPath(), "/pipelines/501/retry"):
			retriedPipeline = true
			_, _ = w.Write([]byte(`{"id":501,"status":"pending","ref":"master","sha":"abcdef1234567890"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.EscapedPath(), "/jobs/9002/retry"):
			retriedJob = true
			_, _ = w.Write([]byte(`{"id":9003,"name":"test-frontend","stage":"test","status":"pending"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found: ` + r.URL.EscapedPath() + `"}`))
		}
	}))
	defer glServer.Close()

	parsedServer, _ := url.Parse(glServer.URL)
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Create a fake workspace with .git/config pointing at the mock self-hosted GitLab host:port.
	repoWS := filepath.Join(tmp, "opscore")
	if err := os.MkdirAll(filepath.Join(repoWS, ".git", "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitConfig := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = " + glServer.URL + "/ops/opscore.git\n"
	if err := os.WriteFile(filepath.Join(repoWS, ".git", "config"), []byte(gitConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoWS, ".git", "HEAD"), []byte("ref: refs/heads/master\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoWS, ".git", "refs", "heads", "master"), []byte("abcdef1234567890\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            st,
		DefaultWorkspace: repoWS,
		AllowTempDirs:    true,
		AllowedRoots:     []string{tmp},
	})
	svc := NewService(Options{
		Store:      st,
		Authorizer: allowBrowserAuth{},
		Resolver:   resolver,
		HTTPClient: glServer.Client(),
	})

	// 1. Test connection before saving.
	testBody, _ := json.Marshal(TestInstanceRequest{BaseURL: glServer.URL, Token: "glpat-selfhosted-123456"})
	testW := httptest.NewRecorder()
	svc.ServeHTTP(testW, httptest.NewRequest(http.MethodPost, BasePath+"/instances/test", bytes.NewReader(testBody)))
	if testW.Code != http.StatusOK {
		t.Fatalf("test status=%d body=%s", testW.Code, testW.Body.String())
	}
	var testResp TestInstanceResponse
	_ = json.Unmarshal(testW.Body.Bytes(), &testResp)
	if !testResp.OK || testResp.Username != "luwei" || testResp.Version != "17.5.2-ee" {
		t.Fatalf("unexpected test resp: %+v", testResp)
	}

	// 2. Create GitLab instance via HTTP; verify token is masked in response.
	createBody, _ := json.Marshal(CreateInstanceRequest{
		Name:    "自建 GitLab",
		BaseURL: glServer.URL,
		Token:   "glpat-selfhosted-123456",
	})
	createW := httptest.NewRecorder()
	svc.ServeHTTP(createW, httptest.NewRequest(http.MethodPost, BasePath+"/instances", bytes.NewReader(createBody)))
	if createW.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createW.Code, createW.Body.String())
	}
	if strings.Contains(createW.Body.String(), "glpat-selfhosted-123456") {
		t.Fatalf("raw token leaked in create response: %s", createW.Body.String())
	}
	var created struct {
		Instance PublicInstance `json:"instance"`
	}
	_ = json.Unmarshal(createW.Body.Bytes(), &created)
	if !created.Instance.TokenSet || created.Instance.Host != parsedServer.Host {
		t.Fatalf("unexpected public instance: %+v", created.Instance)
	}

	// 3. Query pipelines by workspace -> auto-detects remote, branch, HEAD SHA, and matches HEAD pipeline #501.
	listURL := BasePath + "/pipelines?workspace=" + url.QueryEscape(repoWS)
	listW := httptest.NewRecorder()
	svc.ServeHTTP(listW, httptest.NewRequest(http.MethodGet, listURL, nil))
	if listW.Code != http.StatusOK {
		t.Fatalf("pipelines status=%d body=%s", listW.Code, listW.Body.String())
	}
	var listResp PipelinesResponse
	_ = json.Unmarshal(listW.Body.Bytes(), &listResp)
	if !listResp.Configured || !listResp.Matched || listResp.Repo == nil || listResp.Repo.ProjectPath != "ops/opscore" {
		t.Fatalf("unexpected pipelines resp: %+v", listResp)
	}
	if listResp.LatestPipeline == nil || listResp.LatestPipeline.ID != 501 || !listResp.LatestPipeline.MatchesHead {
		t.Fatalf("expected HEAD pipeline #501, got %+v", listResp.LatestPipeline)
	}

	// 4. Pipeline detail -> stages & commit title.
	detW := httptest.NewRecorder()
	svc.ServeHTTP(detW, httptest.NewRequest(http.MethodGet, BasePath+"/pipelines/501?workspace="+url.QueryEscape(repoWS), nil))
	if detW.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detW.Code, detW.Body.String())
	}
	var detResp PipelineDetailResponse
	_ = json.Unmarshal(detW.Body.Bytes(), &detResp)
	if len(detResp.Stages) != 2 || detResp.Stages[0].Name != "build" || detResp.Stages[1].Status != "failed" || detResp.Pipeline.CommitTitle != "feat: gitlab integration" {
		t.Fatalf("unexpected pipeline detail: %+v", detResp)
	}

	// 5. Job trace -> strips ANSI codes & respects tail limit.
	traceW := httptest.NewRecorder()
	svc.ServeHTTP(traceW, httptest.NewRequest(http.MethodGet, BasePath+"/jobs/9002/trace?workspace="+url.QueryEscape(repoWS)+"&tail=2", nil))
	if traceW.Code != http.StatusOK {
		t.Fatalf("trace status=%d body=%s", traceW.Code, traceW.Body.String())
	}
	var traceResp JobTraceResponse
	_ = json.Unmarshal(traceW.Body.Bytes(), &traceResp)
	if !traceResp.Truncated || traceResp.TotalLines != 3 || strings.Contains(traceResp.Trace, "\x1b[") || !strings.Contains(traceResp.Trace, "FAIL: assertion error") {
		t.Fatalf("unexpected trace response: %+v", traceResp)
	}

	// 6. Retry pipeline & retry job.
	retryPW := httptest.NewRecorder()
	svc.ServeHTTP(retryPW, httptest.NewRequest(http.MethodPost, BasePath+"/pipelines/501/retry?workspace="+url.QueryEscape(repoWS), nil))
	if retryPW.Code != http.StatusOK || !retriedPipeline {
		t.Fatalf("retry pipeline failed: %d %s", retryPW.Code, retryPW.Body.String())
	}
	retryJW := httptest.NewRecorder()
	svc.ServeHTTP(retryJW, httptest.NewRequest(http.MethodPost, BasePath+"/jobs/9002/retry?workspace="+url.QueryEscape(repoWS), nil))
	if retryJW.Code != http.StatusOK || !retriedJob {
		t.Fatalf("retry job failed: %d %s", retryJW.Code, retryJW.Body.String())
	}

	// 7. Update instance without changing token keeps existing token intact.
	newName := "内网 GitLab 主站"
	updBody, _ := json.Marshal(UpdateInstanceRequest{Name: &newName})
	updW := httptest.NewRecorder()
	svc.ServeHTTP(updW, httptest.NewRequest(http.MethodPatch, BasePath+"/instances/"+created.Instance.ID, bytes.NewReader(updBody)))
	if updW.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updW.Code, updW.Body.String())
	}
	saved, err := svc.registry.Find(context.Background(), created.Instance.ID)
	if err != nil || saved.Name != newName || saved.Token != "glpat-selfhosted-123456" {
		t.Fatalf("update failed to preserve token: %+v err=%v", saved, err)
	}
}
