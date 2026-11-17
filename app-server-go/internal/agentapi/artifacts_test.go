package agentapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

func TestAgentReadsGroupArtifactUsingExistingBlackboardCapability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := os.WriteFile(filepath.Join(root, "output.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "task"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertAgent(ctx, map[string]any{"id": "reader", "name": "设计", "kind": "codex", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{"id": "reader-session", "agent_id": "reader", "workspace": "C:/design", "connection_id": "local", "status": "idle", "updated_at": "2026-11-12T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "blackboard_scope:reader::reader-session", "group:g", true); err != nil {
		t.Fatal(err)
	}
	var directory artifacts.Directory
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		file := artifacts.Reference(artifacts.File{GroupID: "g", AgentID: "origin", SessionID: "origin-session", ConnectionID: "local", Workspace: filepath.Join(root, "task")}, filepath.Join(root, "output.go"))
		file.ContentKey = artifacts.ContentPrefix + file.ID
		if err := artifacts.WriteTx(ctx, tx, "g", artifacts.Directory{Items: []artifacts.File{file}}); err != nil {
			return err
		}
		directory, err = artifacts.ReadTx(ctx, tx, "g")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{ConnectorSecret: "fixture"}, nil), FileResolver: targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: "/wrong-reader-root"})})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"capability":"blackboard.get","input":{"key":"` + directory.Items[0].ContentKey + `","offset":0,"limit":7,"caller":{"agent_id":"reader","session_id":"reader-session","connection_id":"local","profile_name":"default","runtime":"codex"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer fixture")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("existing capability failed: %d %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// HTTP invoke returns the capability result directly.
	value := result["value"].(map[string]any)
	if value["content"] != "package" || value["next_offset"] != float64(7) || result["namespace"] != "group:g" {
		t.Fatalf("content was not served through existing blackboard: %v", result)
	}
	for _, capability := range AllCapabilities {
		if capability.ID == "files.share" {
			t.Fatal("manual registration capability leaked back in")
		}
	}
}
