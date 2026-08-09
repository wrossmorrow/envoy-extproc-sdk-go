package extproc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "google.golang.org/grpc/health/grpc_health_v1"
)

type HealthServer struct {
	Status pb.HealthCheckResponse_ServingStatus
}

func NewHealthServer() *HealthServer {
	return &HealthServer{Status: pb.HealthCheckResponse_NOT_SERVING}
}

func NewReadyHealthServer() *HealthServer {
	return &HealthServer{Status: pb.HealthCheckResponse_SERVING}
}

func (s *HealthServer) MarkReady() {
	s.Status = pb.HealthCheckResponse_SERVING
}

func (s *HealthServer) MarkUnready() {
	s.Status = pb.HealthCheckResponse_NOT_SERVING
}

func (s *HealthServer) Check(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{Status: pb.HealthCheckResponse_SERVING}, nil
}

func (s *HealthServer) Watch(req *pb.HealthCheckRequest, srv pb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
