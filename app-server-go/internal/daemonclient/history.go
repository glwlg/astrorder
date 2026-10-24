package daemonclient

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type historyPagingClient struct{ pb.RuntimeQueryClient }

// History reads are side-effect free. Reduce only a page rejected for payload
// size, keeping its exact identity and cursor so native continuation owns the
// boundary. A single oversized item and unrelated failures remain explicit.
func (c historyPagingClient) GetHistoryPage(ctx context.Context, request *pb.HistoryPageRequest, opts ...grpc.CallOption) (*pb.HistoryPageResponse, error) {
	current := request
	for {
		page, err := c.RuntimeQueryClient.GetHistoryPage(ctx, current, opts...)
		if err == nil || current == nil || current.Limit <= 1 || ctx.Err() != nil || !historySizeError(err) {
			return page, err
		}
		next := proto.Clone(current).(*pb.HistoryPageRequest)
		next.Limit /= 2
		current = next
	}
}
func historySizeError(err error) bool {
	if status.Code(err) != codes.ResourceExhausted {
		return false
	}
	switch status.Convert(err).Message() {
	case "typed history exceeds business limit", "native session response exceeds business limit":
		return true
	default:
		return false
	}
}
