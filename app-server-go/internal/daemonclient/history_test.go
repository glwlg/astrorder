package daemonclient

import (
	"context"
	"reflect"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type historyPeer struct {
	pb.RuntimeQueryClient
	read func(context.Context, *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error)
}

func (p historyPeer) GetHistoryPage(ctx context.Context, r *pb.HistoryPageRequest, _ ...grpc.CallOption) (*pb.HistoryPageResponse, error) {
	return p.read(ctx, r)
}

func TestHistoryShrinksOversizedPageWithoutChangingCursorOrRequest(t *testing.T) {
	request := &pb.HistoryPageRequest{Session: &pb.SessionRef{SessionId: "exact", AgentId: historyString("agent"), ConnectionId: historyString("debian")}, Limit: 89, Before: &pb.NullableString{State: &pb.NullableString_Value{Value: "codex:opaque"}}}
	original := proto.Clone(request)
	var limits []uint32
	expected := &pb.HistoryPageResponse{Session: request.Session, Items: []*pb.NativeJson{{Utf8Json: []byte(`{"id":"untouched","content":"full output"}`)}}, NextCursor: &pb.NullableString{State: &pb.NullableString_Value{Value: "codex:next"}}}
	peer := historyPagingClient{RuntimeQueryClient: historyPeer{read: func(ctx context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		limits = append(limits, r.Limit)
		if !proto.Equal(r.Session, request.Session) || !proto.Equal(r.Before, request.Before) {
			t.Fatal("identity/cursor changed")
		}
		if r.Limit > 22 {
			return nil, status.Error(codes.ResourceExhausted, "typed history exceeds business limit")
		}
		return expected, nil
	}}}
	got, err := peer.GetHistoryPage(context.Background(), request)
	if err != nil || got != expected {
		t.Fatalf("page=%v error=%v", got, err)
	}
	if !reflect.DeepEqual(limits, []uint32{89, 44, 22}) {
		t.Fatalf("limits=%v", limits)
	}
	if !proto.Equal(request, original) {
		t.Fatal("caller request mutated")
	}
}
func TestHistoryDoesNotRetryOtherFailuresOrSingleOversizedItem(t *testing.T) {
	for _, failure := range []error{status.Error(codes.Unavailable, "native read failed"), status.Error(codes.FailedPrecondition, "identity mismatch"), status.Error(codes.ResourceExhausted, "quota exceeded"), context.Canceled} {
		calls := 0
		peer := historyPagingClient{RuntimeQueryClient: historyPeer{read: func(context.Context, *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
			calls++
			return nil, failure
		}}}
		_, err := peer.GetHistoryPage(context.Background(), &pb.HistoryPageRequest{Limit: 100})
		if err != failure || calls != 1 {
			t.Fatalf("error=%v calls=%d", err, calls)
		}
	}
	calls := 0
	failure := status.Error(codes.ResourceExhausted, "native session response exceeds business limit")
	peer := historyPagingClient{RuntimeQueryClient: historyPeer{read: func(context.Context, *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		calls++
		return nil, failure
	}}}
	_, err := peer.GetHistoryPage(context.Background(), &pb.HistoryPageRequest{Limit: 1})
	if err != failure || calls != 1 {
		t.Fatalf("single item error=%v calls=%d", err, calls)
	}
}
func historyString(s string) *string { return &s }
