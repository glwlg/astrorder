package reviewrelay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

// allowAllBrowser stands in for the browser credential boundary; the
// credential boundary itself is covered by the httpapi regression suite.
type allowAllBrowser struct{}

func (allowAllBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

// TestReviewRelayValidateRoutesWSLConnectionsToWSLTarget proves that a wsl:
// session connection is routed to the resolver's WSL target branch instead of
// being looked up as an SSH connection row, and that the remote workspace path
// is never resolved on the host: with no WSL helper configured the request
// fails explicitly instead of silently probing a local path.
func TestReviewRelayValidateRoutesWSLConnectionsToWSLTarget(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler := NewHandler(Options{
		Store:      st,
		Authorizer: allowAllBrowser{},
		Resolver:   targetfs.NewResolver(targetfs.ResolverOptions{Store: st, WSLBinary: "astrorder-missing-wsl-for-test"}),
	})

	ctx := context.Background()
	repository := t.TempDir()
	for _, seed := range []struct{ agentID, sessionID string }{{"agent-a", "session-1"}, {"agent-b", "session-2"}} {
		if _, err := st.UpsertAgent(ctx, map[string]any{
			"id": seed.agentID, "kind": "codex", "name": seed.agentID, "status": "ready", "connection_id": "wsl:Ubuntu-24.04",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertSession(ctx, map[string]any{
			"id": seed.sessionID, "agent_id": seed.agentID, "status": "idle", "workspace": repository,
			"connection_id": "wsl:Ubuntu-24.04", "updated_at": time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	quoted, err := json.Marshal(repository)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"source_workspace":` + string(quoted) + `,"review_workspace":` + string(quoted) +
		`,"source_agent_id":"agent-a","source_session_id":"session-1","review_agent_id":"agent-b","review_session_id":"session-2"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/review-relay/validate", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code == http.StatusUnprocessableEntity && strings.Contains(response.Body.String(), "SSH 连接不存在") {
		t.Fatalf("wsl connection was rejected as a missing SSH connection: %d %s", response.Code, response.Body.String())
	}
	// The WSL target is attempted (and fails to start the missing binary); it is
	// never resolved through the SSH connection store.
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "SSH") {
		t.Fatalf("wsl validate = %d %s", response.Code, response.Body.String())
	}
}
