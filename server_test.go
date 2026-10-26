package extproc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	stream.sendErrs = []error{status.Error(codes.Canceled, "context canceled")}

	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v on stream cancellation during response send, want nil", err)
	}
	if proc.errorHandlerCalls != 0 {
		t.Errorf("ErrorHandler called %d times for a context cancelled stream, want 0", proc.errorHandlerCalls)
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

	stream := newProcessStreamFake(context.Background(),
		newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
	)
	stream.recvErr = status.Error(codes.Canceled, "context canceled")

	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v on stream cancellation during receive, want nil", err)
	}
	if proc.errorHandlerCalls != 0 {
		t.Errorf("ErrorHandler called %d times for a context cancelled stream, want 0", proc.errorHandlerCalls)
	}

	families := gatherRegisteredMetrics(t, s.metrics)
	if got := counterValue(t, families, "extproc_phase_errors_total"); got != 0 {
		t.Errorf("extproc_phase_errors_total = %v, want 0 on a cancelled stream", got)
	}
	if got := counterValue(t, families, "extproc_errored_streams"); got != 0 {
		t.Errorf("extproc_errored_streams = %v, want 0 on a cancelled stream", got)
	}
}

func TestProcessSendErrorContextCanceledAfterSuccessfulPhasesDoesNotCallErrorHandler(t *testing.T) {
	// a cancellation arriving while envoy is still streaming a response must
	// not be reported as a phase error, even after the phases themselves
	// completed without incident
	proc := newRecordingProcessor("test", NewDefaultProcessingOptions())
	s := newTestServer(t, proc, proc.opts)

	stream := newProcessStreamFake(context.Background(),
		newHeadersRequest("request", false, &corev3.HeaderValue{Key: ":method", RawValue: []byte("POST")}),
		newHeadersRequest("response", false, &corev3.HeaderValue{Key: "content-type", RawValue: []byte("application/json")}),
		newBodyRequest("response", []byte("hello"), true),
	)
	// the failure lands on the final send, i.e. the response body phase
	stream.sendErrs = []error{nil, nil, status.Error(codes.Canceled, "context canceled")}

	if err := s.Process(stream); err != nil {
		t.Fatalf("Process returned error %v on stream cancellation during response send, want nil", err)
	}
	if proc.errorHandlerCalls != 0 {
		t.Errorf("ErrorHandler called %d times for a context cancelled stream, want 0", proc.errorHandlerCalls)
	}
	if len(proc.phases) != 3 {
		t.Errorf("processor phases = %v, want the three phases that completed without error", proc.phases)
	}
}
