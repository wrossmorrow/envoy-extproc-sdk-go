package extproc

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hpb "google.golang.org/grpc/health/grpc_health_v1"
)

type HealthServer struct {
	hpb.UnimplementedHealthServer
	status atomic.Int32
}

func NewHealthServer() *HealthServer {
	return newHealthServer(hpb.HealthCheckResponse_NOT_SERVING)
}

func NewReadyHealthServer() *HealthServer {
	return newHealthServer(hpb.HealthCheckResponse_SERVING)
}

func newHealthServer(s hpb.HealthCheckResponse_ServingStatus) *HealthServer {
	hs := &HealthServer{}
	hs.status.Store(int32(s))
	return hs
}

func (s *HealthServer) set(v hpb.HealthCheckResponse_ServingStatus) {
	s.status.Store(int32(v))
}

func (s *HealthServer) GetStatus() hpb.HealthCheckResponse_ServingStatus {
	return hpb.HealthCheckResponse_ServingStatus(s.status.Load())
}

func (s *HealthServer) MarkReady() {
	s.set(hpb.HealthCheckResponse_SERVING)
}

func (s *HealthServer) MarkUnready() {
	s.set(hpb.HealthCheckResponse_NOT_SERVING)
}

func (s *HealthServer) Check(ctx context.Context, req *hpb.HealthCheckRequest) (*hpb.HealthCheckResponse, error) {
	return &hpb.HealthCheckResponse{Status: s.GetStatus()}, nil
}

func (s *HealthServer) Watch(req *hpb.HealthCheckRequest, srv hpb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
