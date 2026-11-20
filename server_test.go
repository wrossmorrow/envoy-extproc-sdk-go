package extproc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproccfgv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func newTestServer(t *testing.T, processor RequestProcessor, options *ProcessingOptions) *GenericExtProcServer {
	t.Helper()
	return &GenericExtProcServer{
		name:      processor.GetName(),
		processor: processor,
		options:   options,
		metrics:   NewEmptyMetrics(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestServeRejectsNilProcessor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := Serve(&ServerOptions{}, nil, logger)
	if err == nil {
		t.Fatal("expected Serve to reject a nil processor")
	}
	if err.Error() != "processor is nil" {
		t.Errorf("Serve error = %v, want %q", err, "processor is nil")
	}
}

func TestNewGenericExtProcServer(t *testing.T) {
	t.Run("nil processor rejected", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		srv, err := NewGenericExtProcServer(nil, NewEmptyMetrics(), logger)
		if err == nil {
			t.Fatal("expected NewGenericExtProcServer to reject nil processor")
		}
		if err.Error() != "processor is nil" {
			t.Errorf("error = %v, want %q", err, "processor is nil")
		}
		if srv != nil {
			t.Error("expected nil server on error")
		}
	})

	t.Run("nil metrics, logger, and options default cleanly", func(t *testing.T) {
		proc := newRecordingProcessor("defaults", nil)
		srv, err := NewGenericExtProcServer(proc, nil, nil)
		if err != nil {
			t.Fatalf("NewGenericExtProcServer returned error: %v", err)
		}
		if srv.metrics == nil {
			t.Fatal("expected default metrics to be initialized")
		}
		if srv.logger == nil {
			t.Fatal("expected default logger to be initialized")
		}
		if srv.options == nil {
			t.Fatal("expected default processing options to be initialized")
		}
		if srv.name != "defaults" {
			t.Errorf("server name = %q, want %q", srv.name, "defaults")
		}

		rc := newRequestContext()
		if rerr := rc.ResetPhase(); rerr != nil {
			t.Fatal(rerr)
		}
		resp, _, perr := srv.processPhase(&extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
						{Key: ":method", RawValue: []byte("GET")},
					}},
				},
			},
		}, proc, rc)
		if perr != nil {
			t.Fatalf("processPhase returned error: %v", perr)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
	})

	t.Run("non-nil arguments preserved", func(t *testing.T) {
		opts := &ProcessingOptions{UpdateDurationHeader: true}
		proc := newRecordingProcessor("custom", opts)
		metrics := NewEmptyMetrics()
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		srv, err := NewGenericExtProcServer(proc, metrics, logger)
		if err != nil {
			t.Fatalf("NewGenericExtProcServer returned error: %v", err)
		}
		if srv.metrics != metrics {
			t.Error("expected custom metrics instance to be preserved")
		}
		if srv.logger != logger {
			t.Error("expected custom logger instance to be preserved")
		}
		if srv.options != opts {
			t.Error("expected custom options instance to be preserved")
		}
	})
}

type recordingProcessor struct {
	name string
	opts *ProcessingOptions

	phases []int
	bodies map[string][]byte

	// errorHandlerCalls counts ErrorHandler invocations; a graceful stream
	// cancellation must not reach it, a processing failure must
	errorHandlerCalls int
	lastError         error
}

func newRecordingProcessor(name string, opts *ProcessingOptions) *recordingProcessor {
	return &recordingProcessor{
		name:   name,
		opts:   opts,
		bodies: map[string][]byte{},
	}
}

func (p *recordingProcessor) GetName() string                { return p.name }
func (p *recordingProcessor) GetOptions() *ProcessingOptions { return p.opts }

func (p *recordingProcessor) ProcessRequestHeaders(ctx *RequestContext, headers AllHeaders) error {
	p.phases = append(p.phases, REQUEST_PHASE_REQUEST_HEADERS)
	return ctx.ContinueRequest()
}

func (p *recordingProcessor) ProcessRequestTrailers(ctx *RequestContext, trailers AllHeaders) error {
	p.phases = append(p.phases, REQUEST_PHASE_REQUEST_TRAILERS)
	return ctx.ContinueRequest()
}

func (p *recordingProcessor) ProcessRequestBody(ctx *RequestContext, body []byte) error {
	p.phases = append(p.phases, REQUEST_PHASE_REQUEST_BODY)
	p.bodies["request"] = body
	return ctx.ContinueRequest()
}

func (p *recordingProcessor) ProcessResponseHeaders(ctx *RequestContext, headers AllHeaders) error {
	p.phases = append(p.phases, REQUEST_PHASE_RESPONSE_HEADERS)
	return ctx.ContinueRequest()
}

func (p *recordingProcessor) ProcessResponseTrailers(ctx *RequestContext, trailers AllHeaders) error {
	p.phases = append(p.phases, REQUEST_PHASE_RESPONSE_TRAILERS)
	return ctx.ContinueRequest()
}

func (p *recordingProcessor) ProcessResponseBody(ctx *RequestContext, body []byte) error {
	p.phases = append(p.phases, REQUEST_PHASE_RESPONSE_BODY)
	p.bodies["response"] = body
	return ctx.ContinueRequest()
}

func (p *recordingProcessor) ErrorHandler(ctx *RequestContext, phase int, err error) {
	p.phases = append(p.phases, phase)
	p.errorHandlerCalls++
	p.lastError = err
}

func (p *recordingProcessor) Close(gracePeriodSeconds int32) error { return nil }

func TestProcessPhaseRequestHeaders(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
					{Key: ":method", RawValue: []byte("POST")},
				}},
				EndOfStream: false,
			},
		},
	}, proc, rc)

	if err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}
	if verr := resp.ValidateAll(); verr != nil {
		t.Errorf("resp.ValidateAll() = %v, want nil", verr)
	}
	if phase != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_HEADERS)
	}
	if resp.GetRequestHeaders() == nil {
		t.Fatal("expected a ProcessingResponse_RequestHeaders")
	}
	if rc.Method != "POST" {
		t.Errorf("rc.Method = %q, want %q", rc.Method, "POST")
	}
	if rc.EndOfStream {
		t.Error("EndOfStream should default to false for request headers without end_of_stream")
	}
	if len(proc.phases) != 1 || proc.phases[0] != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("processor phases = %v, want [%d]", proc.phases, REQUEST_PHASE_REQUEST_HEADERS)
	}
}

