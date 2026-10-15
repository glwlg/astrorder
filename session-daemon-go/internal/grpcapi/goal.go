package grpcapi

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
)

func (s *runtimeQueryService) SetGoal(ctx context.Context, r *pb.SetGoalRequest) (*pb.CommandReceipt, error) {
	if r == nil || (r.Status == "" && (strings.TrimSpace(r.Objective) == "" || len(r.Objective) > 65536)) || (r.Status != "" && r.Status != "paused" && r.Status != "active") || (r.Status != "" && r.Objective != "") {
		return nil, status.Error(codes.InvalidArgument, "goal objective must contain 1 to 65536 bytes")
	}
	fields := controlFields("session.goal.set", r.Session)
	fields["objective"] = r.Objective
	fields["status"] = r.Status
	return (&sessionControlService{daemon: s.daemon}).execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *runtimeQueryService) ClearGoal(ctx context.Context, r *pb.ClearGoalRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "goal clear request is required")
	}
	return (&sessionControlService{daemon: s.daemon}).execute(ctx, r, r.Session, r.Meta, controlFields("session.goal.clear", r.Session))
}
