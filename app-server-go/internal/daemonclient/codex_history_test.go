package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type codexHistoryQueryClient struct {
	pb.RuntimeQueryClient
	query func(*pb.NativeQueryRequest) (*pb.NativeQueryResponse, error)
}

func (c codexHistoryQueryClient) QueryNative(_ context.Context, request *pb.NativeQueryRequest, _ ...grpc.CallOption) (*pb.NativeQueryResponse, error) {
	return c.query(request)
}

func TestCodexReadOnlyHistoryPreservesIdentityCursorAndExactNumbers(t *testing.T) {
	target := &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, AgentId: historyString("agent"), ConnectionId: historyString("ssh-exact")}
	client := &Client{Runtime: codexHistoryQueryClient{query: func(request *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
		query := request.GetCodexThreadItemsList()
		if request.Target != target || query.ThreadId != "exact-child" || query.Cursor.GetValue() != "opaque:cursor" || query.GetSortDirection() != "desc" || query.Limit != 25 {
			t.Fatalf("native query changed identity or pagination: %v", request)
		}
		return &pb.NativeQueryResponse{Result: &pb.NativeJson{Utf8Json: []byte(`{"data":[{"id":9007199254740993,"type":"agentMessage","text":"完整原生回复"}],"nextCursor":"next"}`)}}, nil
	}}}
	page, err := client.ReadCodexHistoryPage(context.Background(), target, "exact-child", "opaque:cursor", 25)
	if err != nil || len(page.Data) != 1 || page.Data[0]["id"] != json.Number("9007199254740993") || *page.NextCursor != "next" {
		t.Fatalf("native page corrupted: %+v %v", page, err)
	}
}

func TestCodexReadOnlyHistoryRejectsInvalidPagesWithoutRetrying(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[null]}`, `{"data":[]} {}`, `{"data":[],"nextCursor":""}`, `{"data":[],"nextCursor":"same"}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			client := &Client{Runtime: codexHistoryQueryClient{query: func(*pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
				calls++
				return &pb.NativeQueryResponse{Result: &pb.NativeJson{Utf8Json: []byte(body)}}, nil
			}}}
			if _, err := client.ReadCodexHistoryPage(context.Background(), &pb.RuntimeTarget{}, "child", "same", 1); err == nil || calls != 1 {
				t.Fatalf("invalid page accepted or retried: calls=%d error=%v", calls, err)
			}
		})
	}
	failure := errors.New("native query failed")
	client := &Client{Runtime: codexHistoryQueryClient{query: func(*pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
		return nil, failure
	}}}
	if _, err := client.ReadCodexHistoryPage(context.Background(), &pb.RuntimeTarget{}, "child", "", 1); err != failure {
		t.Fatal("native failure was hidden", err)
	}
}
