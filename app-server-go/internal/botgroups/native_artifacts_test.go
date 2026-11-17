package botgroups

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestArtifactEvidenceReadsWholeReplyTurnWithoutAttachingOrLeakingOtherTurns(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertAgent(ctx, map[string]any{"id": "dev", "kind": "codex", "connection_id": "local", "name": "dev", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.UpsertSession(ctx, map[string]any{"id": "thread", "agent_id": "dev", "workspace": "/task", "connection_id": "local", "status": "idle", "updated_at": now()})
	if err != nil {
		t.Fatal(err)
	}
	change := func(id, p string) map[string]any {
		return map[string]any{"id": id, "type": "fileChange", "status": "completed", "changes": []any{map[string]any{"path": p, "kind": map[string]any{"type": "add"}}}}
	}
	pages := map[string]map[string]any{
		"": {"data": []any{
			change("future-write", "future.md"),
			map[string]any{"id": "newer-user", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "unrelated"}}},
			map[string]any{"item": map[string]any{"id": "final", "type": "agentMessage", "text": "done", "phase": "final_answer"}},
		}, "nextCursor": "tools:opaque"},
		"tools:opaque": {"data": []any{
			change("patch", "docs/report.md"),
			map[string]any{"id": "image", "type": "commandExecution", "status": "completed", "exitCode": json.Number("0"), "cwd": "/task", "command": `python -c 'plt.savefig("out/chart.png")'`},
			map[string]any{"id": "read", "type": "commandExecution", "status": "completed", "exitCode": json.Number("0"), "command": "cat SKILL.md", "commandActions": []any{map[string]any{"type": "read"}}},
		}, "nextCursor": "boundary"},
		"boundary": {"data": []any{
			map[string]any{"id": "user", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "deliver this task"}}},
			change("previous-write", "previous.md"),
		}, "nextCursor": nil},
	}
	var cursors []string
	peer := &readonlyGroupHistoryClient{}
	peer.query = func(request *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
		query := request.GetCodexThreadItemsList()
		if query == nil || query.ThreadId != "thread" || request.Target.GetAgentId() != "dev" || request.Target.GetConnectionId() != "" {
			t.Fatalf("wrong evidence target: %v", request)
		}
		cursor := query.Cursor.GetValue()
		cursors = append(cursors, cursor)
		return &pb.NativeQueryResponse{Result: native(pages[cursor])}, nil
	}
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Runtime: peer}}
	operations, err := controller.ReadArtifactEvidence(ctx, "dev", "thread", []string{"final"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursors, []string{"", "tools:opaque", "boundary"}) || len(operations) != 2 || operations[0].ID != "patch" || operations[1].ID != "image" || peer.ownedReads != 0 {
		t.Fatalf("incorrect evidence scope: %v %+v owned=%d", cursors, operations, peer.ownedReads)
	}
	after, _ := st.GetSession(ctx, "dev", "thread")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("evidence read mutated native session")
	}
	pages["boundary"]["data"] = []any{change("old", "unscoped.md")}
	if _, err := controller.ReadArtifactEvidence(ctx, "dev", "thread", []string{"final"}); err == nil {
		t.Fatal("missing user boundary accepted")
	}
	if _, err := controller.ReadArtifactEvidence(ctx, "dev", "thread", []string{"missing"}); err == nil {
		t.Fatal("missing final accepted")
	}
}

func TestFailedDeletedAndUncertainNativeOperationsAreNotWriteEvidence(t *testing.T) {
	for _, item := range []map[string]any{
		{"id": "fail", "type": "fileChange", "status": "failed", "changes": []any{map[string]any{"path": "a.md", "kind": map[string]any{"type": "add"}}}},
		{"id": "delete", "type": "fileChange", "status": "completed", "changes": []any{map[string]any{"path": "a.md", "kind": map[string]any{"type": "delete"}}}},
		{"id": "failed-command", "type": "commandExecution", "status": "completed", "exitCode": json.Number("1"), "command": "write a.md"},
		{"id": "unknown-command", "type": "commandExecution", "status": "completed", "command": "write a.md"},
		{"id": "in-progress", "type": "commandExecution", "status": "inProgress", "exitCode": json.Number("0"), "command": "write a.md"},
	} {
		if got := nativeFileOperations(item); len(got) != 0 {
			t.Fatalf("unconfirmed native operation accepted: %+v", got)
		}
	}
	file := artifacts.Reference(artifacts.File{Workspace: "/project"}, "docs/result.md")
	operations := []NativeFileOperation{{ID: "wrong-cwd", Command: "write docs/result.md", Cwd: "/other"}, {ID: "right-cwd", Command: "write docs/result.md", Cwd: "/project"}}
	got := evidenceForFile(file, operations)
	if len(got) != 1 || got[0].ID != "right-cwd" {
		t.Fatalf("relative path used wrong cwd: %+v", got)
	}
}
