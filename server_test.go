package extproc

import (
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
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
	if rc.GetPhase() != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("context phase = %d, want %d", rc.GetPhase(), REQUEST_PHASE_REQUEST_HEADERS)
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