func TestProcessPhaseRequestBody(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestBody{
			RequestBody: &extprocv3.HttpBody{
				Body:        []byte(`{"hello":"world"}`),
				EndOfStream: true,
			},
		},
	}, proc, rc)

	if err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}
	if verr := resp.ValidateAll(); verr != nil {
		t.Errorf("resp.ValidateAll() = %v, want nil", verr)
	}
	if phase != REQUEST_PHASE_REQUEST_BODY {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_BODY)
	}
	if resp.GetRequestBody() == nil {
		t.Fatal("expected a ProcessingResponse_RequestBody")
	}
	if rc.EndOfStream != true {
		t.Errorf("rc.EndOfStream = %v, want true", rc.EndOfStream)
	}
	if rc.GetPhase() != REQUEST_PHASE_REQUEST_BODY {
		t.Errorf("context phase = %d, want %d", rc.GetPhase(), REQUEST_PHASE_REQUEST_BODY)
	}
	if string(proc.bodies["request"]) != `{"hello":"world"}` {
		t.Errorf("processor body = %q, want %q", proc.bodies["request"], `{"hello":"world"}`)
	}
}

func TestProcessPhaseResponseHeadersUpdatesContext(t *testing.T) {
	proc := newRecordingProcessor("myproc", &ProcessingOptions{
		UpdateExtProcHeader:  true,
		UpdateDurationHeader: true,
	})
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
					{Key: "x-foo", RawValue: []byte("bar")},
				}},
				EndOfStream: true,
			},
		},
	}, proc, rc)

	if err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}
	if verr := resp.ValidateAll(); verr != nil {
		t.Errorf("resp.ValidateAll() = %v, want nil", verr)
	}
	if phase != REQUEST_PHASE_RESPONSE_HEADERS {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_RESPONSE_HEADERS)
	}
	if resp.GetResponseHeaders() == nil {
		t.Fatal("expected a ProcessingResponse_ResponseHeaders")
	}
	if rc.EndOfStream != true {
		t.Errorf("rc.EndOfStream = %v, want true", rc.EndOfStream)
	}

	// the duration header is appended once the response stream is at its end
	if len(rc.response.headerMutation.SetHeaders) != 2 {
		t.Fatalf("SetHeaders = %d entries, want 2 (extproc name + duration)", len(rc.response.headerMutation.SetHeaders))
	}
	if got := rc.response.headerMutation.SetHeaders[0].GetHeader().GetKey(); got != "x-extproc-names" {
		t.Errorf("first mutation header = %q, want %q", got, "x-extproc-names")
	}
	if got := rc.response.headerMutation.SetHeaders[0].GetHeader().GetRawValue(); string(got) != "myproc" {
		t.Errorf("x-extproc-names value = %q, want %q", string(got), "myproc")
	}
	if got := rc.response.headerMutation.SetHeaders[1].GetHeader().GetKey(); got != "x-extproc-duration-ns" {
		t.Errorf("second mutation header = %q, want %q", got, "x-extproc-duration-ns")
	}
	durStr := string(rc.response.headerMutation.SetHeaders[1].GetHeader().GetRawValue())
	if dur, err := strconv.ParseInt(durStr, 10, 64); err != nil || dur <= 0 {
		t.Errorf("x-extproc-duration-ns = %q (err %v), want a positive integer duration", durStr, err)
	}
}

func TestProcessPhaseResponseBodyDoesNotAddExtprocName(t *testing.T) {
	proc := newRecordingProcessor("myproc", &ProcessingOptions{
		UpdateExtProcHeader:  true,
		UpdateDurationHeader: true,
	})
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{
				Body:        []byte("hello"),
				EndOfStream: true,
			},
		},
	}, proc, rc)

	if err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}
	if verr := resp.ValidateAll(); verr != nil {
		t.Errorf("resp.ValidateAll() = %v, want nil", verr)
	}
	if phase != REQUEST_PHASE_RESPONSE_BODY {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_RESPONSE_BODY)
	}
	if resp.GetResponseBody() == nil {
		t.Fatal("expected a ProcessingResponse_ResponseBody")
	}
	if rc.EndOfStream != true {
		t.Errorf("rc.EndOfStream = %v, want true", rc.EndOfStream)
	}

	// UpdateExtProcHeader is only consulted during the response headers phase
	if len(rc.response.headerMutation.SetHeaders) != 1 {
		t.Fatalf("SetHeaders = %d entries, want 1 (only the duration header)", len(rc.response.headerMutation.SetHeaders))
	}
	if got := rc.response.headerMutation.SetHeaders[0].GetHeader().GetKey(); got != "x-extproc-duration-ns" {
		t.Errorf("mutation header = %q, want %q", got, "x-extproc-duration-ns")
	}
}

func TestProcessPhaseResponseTrailers(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseTrailers{
			ResponseTrailers: &extprocv3.HttpTrailers{
				Trailers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
					{Key: "x-trailer", RawValue: []byte("yes")},
				}},
			},
		},
	}, proc, rc)

	if err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}
	if verr := resp.ValidateAll(); verr != nil {
		t.Errorf("resp.ValidateAll() = %v, want nil", verr)
	}
	if phase != REQUEST_PHASE_RESPONSE_TRAILERS {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_RESPONSE_TRAILERS)
	}
	if resp.GetResponseTrailers() == nil {
		t.Fatal("expected a ProcessingResponse_ResponseTrailers")
	}
	if resp.GetResponseTrailers().GetHeaderMutation() == nil {
		t.Fatal("expected the trailers response to carry a HeaderMutation")
	}
}

func TestProcessPhaseProcessorErrorPropagates(t *testing.T) {
	proc := &erroringProcessor{name: "test", opts: NewDefaultProcessingOptions()}
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
					{Key: ":method", RawValue: []byte("GET")},
				}},
			},
		},
	}, proc, rc)

	if err == nil {
		t.Fatal("expected an error from the processor to propagate out of processPhase")
	}
	if resp != nil {
		t.Error("expected no response when the processor fails")
	}
	if phase != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_HEADERS)
	}
}

func TestProcessPhaseInitReqCtxErrorInvokesErrorHandler(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
					{Key: "x-bad", Value: "a", RawValue: []byte("b")},
				}},
			},
		},
	}, proc, rc)

	if err == nil {
		t.Fatal("expected initReqCtx error to propagate out of processPhase")
	}
	if resp != nil {
		t.Error("expected no response when the request context cannot be initialized")
	}
	if phase != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_HEADERS)
	}
	if len(proc.phases) == 0 || proc.phases[len(proc.phases)-1] != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("expected ErrorHandler to be called with the phase, got %v", proc.phases)
	}
}

func TestProcessPhaseUnknownRequestType(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{}, proc, rc)
	if err == nil {
		t.Fatal("expected an error for a request with no phase message")
	}
	if resp != nil {
		t.Error("expected no response for an unknown request type")
	}
	if phase != REQUEST_PHASE_UNDETERMINED {
		t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_UNDETERMINED)
	}
}

func TestProcessPhaseMetricsIncrement(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{
				Body:        []byte("abc"),
				EndOfStream: true,
			},
		},
	}, proc, rc); err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}

	registry := prometheus.NewPedanticRegistry()
	s.metrics.Register(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics failed: %v", err)
	}
	bodyBytes := findMetricFamily(t, families, "extproc_response_body_bytes_total")
	if got := bodyBytes.GetMetric()[0].GetCounter().GetValue(); got != 3 {
		t.Errorf("extproc_response_body_bytes_total = %v, want 3", got)
	}
}

