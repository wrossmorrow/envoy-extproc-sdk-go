package main

import (
	"strconv"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// durationHeaderOption builds a header mutation set-headers entry carrying the
// duration header with the given value in nanoseconds.
func durationHeaderOption(ns int64) *corev3.HeaderValueOption {
	return &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      DURATION_HEADER,
			Value:    "",
			RawValue: []byte(strconv.FormatInt(ns, 10)),
		},
	}
}

// commonResponseWithDuration builds a common response whose header mutation
// carries the duration header with the given value in nanoseconds.
func commonResponseWithDuration(ns int64) *extprocv3.CommonResponse {
	return &extprocv3.CommonResponse{
		Status: 0,
		HeaderMutation: &extprocv3.HeaderMutation{
			SetHeaders:    []*corev3.HeaderValueOption{durationHeaderOption(ns)},
			RemoveHeaders: []string{},
		},
	}
}

// processingResponseWith builds a processing response carrying the given kind
// (response_headers or response_body) with the duration header set.
func processingResponseWith(kind string, ns int64) *extprocv3.ProcessingResponse {
	resp := &extprocv3.ProcessingResponse{}
	switch kind {
	case "response_headers":
		resp.Response = &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{
				Response: commonResponseWithDuration(ns),
			},
		}
	case "response_body":
		resp.Response = &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: commonResponseWithDuration(ns),
			},
		}
	default:
		resp.Response = nil
	}
	return resp
}

func TestExtractDurationNsFromResponseHeaders(t *testing.T) {
	resp := processingResponseWith("response_headers", 12_345_678)
	ns, ok := extractDurationNs(resp)
	if !ok {
		t.Fatal("expected a duration for a response_headers response with the duration header set")
	}
	if ns != 12_345_678 {
		t.Errorf("duration = %d, want %d", ns, 12_345_678)
	}
}

func TestExtractDurationNsFromResponseBody(t *testing.T) {
	resp := processingResponseWith("response_body", 1_000_000_000)
	ns, ok := extractDurationNs(resp)
	if !ok {
		t.Fatal("expected a duration for a response_body response with the duration header set")
	}
	if ns != 1_000_000_000 {
		t.Errorf("duration = %d, want %d", ns, 1_000_000_000)
	}
}

func TestExtractDurationNsAbsentWithoutDurationHeader(t *testing.T) {
	// A mutation that carries a different header, but no duration header.
	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					Status: 0,
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: []*corev3.HeaderValueOption{
							{
								Header: &corev3.HeaderValue{
									Key:      "x-extproc-names",
									Value:    "",
									RawValue: []byte("tester"),
								},
							},
						},
						RemoveHeaders: []string{},
					},
				},
			},
		},
	}
	if ns, ok := extractDurationNs(resp); ok {
		t.Errorf("expected no duration, got %d", ns)
	}
}

func TestExtractDurationNsAbsentWithoutMutation(t *testing.T) {
	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{
				Response: nil,
			},
		},
	}
	if ns, ok := extractDurationNs(resp); ok {
		t.Errorf("expected no duration, got %d", ns)
	}
}

func TestPercentileEmpty(t *testing.T) {
	if v, ok := percentile([]time.Duration{}, 0.95); ok {
		t.Errorf("percentile of empty = %v, want false", v)
	}
}

func TestPercentileSingle(t *testing.T) {
	sorted := []time.Duration{time.Millisecond * 3}
	if v, ok := percentile(sorted, 0.95); !ok || v != 3.0 {
		t.Errorf("percentile = %v, %v; want 3.0, true", v, ok)
	}
}

func TestPercentileSorted(t *testing.T) {
	sorted := []time.Duration{
		time.Millisecond,
		time.Millisecond * 2,
		time.Millisecond * 3,
		time.Millisecond * 4,
	}
	// Nearest-rank percentile: index = (len - 1) * percentile, rounded.
	if v, ok := percentile(sorted, 0.50); !ok || v != 3.0 {
		t.Errorf("p50 = %v, %v; want 3.0, true", v, ok)
	}
	if v, ok := percentile(sorted, 0.95); !ok || v != 4.0 {
		t.Errorf("p95 = %v, %v; want 4.0, true", v, ok)
	}
}

func TestFormatDurationMs(t *testing.T) {
	if v := formatDurationMs(time.Millisecond * 2); v != 2.0 {
		t.Errorf("formatDurationMs = %v, want 2.0", v)
	}
	if v := formatDurationMs(time.Second); v != 1000.0 {
		t.Errorf("formatDurationMs = %v, want 1000.0", v)
	}
}
