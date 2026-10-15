package grpcapi

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListBackgroundProcesses reads the owning gateway, never a discovery worker.
func (s *runtimeQueryService) ListBackgroundProcesses(ctx context.Context, request *pb.ListBackgroundProcessesRequest) (*pb.ListBackgroundProcessesResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "session is required")
	}
	payload, _, err := s.readSession(ctx, request, request.Session, "session.processes", nil)
	if err != nil {
		return nil, err
	}
	rows, ok := payload["processes"].([]any)
	if !ok {
		return nil, status.Error(codes.Unavailable, "native process list is absent")
	}
	response := &pb.ListBackgroundProcessesResponse{}
	for _, row := range rows {
		value, err := nativeReadValue(row)
		if err != nil {
			return nil, err
		}
		response.Processes = append(response.Processes, value)
	}
	return response, nil
}