func TestProcessPhaseMetricsErrorPath(t *testing.T) {
	proc := &erroringProcessor{name: "test", opts: NewDefaultProcessingOptions()}
	s := newTestServer(t, proc, proc.opts)
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{Body: []byte("abc")},
		},
	}, proc, rc); err == nil {
		t.Fatal("expected processPhase to propagate the processor error")
	}

	registry := prometheus.NewPedanticRegistry()
	s.metrics.Register(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics failed: %v", err)
	}
	if f := findMetricFamily(t, families, "extproc_phase_errors_total"); len(f.GetMetric()) != 1 {
		t.Errorf("expected one phase error metric, got %v", f.GetMetric())
	}
}

func TestProcessStreamExternalProcessingDurationObserved(t *testing.T) {
	// end-to-end over the fake stream: once the inbound stream is exhausted, the
	// deferred stream cleanup observes both the stream duration and the external
	// processing duration histograms
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)

	stream := newProcessStreamFake(context.Background(),
		newHeadersRequest("request", true, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
	)
	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v, want nil", err)
	}
	for i, sentResp := range stream.sent {
		if verr := sentResp.ValidateAll(); verr != nil {
			t.Errorf("stream.sent[%d].ValidateAll() = %v, want nil", i, verr)
		}
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	family := findMetricFamily(t, families, "extproc_external_processing_seconds")
	if len(family.GetMetric()) == 0 {
		t.Fatalf("no collected metrics for %q", "extproc_external_processing_seconds")
	}
	if got := family.GetMetric()[0].GetHistogram().GetSampleSum(); got <= 0 {
		t.Errorf("extproc_external_processing_seconds sample_sum = %v, want > 0", got)
	}
}

// clearRouteProcessor is a minimal external processor that clears the route
// cache, as an upstream-changing processor does when it rewrites a routed header
// such as `:authority`.
type clearRouteProcessor struct {
	name  string
	opts  *ProcessingOptions
	clear bool
}

func (p *clearRouteProcessor) GetName() string                { return p.name }
func (p *clearRouteProcessor) GetOptions() *ProcessingOptions { return p.opts }

func (p *clearRouteProcessor) ProcessRequestHeaders(ctx *RequestContext, headers AllHeaders) error {
	if p.clear {
		ctx.ClearRouteCache()
	}
	return ctx.ContinueRequest()
}

func (p *clearRouteProcessor) ProcessRequestTrailers(ctx *RequestContext, trailers AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *clearRouteProcessor) ProcessRequestBody(ctx *RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}
func (p *clearRouteProcessor) ProcessResponseHeaders(ctx *RequestContext, headers AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *clearRouteProcessor) ProcessResponseBody(ctx *RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}
func (p *clearRouteProcessor) ProcessResponseTrailers(ctx *RequestContext, trailers AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *clearRouteProcessor) ErrorHandler(ctx *RequestContext, phase int, err error) {}
func (p *clearRouteProcessor) Close(gracePeriodSeconds int32) error                   { return nil }

func TestProcessPhaseClearRouteCachePropagatesToEnvoyResponse(t *testing.T) {
	t.Run("processor clears route cache", func(t *testing.T) {
		proc := &clearRouteProcessor{name: "router", opts: NewDefaultProcessingOptions(), clear: true}
		s := newTestServer(t, proc, proc.opts)
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
						{Key: ":method", RawValue: []byte("GET")},
					}},
				},
			},
		}, proc, rc)
		if err != nil {
			t.Fatalf("processPhase returned error: %v", err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if phase != REQUEST_PHASE_REQUEST_HEADERS {
			t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_HEADERS)
		}

		cr := resp.GetRequestHeaders().GetResponse()
		if cr == nil {
			t.Fatal("expected a CommonResponse in the request headers response")
		}
		if !cr.GetClearRouteCache() {
			t.Error("expected ClearRouteCache to be set in the envoy-facing response when the processor clears it")
		}
	})

	t.Run("processor does not clear route cache", func(t *testing.T) {
		proc := &clearRouteProcessor{name: "router", opts: NewDefaultProcessingOptions(), clear: false}
		s := newTestServer(t, proc, proc.opts)
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		resp, phase, err := s.processPhase(&extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
						{Key: ":method", RawValue: []byte("GET")},
					}},
				},
			},
		}, proc, rc)
		if err != nil {
			t.Fatalf("processPhase returned error: %v", err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if phase != REQUEST_PHASE_REQUEST_HEADERS {
			t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_HEADERS)
		}

		cr := resp.GetRequestHeaders().GetResponse()
		if cr == nil {
			t.Fatal("expected a CommonResponse in the request headers response")
		}
		if cr.GetClearRouteCache() {
			t.Error("expected ClearRouteCache to default to false")
		}
	})
}

type erroringProcessor struct {
	name string
	opts *ProcessingOptions
}

func (p *erroringProcessor) GetName() string                { return p.name }
func (p *erroringProcessor) GetOptions() *ProcessingOptions { return p.opts }

func (p *erroringProcessor) ProcessRequestHeaders(ctx *RequestContext, headers AllHeaders) error {
	return errors.New("processor exploded")
}
func (p *erroringProcessor) ProcessRequestTrailers(ctx *RequestContext, trailers AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *erroringProcessor) ProcessRequestBody(ctx *RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}
func (p *erroringProcessor) ProcessResponseHeaders(ctx *RequestContext, headers AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *erroringProcessor) ProcessResponseTrailers(ctx *RequestContext, trailers AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *erroringProcessor) ProcessResponseBody(ctx *RequestContext, body []byte) error {
	return errors.New("processor exploded")
}
func (p *erroringProcessor) ErrorHandler(ctx *RequestContext, phase int, err error) {}
func (p *erroringProcessor) Close(gracePeriodSeconds int32) error                   { return nil }

// processStreamFake is a scripted grpc.ServerStream for exercising
// GenericExtProcServer.Process end to end. It hands out queued inbound messages
// via RecvMsg and fails (or succeeds) sends via SendMsg, so tests can drive both
// the happy path and mid-stream failures such as context cancellation.
type processStreamFake struct {
	ctx context.Context

	// inbound requests, consumed in order; once exhausted RecvMsg returns io.EOF
	requests []*extprocv3.ProcessingRequest
	recvIdx  int

	// when set, the next RecvMsg returns this error instead of a queued message
	recvErr error

	// when set, sends fail with these errors in order; sends past the queue
	// succeed
	sendErrs []error

	// recorded outbound responses
	sent []*extprocv3.ProcessingResponse
}

func newProcessStreamFake(ctx context.Context, requests ...*extprocv3.ProcessingRequest) *processStreamFake {
	f := &processStreamFake{ctx: ctx}
	f.requests = append(f.requests, requests...)
	return f
}

func (f *processStreamFake) Context() context.Context { return f.ctx }

func (f *processStreamFake) Send(m *extprocv3.ProcessingResponse) error {
	return f.SendMsg(m)
}

