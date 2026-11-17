package blackboard

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type artifactResolver struct {
	root  string
	calls []targetfs.ResolveRequest
	fail  error
}

func (r *artifactResolver) Resolve(ctx context.Context, request targetfs.ResolveRequest) (targetfs.Resolved, error) {
	r.calls = append(r.calls, request)
	if r.fail != nil {
		return targetfs.Resolved{}, r.fail
	}
	filesystem, err := targetfs.NewRemote(targetfs.RemoteOptions{Workspace: r.root, AllowedRoots: []string{r.root}, Command: []string{os.Args[0], "-test.run=TestArtifactContentHelperProcess", "--"}, Environment: []string{"ASTRORDER_ARTIFACT_TEST_HELPER=1"}})
	return targetfs.Resolved{FileSystem: filesystem, Workspace: r.root, ConnectionID: request.ConnectionID, Kind: "ssh"}, err
}
func TestArtifactContentHelperProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_ARTIFACT_TEST_HELPER") != "1" {
		return
	}
	if err := targetfs.ServeHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestBlackboardReadsActualArtifactContentFromPinnedRemoteSource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	text := []byte("第一行\npackage main\n")
	image := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 1, 255}
	for name, data := range map[string][]byte{"result.go": text, "image.png": image} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := artifacts.File{GroupID: "group-test", MemberName: "开发", AgentID: "remote", SessionID: "original-session", ConnectionID: "ssh-original", Workspace: root}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		files := []artifacts.File{}
		for _, name := range []string{"result.go", "image.png"} {
			file := artifacts.Reference(source, name)
			file.ContentKey = artifacts.ContentPrefix + file.ID
			files = append(files, file)
		}
		return artifacts.WriteTx(ctx, tx, source.GroupID, artifacts.Directory{Items: files})
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreference(ctx, "blackboard_scope:windows::reader", "group:group-test", true)
	resolver := &artifactResolver{root: root}
	service := NewService(st, nil).WithFileResolver(resolver)
	var directory artifacts.Directory
	err = st.WithTx(ctx, func(tx *sql.Tx) error { directory, err = artifacts.ReadTx(ctx, tx, source.GroupID); return err })
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range [][]byte{text, image} {
		response, err := service.Get(ctx, map[string]any{"caller": map[string]any{"agent_id": "windows", "session_id": "reader"}, "key": directory.Items[i].ContentKey})
		if err != nil {
			t.Fatal(err)
		}
		value := response["value"].(map[string]any)
		data := []byte(value["content"].(string))
		if i == 1 {
			if value["encoding"] != "base64" {
				t.Fatal("image was not base64 encoded")
			}
			data, err = base64.StdEncoding.DecodeString(string(data))
			if err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(data, expected) {
			t.Fatalf("artifact %d content=%q", i, data)
		}
	}
	for _, call := range resolver.calls {
		if call.ConnectionID != "ssh-original" || call.Workspace != root || call.SessionID != "" {
			t.Fatalf("read followed the reader instead of the source: %+v", call)
		}
	}
	key := directory.Items[0].ContentKey
	page, err := service.Get(ctx, map[string]any{"group_id": source.GroupID, "key": key, "offset": json.Number("0"), "limit": json.Number("10")})
	if err != nil {
		t.Fatal(err)
	}
	value := page["value"].(map[string]any)
	if value["truncated"] != true || value["next_offset"] != int64(10) {
		t.Fatalf("pagination=%v", value)
	}
	page, err = service.Get(ctx, map[string]any{"group_id": source.GroupID, "key": key, "offset": 10, "limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	if page["value"].(map[string]any)["content"] != string(text[10:]) {
		t.Fatal("artifact continuation lost bytes")
	}
	resolver.fail = errors.New("original server offline")
	if _, err = service.Get(ctx, map[string]any{"group_id": source.GroupID, "key": key}); err == nil {
		t.Fatal("missing source was silently read locally")
	}
}

func TestArtifactDirectoryIsAutomaticAndSurvivesOrdinaryBlackboardCleanup(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := NewService(st, nil)
	if _, err := service.Set(ctx, map[string]any{"group_id": "g", "key": "artifacts", "value": "overwrite"}); err == nil {
		t.Fatal("platform directory overwrite accepted")
	}
	if err := st.SetPreference(ctx, artifacts.DatabaseKey("g"), artifacts.Directory{Items: []artifacts.File{}}, true); err != nil {
		t.Fatal(err)
	}
	_, _ = service.Set(ctx, map[string]any{"group_id": "g", "key": "other", "value": "ordinary"})
	if _, err := service.Delete(ctx, map[string]any{"group_id": "g", "key": "artifacts"}); err == nil {
		t.Fatal("directory deletion accepted")
	}
	if _, err := service.Delete(ctx, map[string]any{"group_id": "g", "key": "*"}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Get(ctx, map[string]any{"group_id": "g", "key": "artifacts"})
	if err != nil || result["exists"] != true {
		t.Fatalf("directory lost: %v %v", result, err)
	}
	result, err = service.Get(ctx, map[string]any{"group_id": "g", "key": "other"})
	if err != nil || result["exists"] != false {
		t.Fatal("ordinary entries not cleaned")
	}
}
