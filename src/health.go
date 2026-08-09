package extproc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hpb "google.golang.org/grpc/health/grpc_health_v1"
)

type HealthServer struct {
	hpb.UnimplementedHealthServer
	Status hpb.HealthCheckResponse_ServingStatus
}

func NewHealthServer() *HealthServer {
	return &HealthServer{Status: hpb.HealthCheckResponse_NOT_SERVING}
}

func NewReadyHealthServer() *HealthServer {
	return &HealthServer{Status: hpb.HealthCheckResponse_SERVING}
}

func (s *HealthServer) MarkReady() {
	s.Status = hpb.HealthCheckResponse_SERVING
}

func (s *HealthServer) MarkUnready() {
	s.Status = hpb.HealthCheckResponse_NOT_SERVING
}

func (s *HealthServer) Check(ctx context.Context, req *hpb.HealthCheckRequest) (*hpb.HealthCheckResponse, error) {
	return &hpb.HealthCheckResponse{Status: hpb.HealthCheckResponse_SERVING}, nil
}

func (s *HealthServer) Watch(req *hpb.HealthCheckRequest, srv hpb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