func (f *processStreamFake) Recv() (*extprocv3.ProcessingRequest, error) {
	if f.recvErr != nil {
		err := f.recvErr
		f.recvErr = nil
		return nil, err
	}
	if f.recvIdx >= len(f.requests) {
		return nil, io.EOF
	}
	req := f.requests[f.recvIdx]
	f.requests[f.recvIdx] = nil
	f.recvIdx++
	return req, nil
}

func (f *processStreamFake) SendMsg(m any) error {
	preq, ok := m.(*extprocv3.ProcessingResponse)
	if !ok {
		return status.Errorf(codes.Internal, "unexpected send type %T", m)
	}
	f.sent = append(f.sent, preq)
	if len(f.sendErrs) > 0 {
		err := f.sendErrs[0]
		f.sendErrs = f.sendErrs[1:]
		return err
	}
	return nil
}

func (f *processStreamFake) RecvMsg(m any) error {
	// Recv is the entry point Process uses and pulls from the queue directly;
	// the inbound messages are treated as read-only by the SDK, so handing over
	// the queued pointers is safe and avoids copying protobuf messages, which
	// embed lock values
	_, err := f.Recv()
	return err
}

func (f *processStreamFake) SetHeader(md metadata.MD) error  { return nil }
func (f *processStreamFake) SendHeader(md metadata.MD) error { return nil }
func (f *processStreamFake) SetTrailer(md metadata.MD)       {}

func newHeadersRequest(kind string, endOfStream bool, headers ...*corev3.HeaderValue) *extprocv3.ProcessingRequest {
	switch kind {
	case "request":
		return &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers:     &corev3.HeaderMap{Headers: headers},
					EndOfStream: endOfStream,
				},
			},
		}
	case "response":
		return &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_ResponseHeaders{
				ResponseHeaders: &extprocv3.HttpHeaders{
					Headers:     &corev3.HeaderMap{Headers: headers},
					EndOfStream: endOfStream,
				},
			},
		}
	default:
		panic("unknown phase kind")
	}
}

func newBodyRequest(kind string, body []byte, endOfStream bool) *extprocv3.ProcessingRequest {
	if kind == "request" {
		return &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestBody{
				RequestBody: &extprocv3.HttpBody{
					Body:        body,
					EndOfStream: endOfStream,
				},
			},
		}
	}
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{
				Body:        body,
				EndOfStream: endOfStream,
			},
		},
	}
}

// gatherRegisteredMetrics registers the server's metrics to a private
// registry and returns the gathered metric families
func gatherRegisteredMetrics(t *testing.T, m *Metrics) []*dto.MetricFamily {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	m.Register(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics failed: %v", err)
	}
	return families
}

// counterValue reads a single-child counter's value from gathered families
func counterValue(t *testing.T, families []*dto.MetricFamily, name string) float64 {
	t.Helper()
	f := findMetricFamily(t, families, name)
	if len(f.GetMetric()) == 0 {
		t.Fatalf("no collected metrics for %q", name)
	}
	return f.GetMetric()[0].GetCounter().GetValue()
}

func TestProcessStreamContextCanceledWhileIdleIsGraceful(t *testing.T) {
	// the stream context transitioning to done between phases is handled like a
	// stream cancellation: notify the error handler, end gracefully
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := newProcessStreamFake(ctx)

	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v on stream cancellation, want nil", err)
	}
	if proc.errorHandlerCalls != 1 {
		t.Fatalf("ErrorHandler called %d times on stream cancellation, want 1", proc.errorHandlerCalls)
	}
	if !isStreamCancelled(proc.lastError) {
		t.Errorf("ErrorHandler error = %v, want a cancellation error", proc.lastError)
	}
	if phase := proc.phases[len(proc.phases)-1]; phase != REQUEST_PHASE_UNDETERMINED {
		t.Errorf("ErrorHandler phase = %d, want %d (REQUEST_PHASE_UNDETERMINED, no phase processed yet)", phase, REQUEST_PHASE_UNDETERMINED)
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	if got := counterValue(t, families, "extproc_phase_errors_total"); got != 0 {
		t.Errorf("extproc_phase_errors_total = %v, want 0 on a cancelled stream", got)
	}
	if got := counterValue(t, families, "extproc_errored_streams"); got != 0 {
		t.Errorf("extproc_errored_streams = %v, want 0 on a cancelled stream", got)
	}
}

func TestIsStreamCancelled(t *testing.T) {
	if isStreamCancelled(nil) {
		t.Error("isStreamCancelled(nil) = true, want false")
	}
	if isStreamCancelled(status.Error(codes.Canceled, "context canceled")) {
		// a cancelled stream is a cancellation, not a processing failure
		return
	}
	if isStreamCancelled(status.Error(codes.Internal, "failed to send response to envoy")) {
		t.Error("isStreamCancelled(send error) = true, want false")
	}
	if isStreamCancelled(errors.New("some other failure")) {
		t.Error("isStreamCancelled(other error) = true, want false")
	}
}

func TestProcessSendErrorContextCanceledDuringResponseBodyIsGraceful(t *testing.T) {
	// mirrors the reported logs: the final response body phase processed
	// successfully (token usage extracted, etc.) and the send of the response
	// to envoy then failed because the stream context was cancelled
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)

	stream := newProcessStreamFake(context.Background(),
		newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
		newHeadersRequest("response", false, &corev3.HeaderValue{Key: "content-type", RawValue: []byte("application/json")}),
		newBodyRequest("response", []byte("{"), true),
	)
	// the failure lands on the final send, i.e. the response body phase
	stream.sendErrs = []error{nil, nil, status.Error(codes.Canceled, "context canceled")}

	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v on stream cancellation during response send, want nil", err)
	}
	// the handler is still notified (for cleanup), but the stream is not errored
	if proc.errorHandlerCalls != 1 {
		t.Fatalf("ErrorHandler called %d times on stream cancellation, want 1", proc.errorHandlerCalls)
	}
	if status.Code(proc.lastError) != codes.Canceled {
		t.Errorf("ErrorHandler error = %v, want a cancellation error", proc.lastError)
	}
	if phase := proc.phases[len(proc.phases)-1]; phase != REQUEST_PHASE_RESPONSE_BODY {
		t.Errorf("ErrorHandler phase = %d, want %d (RESPONSE_BODY)", phase, REQUEST_PHASE_RESPONSE_BODY)
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	if got := counterValue(t, families, "extproc_phase_errors_total"); got != 0 {
		t.Errorf("extproc_phase_errors_total = %v, want 0 on a cancelled stream", got)
	}
	if got := counterValue(t, families, "extproc_response_send_errors"); got != 0 {
		t.Errorf("extproc_response_send_errors = %v, want 0 on a cancelled stream", got)
	}
	if got := counterValue(t, families, "extproc_errored_streams"); got != 0 {
		t.Errorf("extproc_errored_streams = %v, want 0 on a cancelled stream", got)
	}
}

