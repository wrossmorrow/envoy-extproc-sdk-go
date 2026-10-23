package extproc

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	hpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestHealthServerNotReadyByDefault(t *testing.T) {
	hs := NewHealthServer()
	if got := hs.GetStatus(); got != hpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("status = %v, want %v", got, hpb.HealthCheckResponse_NOT_SERVING)
	}
}

func TestHealthServerReadyByDefault(t *testing.T) {
	hs := NewReadyHealthServer()
	if got := hs.GetStatus(); got != hpb.HealthCheckResponse_SERVING {
		t.Errorf("status = %v, want %v", got, hpb.HealthCheckResponse_SERVING)
	}
}

func TestHealthServerCheck(t *testing.T) {
	ctx := context.Background()
	req := &hpb.HealthCheckRequest{}

	hs := NewHealthServer()
	resp, err := hs.Check(ctx, req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got := resp.GetStatus(); got != hpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("status = %v, want %v", got, hpb.HealthCheckResponse_NOT_SERVING)
	}

	hs.MarkReady()
	if got := hs.GetStatus(); got != hpb.HealthCheckResponse_SERVING {
		t.Errorf("status after MarkReady = %v, want %v", got, hpb.HealthCheckResponse_SERVING)
	}
	resp, err = hs.Check(ctx, req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got := resp.GetStatus(); got != hpb.HealthCheckResponse_SERVING {
		t.Errorf("status = %v, want %v", got, hpb.HealthCheckResponse_SERVING)
	}

	hs.MarkUnready()
	if got := hs.GetStatus(); got != hpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("status after MarkUnready = %v, want %v", got, hpb.HealthCheckResponse_NOT_SERVING)
	}
	resp, err = hs.Check(ctx, req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got := resp.GetStatus(); got != hpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("status = %v, want %v", got, hpb.HealthCheckResponse_NOT_SERVING)
	}
}

func TestHealthServerWatchIsUnimplemented(t *testing.T) {
	hs := NewHealthServer()
	if err := hs.Watch(&hpb.HealthCheckRequest{}, nil); err == nil {
		t.Fatal("expected Watch to return an Unimplemented error")
	} else if status.Code(err) != codes.Unimplemented {
		t.Errorf("Watch error code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
}
