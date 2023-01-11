package main

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "google.golang.org/grpc/health/grpc_health_v1"
)

type healthServer struct{}

func (s *healthServer) Check(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	log.Printf("Handling grpc Check request + %s", req.String())
	return &pb.HealthCheckResponse{Status: pb.HealthCheckResponse_SERVING}, nil
}

func (s *healthServer) Watch(req *pb.HealthCheckRequest, srv pb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