func TestProcessSendErrorDuringResponseBodyInvokesErrorHandler(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)

	stream := newProcessStreamFake(context.Background(),
		newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
		newHeadersRequest("response", false, &corev3.HeaderValue{Key: "content-type", RawValue: []byte("application/json")}),
		newBodyRequest("response", []byte("{"), true),
	)
	// the failure lands on the final send, i.e. the response body phase
	stream.sendErrs = []error{nil, nil, status.Error(codes.Internal, "failed to send response to envoy")}

	if err := s.Process(stream); err == nil {
		t.Fatal("expected Process to return an error when sending the response fails")
	}
	if proc.errorHandlerCalls != 1 {
		t.Errorf("ErrorHandler called %d times, want 1", proc.errorHandlerCalls)
	}
	if proc.lastError == nil {
		t.Fatal("expected the last ErrorHandler error to be recorded")
	}
	if phase := proc.phases[len(proc.phases)-1]; phase != REQUEST_PHASE_RESPONSE_BODY {
		t.Errorf("ErrorHandler phase = %d, want %d (RESPONSE_BODY)", phase, REQUEST_PHASE_RESPONSE_BODY)
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	// the send failure itself is tracked by ResponseSendErrors, not PhaseErrors
	if got := counterValue(t, families, "extproc_phase_errors_total"); got != 0 {
		t.Errorf("extproc_phase_errors_total = %v, want 0", got)
	}
	if got := counterValue(t, families, "extproc_response_send_errors"); got != 1 {
		t.Errorf("extproc_response_send_errors = %v, want 1", got)
	}
	if got := counterValue(t, families, "extproc_errored_streams"); got != 1 {
		t.Errorf("extproc_errored_streams = %v, want 1", got)
	}
}

func TestProcessStreamContextCanceledDuringRecvIsGraceful(t *testing.T) {
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)

	stream := newProcessStreamFake(context.Background())
	stream.recvErr = status.Error(codes.Canceled, "context canceled")

	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v on stream cancellation during receive, want nil", err)
	}
	// the handler is still notified (for cleanup), with the last known phase
	if proc.errorHandlerCalls != 1 {
		t.Fatalf("ErrorHandler called %d times on stream cancellation, want 1", proc.errorHandlerCalls)
	}
	if status.Code(proc.lastError) != codes.Canceled {
		t.Errorf("ErrorHandler error = %v, want a cancellation error", proc.lastError)
	}
	if phase := proc.phases[len(proc.phases)-1]; phase != REQUEST_PHASE_UNDETERMINED {
		t.Errorf("ErrorHandler phase = %d, want %d (REQUEST_PHASE_UNDETERMINED, the last known phase here)", phase, REQUEST_PHASE_UNDETERMINED)
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	if got := counterValue(t, families, "extproc_phase_errors_total"); got != 0 {
		t.Errorf("extproc_phase_errors_total = %v, want 0 on a cancelled stream", got)
	}
	if got := counterValue(t, families, "extproc_errored_streams"); got != 0 {
		t.Errorf("extproc_errored_streams = %v, want 0 on a cancelled stream", got)
	}
}

type observabilityProcessor struct {
	name     string
	opts     *ProcessingOptions
	phases   []int
	obsModes []bool
}

func (p *observabilityProcessor) GetName() string                { return p.name }
func (p *observabilityProcessor) GetOptions() *ProcessingOptions { return p.opts }

func (p *observabilityProcessor) record(ctx *RequestContext, phase int) error {
	p.phases = append(p.phases, phase)
	p.obsModes = append(p.obsModes, ctx.ObservabilityMode)
	_ = ctx.AddHeader("x-obs-should-suppress", HeaderValue{RawValue: []byte("1")})
	return ctx.ContinueRequest()
}

func (p *observabilityProcessor) ProcessRequestHeaders(ctx *RequestContext, _ AllHeaders) error {
	return p.record(ctx, REQUEST_PHASE_REQUEST_HEADERS)
}

func (p *observabilityProcessor) ProcessRequestBody(ctx *RequestContext, _ []byte) error {
	return p.record(ctx, REQUEST_PHASE_REQUEST_BODY)
}

func (p *observabilityProcessor) ProcessRequestTrailers(ctx *RequestContext, _ AllHeaders) error {
	return p.record(ctx, REQUEST_PHASE_REQUEST_TRAILERS)
}

func (p *observabilityProcessor) ProcessResponseHeaders(ctx *RequestContext, _ AllHeaders) error {
	return p.record(ctx, REQUEST_PHASE_RESPONSE_HEADERS)
}

func (p *observabilityProcessor) ProcessResponseBody(ctx *RequestContext, _ []byte) error {
	return p.record(ctx, REQUEST_PHASE_RESPONSE_BODY)
}

func (p *observabilityProcessor) ProcessResponseTrailers(ctx *RequestContext, _ AllHeaders) error {
	return p.record(ctx, REQUEST_PHASE_RESPONSE_TRAILERS)
}

func (p *observabilityProcessor) ErrorHandler(_ *RequestContext, _ int, _ error) {}
func (p *observabilityProcessor) Close(_ int32) error                            { return nil }

func TestProcessObservabilityModeSuppressesResponses(t *testing.T) {
	proc := &observabilityProcessor{
		name: "obs",
		opts: &ProcessingOptions{
			UpdateExtProcHeader:  true,
			UpdateDurationHeader: true,
		},
	}
	s := newTestServer(t, proc, proc.opts)

	reqHdrs := newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")})
	reqHdrs.ObservabilityMode = true
	reqBody := newBodyRequest("request", []byte("hello"), true)
	reqBody.ObservabilityMode = true
	respHdrs := newHeadersRequest("response", false, &corev3.HeaderValue{Key: ":status", RawValue: []byte("200")})
	respHdrs.ObservabilityMode = true
	respBody := newBodyRequest("response", []byte("world"), true)
	respBody.ObservabilityMode = true

	stream := newProcessStreamFake(context.Background(), reqHdrs, reqBody, respHdrs, respBody)
	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v, want nil", err)
	}

	if len(stream.sent) != 0 {
		t.Errorf("stream.sent = %d responses, want 0 in observability_mode", len(stream.sent))
	}

	wantPhases := []int{
		REQUEST_PHASE_REQUEST_HEADERS,
		REQUEST_PHASE_REQUEST_BODY,
		REQUEST_PHASE_RESPONSE_HEADERS,
		REQUEST_PHASE_RESPONSE_BODY,
	}
	if len(proc.phases) != len(wantPhases) {
		t.Fatalf("recorded phases = %v, want %v", proc.phases, wantPhases)
	}
	for i, wp := range wantPhases {
		if proc.phases[i] != wp {
			t.Errorf("proc.phases[%d] = %d, want %d", i, proc.phases[i], wp)
		}
		if !proc.obsModes[i] {
			t.Errorf("ctx.ObservabilityMode at phase %d was false, want true", wp)
		}
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	if got := counterValue(t, families, "extproc_total_streams"); got != 1 {
		t.Errorf("extproc_total_streams = %v, want 1", got)
	}
	if got := counterValue(t, families, "extproc_request_body_bytes_total"); got != 5 {
		t.Errorf("extproc_request_body_bytes_total = %v, want 5", got)
	}
	if got := counterValue(t, families, "extproc_response_body_bytes_total"); got != 5 {
		t.Errorf("extproc_response_body_bytes_total = %v, want 5", got)
	}
	if got := counterValue(t, families, "extproc_empty_responses"); got != 0 {
		t.Errorf("extproc_empty_responses = %v, want 0", got)
	}
}

