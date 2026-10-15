package sessions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/files"
	"astrorder.dev/app-server/internal/targetfs"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type allowImageBrowser struct{}

func (allowImageBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func TestCodexImagesAcrossNativeHistoryPaths(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(map[bool]string{true: "bound", false: "readonly-paginated"}[bound], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/ZfoAAAAASUVORK5CYII=")
			if err != nil {
				t.Fatal(err)
			}
			imagePath := filepath.Join(root, "截图 #1.png")
			if err = os.WriteFile(imagePath, png, 0600); err != nil {
				t.Fatal(err)
			}
			native := map[string]any{"id": "native-image", "type": "userMessage", "content": []any{map[string]any{"type": "localImage", "path": imagePath}, map[string]any{"type": "image", "url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}}}
			var h *Handler
			var fileHandler http.Handler
			sid, aid := "image-thread", "codex-1"
			if bound {
				raw, _ := json.Marshal(native)
				handler, st, _, cleanup := setupTestServer(t, &mockDaemonServer{getHistoryPageCb: func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
					return &pb.HistoryPageResponse{Session: r.Session, Items: []*pb.NativeJson{{Schema: "codex.item.v1", Utf8Json: raw}}}, nil
				}})
				defer cleanup()
				h = handler
				if _, err = st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
					t.Fatal(err)
				}
			} else {
				handler, _, srv, _ := newNativeFixture(t)
				h = handler
				// First page has two later items; the image is on the second page.
				srv.items = []map[string]any{codexItem("agentMessage", "later-2", "later"), codexItem("agentMessage", "later-1", "later"), native}
			}
			if _, err = h.store.UpsertSession(ctx, map[string]any{"id": sid, "agent_id": aid, "workspace": root, "connection_id": "local", "status": "idle", "history_state": "available", "control_state": map[bool]string{true: "owned", false: "native"}[bound], "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			fileHandler = files.NewHandler(files.Options{Authorizer: allowImageBrowser{}, Resolver: targetfs.NewResolver(targetfs.ResolverOptions{Store: h.store, DefaultWorkspace: root, AllowedRoots: []string{root}})})
			before := ""
			var images []map[string]any
			for page := 0; page < 2; page++ {
				request := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/messages?agent_id="+aid+"&limit=2&before="+url.QueryEscape(before), nil)
				response := httptest.NewRecorder()
				h.handleListMessages(response, request, sid)
				if response.Code != 200 {
					t.Fatalf("history: %d %s", response.Code, response.Body.String())
				}
				var body struct {
					Items []struct {
						Attachments []map[string]any `json:"attachments"`
					} `json:"items"`
					Next *string `json:"next_cursor"`
				}
				if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				for _, item := range body.Items {
					images = append(images, item.Attachments...)
				}
				if body.Next == nil {
					break
				}
				before = *body.Next
			}
			if len(images) != 2 {
				t.Fatalf("images missing: %+v", images)
			}
			response := httptest.NewRecorder()
			fileHandler.ServeHTTP(response, httptest.NewRequest("GET", images[0]["url"].(string), nil))
			if response.Code != 200 || response.Body.String() != string(png) || response.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("image bytes: %d %v", response.Code, response.Header())
			}
			outside := filepath.Join(t.TempDir(), "outside.png")
			if err = os.WriteFile(outside, png, 0600); err != nil {
				t.Fatal(err)
			}
			response = httptest.NewRecorder()
			fileHandler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/files/raw?session_id="+sid+"&path="+url.QueryEscape(outside), nil))
			if response.Code != 403 {
				t.Fatalf("scope escape: %d", response.Code)
			}
			rows, err := h.store.QueryRows(ctx, "SELECT * FROM messages WHERE agent_id=? AND session_id=?", aid, sid)
			if err != nil || len(rows) != 0 {
				t.Fatalf("native history persisted: %d %v", len(rows), err)
			}
		})
	}
}
