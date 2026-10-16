package grpcapi_test

import (
	"reflect"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestGrokListPreservesTypedFiltersWithoutOpeningSession(t *testing.T) {
	f := startFixture(t)
	q := &nativeQueryFixture{}
	f.daemon.RegisterRuntime("grok", q)
	cwd, cursor := "P:/workspace/probe", "opaque==cursor"
	_, err := pb.NewRuntimeQueryClient(f.conn).QueryNative(f.ctx, &pb.NativeQueryRequest{Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK}, Query: &pb.NativeQueryRequest_GrokSessionList{GrokSessionList: &pb.GrokSessionListQuery{Cwd: &cwd, Cursor: &cursor}}})
	if err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.creates != 0 || len(q.requests) != 1 || q.requests[0].Fields["method"] != "session/list" || !reflect.DeepEqual(q.requests[0].Fields["request_params"], map[string]any{"cwd": cwd, "cursor": cursor}) {
		t.Fatalf("Grok filters or discovery semantics changed: %+v", q.requests)
	}
	authority, err := f.daemon.AuthorityStatus()
	if err != nil || len(authority.Sessions) != 0 || authority.PendingOperations != 0 {
		t.Fatalf("discovery opened a conversation: %+v %v", authority, err)
	}
}