func TestProcessObservabilityModeProcessorErrors(t *testing.T) {
	t.Run("continue_on_failure", func(t *testing.T) {
		proc := &erroringProcessor{name: "obs-err-continue", opts: &ProcessingOptions{AbortOnProcessorFailure: false}}
		s := newTestServer(t, proc, proc.opts)

		req := newHeadersRequest("request", true, &corev3.HeaderValue{Key: ":method", RawValue: []byte("GET")})
		req.ObservabilityMode = true
		stream := newProcessStreamFake(context.Background(), req)

		if err := s.Process(stream); err != nil {
			t.Fatalf("Process returned error %v, want nil when AbortOnProcessorFailure is false", err)
		}
		if len(stream.sent) != 0 {
			t.Errorf("stream.sent = %d responses, want 0 in observability_mode", len(stream.sent))
		}
		families := gatherRegisteredMetrics(t, s.metrics)
		if got := counterValue(t, families, "extproc_phase_errors_total"); got != 1 {
			t.Errorf("extproc_phase_errors_total = %v, want 1", got)
		}
		if got := counterValue(t, families, "extproc_errored_streams"); got != 1 {
			t.Errorf("extproc_errored_streams = %v, want 1", got)
		}
		if got := counterValue(t, families, "extproc_empty_responses"); got != 0 {
			t.Errorf("extproc_empty_responses = %v, want 0", got)
		}
	})

	t.Run("abort_on_failure", func(t *testing.T) {
		proc := &erroringProcessor{name: "obs-err-abort", opts: &ProcessingOptions{AbortOnProcessorFailure: true}}
		s := newTestServer(t, proc, proc.opts)

		req := newHeadersRequest("request", true, &corev3.HeaderValue{Key: ":method", RawValue: []byte("GET")})
		req.ObservabilityMode = true
		stream := newProcessStreamFake(context.Background(), req)

		if err := s.Process(stream); err == nil {
			t.Fatal("expected Process to abort when AbortOnProcessorFailure is true")
		}
		if len(stream.sent) != 0 {
			t.Errorf("stream.sent = %d responses, want 0 in observability_mode", len(stream.sent))
		}
		families := gatherRegisteredMetrics(t, s.metrics)
		if got := counterValue(t, families, "extproc_errored_streams"); got != 1 {
			t.Errorf("extproc_errored_streams = %v, want 1", got)
		}
	})
}

type phaseHookProcessor struct {
	name       string
	opts       *ProcessingOptions
	onReqHdrs  func(ctx *RequestContext) error
	onReqBody  func(ctx *RequestContext) error
	onRespHdrs func(ctx *RequestContext) error
	onRespBody func(ctx *RequestContext) error
}

func (p *phaseHookProcessor) GetName() string                { return p.name }
func (p *phaseHookProcessor) GetOptions() *ProcessingOptions { return p.opts }
func (p *phaseHookProcessor) ProcessRequestHeaders(ctx *RequestContext, _ AllHeaders) error {
	if p.onReqHdrs != nil {
		return p.onReqHdrs(ctx)
	}
	return ctx.ContinueRequest()
}
func (p *phaseHookProcessor) ProcessRequestBody(ctx *RequestContext, _ []byte) error {
	if p.onReqBody != nil {
		return p.onReqBody(ctx)
	}
	return ctx.ContinueRequest()
}
func (p *phaseHookProcessor) ProcessRequestTrailers(ctx *RequestContext, _ AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *phaseHookProcessor) ProcessResponseHeaders(ctx *RequestContext, _ AllHeaders) error {
	if p.onRespHdrs != nil {
		return p.onRespHdrs(ctx)
	}
	return ctx.ContinueRequest()
}
func (p *phaseHookProcessor) ProcessResponseBody(ctx *RequestContext, _ []byte) error {
	if p.onRespBody != nil {
		return p.onRespBody(ctx)
	}
	return ctx.ContinueRequest()
}
func (p *phaseHookProcessor) ProcessResponseTrailers(ctx *RequestContext, _ AllHeaders) error {
	return ctx.ContinueRequest()
}
func (p *phaseHookProcessor) ErrorHandler(_ *RequestContext, _ int, _ error) {}
func (p *phaseHookProcessor) Close(_ int32) error                            { return nil }

func TestProcessPhaseHeaderExistenceCarriesIntoBodyPhase(t *testing.T) {
	t.Run("request_headers_mutations_carry_into_request_body", func(t *testing.T) {
		proc := &phaseHookProcessor{
			name: "carry",
			opts: NewDefaultProcessingOptions(),
			onReqHdrs: func(ctx *RequestContext) error {
				return ctx.AddHeader("x-added-in-hdrs", HeaderValue{RawValue: []byte("v1")})
			},
			onReqBody: func(ctx *RequestContext) error {
				_ = ctx.AddHeader("x-client", HeaderValue{RawValue: []byte("spoof")})
				_ = ctx.AddHeader("x-added-in-hdrs", HeaderValue{RawValue: []byte("v2")})
				return ctx.AddHeader("x-added-in-body", HeaderValue{RawValue: []byte("v3")})
			},
		}
		s := newTestServer(t, proc, proc.opts)
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		hdrResp, _, hdrErr := s.processPhase(
			newHeadersRequest("request", false, &corev3.HeaderValue{Key: "x-client", RawValue: []byte("orig")}),
			proc,
			rc,
		)
		if hdrErr != nil {
			t.Fatalf("RequestHeaders processPhase failed: %v", hdrErr)
		}
		hdrSet := hdrResp.GetRequestHeaders().GetResponse().GetHeaderMutation().GetSetHeaders()
		if len(hdrSet) != 1 || hdrSet[0].GetHeader().GetKey() != "x-added-in-hdrs" {
			t.Fatalf("RequestHeaders SetHeaders = %v, want [x-added-in-hdrs]", hdrSet)
		}

		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		bodyResp, _, bodyErr := s.processPhase(newBodyRequest("request", []byte("body"), true), proc, rc)
		if bodyErr != nil {
			t.Fatalf("RequestBody processPhase failed: %v", bodyErr)
		}
		bodySet := bodyResp.GetRequestBody().GetResponse().GetHeaderMutation().GetSetHeaders()
		if len(bodySet) != 1 || bodySet[0].GetHeader().GetKey() != "x-added-in-body" {
			t.Fatalf("RequestBody SetHeaders = %v, want only [x-added-in-body]", bodySet)
		}
	})

	t.Run("cancel_request_preserves_immediate_headers", func(t *testing.T) {
		proc := &phaseHookProcessor{
			name: "cancel",
			opts: NewDefaultProcessingOptions(),
			onReqHdrs: func(ctx *RequestContext) error {
				return ctx.CancelRequest(429, map[string]HeaderValue{
					"x-client": {RawValue: []byte("override-in-local-reply")},
				}, "limited")
			},
		}
		s := newTestServer(t, proc, proc.opts)
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		resp, _, err := s.processPhase(
			newHeadersRequest("request", true, &corev3.HeaderValue{Key: "x-client", RawValue: []byte("orig")}),
			proc,
			rc,
		)
		if err != nil {
			t.Fatalf("processPhase failed: %v", err)
		}
		immSet := resp.GetImmediateResponse().GetHeaders().GetSetHeaders()
		if len(immSet) != 1 || immSet[0].GetHeader().GetKey() != "x-client" {
			t.Fatalf("ImmediateResponse SetHeaders = %v, want [x-client]", immSet)
		}
	})
}

