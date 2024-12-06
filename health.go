package extproc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "google.golang.org/grpc/health/grpc_health_v1"
)

type HealthServer struct{}

func (s *HealthServer) Check(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{Status: pb.HealthCheckResponse_SERVING}, nil
}

func (s *HealthServer) Watch(req *pb.HealthCheckRequest, srv pb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