func TestProcessPhaseModeOverride(t *testing.T) {
	t.Run("request_headers_propagates_mode_override_and_headers", func(t *testing.T) {
		proc := &phaseHookProcessor{
			name: "mode-override",
			opts: NewDefaultProcessingOptions(),
			onReqHdrs: func(ctx *RequestContext) error {
				if err := ctx.AddHeader("x-checked", HeaderValue{RawValue: []byte("true")}); err != nil {
					return err
				}
				return ctx.OverrideMode(ModeOverride{
					RequestBodyMode:  BodyModeBuffered,
					ResponseBodyMode: BodyModeStreamed,
				})
			},
		}
		s := newTestServer(t, proc, proc.opts)
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		resp, phase, err := s.processPhase(
			newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
			proc,
			rc,
		)
		if err != nil {
			t.Fatalf("processPhase returned error: %v", err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if phase != REQUEST_PHASE_REQUEST_HEADERS {
			t.Errorf("phase = %d, want %d", phase, REQUEST_PHASE_REQUEST_HEADERS)
		}
		setHdrs := resp.GetRequestHeaders().GetResponse().GetHeaderMutation().GetSetHeaders()
		if len(setHdrs) != 1 || setHdrs[0].GetHeader().GetKey() != "x-checked" {
			t.Errorf("RequestHeaders SetHeaders = %v, want [x-checked]", setHdrs)
		}
		mo := resp.GetModeOverride()
		if mo == nil {
			t.Fatal("expected ProcessingResponse.ModeOverride to be set")
		}
		if mo.GetRequestBodyMode().String() != "BUFFERED" || mo.GetResponseBodyMode().String() != "STREAMED" {
			t.Errorf("ModeOverride = %+v, want RequestBodyMode=BUFFERED ResponseBodyMode=STREAMED", mo)
		}
	})

	t.Run("rejected_in_body_phase", func(t *testing.T) {
		proc := &phaseHookProcessor{
			name: "mode-override-invalid-phase",
			opts: &ProcessingOptions{AbortOnProcessorFailure: false},
			onReqBody: func(ctx *RequestContext) error {
				return ctx.OverrideMode(ModeOverride{ResponseBodyMode: BodyModeBuffered})
			},
		}
		s := newTestServer(t, proc, proc.opts)
		stream := newProcessStreamFake(
			context.Background(),
			newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
			newBodyRequest("request", []byte("payload"), true),
		)

		if err := s.Process(stream); err != nil {
			t.Fatalf("Process returned error %v, want nil when AbortOnProcessorFailure is false", err)
		}
		if len(stream.sent) != 2 {
			t.Fatalf("stream.sent = %d responses, want 2", len(stream.sent))
		}
		if stream.sent[1].GetModeOverride() != nil {
			t.Errorf("RequestBody response unexpectedly set ModeOverride: %v", stream.sent[1].GetModeOverride())
		}

		families := gatherRegisteredMetrics(t, s.metrics)
		if got := counterValue(t, families, "extproc_phase_errors_total"); got != 1 {
			t.Errorf("extproc_phase_errors_total = %v, want 1", got)
		}
		if got := counterValue(t, families, "extproc_errored_streams"); got != 1 {
			t.Errorf("extproc_errored_streams = %v, want 1", got)
		}
	})
}

func TestProcessStreamProtocolConfig(t *testing.T) {
	t.Run("persists_across_all_phases_when_only_first_message_sets_it", func(t *testing.T) {
		var observed []*ProtocolConfig
		capture := func(ctx *RequestContext) error {
			observed = append(observed, ctx.ProtocolConfig)
			return ctx.ContinueRequest()
		}
		proc := &phaseHookProcessor{
			name:       "proto-cfg-persist",
			opts:       NewDefaultProcessingOptions(),
			onReqHdrs:  capture,
			onReqBody:  capture,
			onRespHdrs: capture,
			onRespBody: capture,
		}
		s := newTestServer(t, proc, proc.opts)

		reqHdrs := newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")})
		reqHdrs.ProtocolConfig = &extprocv3.ProtocolConfiguration{
			RequestBodyMode:                         extproccfgv3.ProcessingMode_BUFFERED,
			ResponseBodyMode:                        extproccfgv3.ProcessingMode_STREAMED,
			SendBodyWithoutWaitingForHeaderResponse: true,
		}
		stream := newProcessStreamFake(
			context.Background(),
			reqHdrs,
			newBodyRequest("request", []byte("req-payload"), true),
			newHeadersRequest("response", false, &corev3.HeaderValue{Key: ":status", RawValue: []byte("200")}),
			newBodyRequest("response", []byte("resp-payload"), true),
		)

		if err := s.Process(stream); err != nil {
			t.Fatalf("Process returned error: %v", err)
		}
		if len(observed) != 4 {
			t.Fatalf("captured %d phases, want 4", len(observed))
		}
		want := ProtocolConfig{
			RequestBodyMode:                         BodyModeBuffered,
			ResponseBodyMode:                        BodyModeStreamed,
			SendBodyWithoutWaitingForHeaderResponse: true,
		}
		for i, pc := range observed {
			if pc == nil {
				t.Fatalf("phase %d observed nil ProtocolConfig", i)
			}
			if *pc != want {
				t.Errorf("phase %d ProtocolConfig = %+v, want %+v", i, *pc, want)
			}
		}
	})

	t.Run("captured_when_request_headers_skipped", func(t *testing.T) {
		var observed []*ProtocolConfig
		capture := func(ctx *RequestContext) error {
			observed = append(observed, ctx.ProtocolConfig)
			return ctx.ContinueRequest()
		}
		proc := &phaseHookProcessor{
			name:       "proto-cfg-skip-req-hdrs",
			opts:       NewDefaultProcessingOptions(),
			onRespHdrs: capture,
			onRespBody: capture,
		}
		s := newTestServer(t, proc, proc.opts)

		respHdrs := newHeadersRequest("response", false, &corev3.HeaderValue{Key: ":status", RawValue: []byte("200")})
		respHdrs.ProtocolConfig = &extprocv3.ProtocolConfiguration{
			RequestBodyMode:  extproccfgv3.ProcessingMode_NONE,
			ResponseBodyMode: extproccfgv3.ProcessingMode_BUFFERED,
		}
		stream := newProcessStreamFake(
			context.Background(),
			respHdrs,
			newBodyRequest("response", []byte("ok"), true),
		)

		if err := s.Process(stream); err != nil {
			t.Fatalf("Process returned error: %v", err)
		}
		if len(observed) != 2 {
			t.Fatalf("captured %d phases, want 2", len(observed))
		}
		want := ProtocolConfig{
			RequestBodyMode:  BodyModeNone,
			ResponseBodyMode: BodyModeBuffered,
		}
		for i, pc := range observed {
			if pc == nil {
				t.Fatalf("phase %d observed nil ProtocolConfig", i)
			}
			if *pc != want {
				t.Errorf("phase %d ProtocolConfig = %+v, want %+v", i, *pc, want)
			}
		}
	})

	t.Run("nil_when_absent_on_stream", func(t *testing.T) {
		var observed []*ProtocolConfig
		proc := &phaseHookProcessor{
			name: "proto-cfg-absent",
			opts: NewDefaultProcessingOptions(),
			onReqHdrs: func(ctx *RequestContext) error {
				observed = append(observed, ctx.ProtocolConfig)
				return ctx.ContinueRequest()
			},
			onRespHdrs: func(ctx *RequestContext) error {
				observed = append(observed, ctx.ProtocolConfig)
				return ctx.ContinueRequest()
			},
		}
		s := newTestServer(t, proc, proc.opts)

		stream := newProcessStreamFake(
			context.Background(),
			newHeadersRequest("request", true, &corev3.HeaderValue{Key: ":method", RawValue: []byte("GET")}),
			newHeadersRequest("response", true, &corev3.HeaderValue{Key: ":status", RawValue: []byte("200")}),
		)

		if err := s.Process(stream); err != nil {
			t.Fatalf("Process returned error: %v", err)
		}
		if len(observed) != 2 {
			t.Fatalf("captured %d phases, want 2", len(observed))
		}
		for i, pc := range observed {
			if pc != nil {
				t.Errorf("phase %d ProtocolConfig = %+v, want nil", i, pc)
			}
		}
	})
}

func TestProcessStreamMetadataAndAttributes(t *testing.T) {
	t.Run("inbound_metadata_and_attributes_persist_and_merge_across_phases", func(t *testing.T) {
		var respBodyMeta map[string]map[string]any
		var respBodyTyped *anypb.Any
		var respBodyAttrs map[string]any

		proc := &phaseHookProcessor{
			name: "metadata-attrs",
			opts: NewDefaultProcessingOptions(),
			onReqBody: func(ctx *RequestContext) error {
				return ctx.SetDynamicMetadata("com.example.out", "checked", true)
			},
			onRespBody: func(ctx *RequestContext) error {
				respBodyMeta = make(map[string]map[string]any, len(ctx.Metadata))
				for k, v := range ctx.Metadata {
					copied := make(map[string]any, len(v))
					for mk, mv := range v {
						copied[mk] = mv
					}
					respBodyMeta[k] = copied
				}
				respBodyTyped = ctx.TypedMetadata["com.example.typed"]
				respBodyAttrs = make(map[string]any, len(ctx.Attributes))
				for k, v := range ctx.Attributes {
					respBodyAttrs[k] = v
				}
				return ctx.ContinueRequest()
			},
		}
		s := newTestServer(t, proc, proc.opts)

		reqMetaStruct, err := structpb.NewStruct(map[string]any{"sub": "alice"})
		if err != nil {
			t.Fatal(err)
		}
		typedAny, err := anypb.New(wrapperspb.String("typed-claim"))
		if err != nil {
			t.Fatal(err)
		}
		reqAttrStruct, err := structpb.NewStruct(map[string]any{"request.protocol": "HTTP/2"})
		if err != nil {
			t.Fatal(err)
		}
		respMetaStruct, err := structpb.NewStruct(map[string]any{"region": "us-west"})
		if err != nil {
			t.Fatal(err)
		}
		respAttrStruct, err := structpb.NewStruct(map[string]any{"response.code": 200})
		if err != nil {
			t.Fatal(err)
		}

		reqHdrs := newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")})
		reqHdrs.MetadataContext = &corev3.Metadata{
			FilterMetadata:      map[string]*structpb.Struct{"com.example.auth": reqMetaStruct},
			TypedFilterMetadata: map[string]*anypb.Any{"com.example.typed": typedAny},
		}
		reqHdrs.Attributes = map[string]*structpb.Struct{
			"envoy.filters.http.ext_proc": reqAttrStruct,
		}

		respHdrs := newHeadersRequest("response", false, &corev3.HeaderValue{Key: ":status", RawValue: []byte("200")})
		respHdrs.MetadataContext = &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{"com.example.upstream": respMetaStruct},
		}
		respHdrs.Attributes = map[string]*structpb.Struct{
			"envoy.filters.http.ext_proc": respAttrStruct,
		}

		stream := newProcessStreamFake(
			context.Background(),
			reqHdrs,
			newBodyRequest("request", []byte("req-body"), true),
			respHdrs,
			newBodyRequest("response", []byte("resp-body"), true),
		)

		if perr := s.Process(stream); perr != nil {
			t.Fatalf("Process returned error: %v", perr)
		}
		if len(stream.sent) != 4 {
			t.Fatalf("stream.sent = %d responses, want 4", len(stream.sent))
		}

		if respBodyMeta["com.example.auth"]["sub"] != "alice" || respBodyMeta["com.example.upstream"]["region"] != "us-west" {
			t.Errorf("ResponseBody Metadata = %+v", respBodyMeta)
		}
		var unpacked wrapperspb.StringValue
		if respBodyTyped == nil || respBodyTyped.UnmarshalTo(&unpacked) != nil || unpacked.GetValue() != "typed-claim" {
			t.Errorf("ResponseBody TypedMetadata = %v, want typed-claim", unpacked.GetValue())
		}
		if respBodyAttrs["request.protocol"] != "HTTP/2" || respBodyAttrs["response.code"] != float64(200) {
			t.Errorf("ResponseBody Attributes = %+v", respBodyAttrs)
		}

		sentBodyMeta := stream.sent[1].GetDynamicMetadata().GetFields()["com.example.out"].GetStructValue().AsMap()
		if sentBodyMeta["checked"] != true {
			t.Errorf("RequestBody response DynamicMetadata = %+v, want checked=true", sentBodyMeta)
		}
		if stream.sent[2].GetDynamicMetadata() != nil {
			t.Errorf("ResponseHeaders response unexpectedly carried DynamicMetadata from previous phase: %v", stream.sent[2].GetDynamicMetadata())
		}
	})
}
