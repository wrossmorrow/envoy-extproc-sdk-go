package extproc

import (
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// phaseCaptureProcessor records the phase observed by a processor during
// request headers processing.
type phaseCaptureProcessor struct {
	observedPhase int
}

func (p *phaseCaptureProcessor) GetName() string { return "phase-capture" }

func (p *phaseCaptureProcessor) GetOptions() *ProcessingOptions { return NewDefaultProcessingOptions() }

func (p *phaseCaptureProcessor) ProcessRequestHeaders(ctx *RequestContext, headers AllHeaders) error {
	p.observedPhase = ctx.GetPhase()
	return ctx.ContinueRequest()
}

func (p *phaseCaptureProcessor) ProcessResponseHeaders(ctx *RequestContext, headers AllHeaders) error {
	return ctx.ContinueRequest()
}

func (p *phaseCaptureProcessor) ProcessResponseBody(ctx *RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (p *phaseCaptureProcessor) ProcessRequestBody(ctx *RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (p *phaseCaptureProcessor) ProcessRequestTrailers(ctx *RequestContext, trailers AllHeaders) error {
	return ctx.ContinueRequest()
}

func (p *phaseCaptureProcessor) ProcessResponseTrailers(ctx *RequestContext, trailers AllHeaders) error {
	return ctx.ContinueRequest()
}

func (p *phaseCaptureProcessor) ErrorHandler(ctx *RequestContext, phase int, err error) {}

func (p *phaseCaptureProcessor) Close(gracePeriodSeconds int32) error { return nil }

// TestProcessPhaseRequestHeadersProcessorObservesPhase guards against the
// phase being reset while the request context is initialized: initReqCtx resets
// the phase, so processPhase must set the phase again before invoking the
// processor, or processors observe the wrong phase.
func TestProcessPhaseRequestHeadersProcessorObservesPhase(t *testing.T) {
	proc := &phaseCaptureProcessor{}
	s := &GenericExtProcServer{
		name:      proc.GetName(),
		processor: proc,
		options:   NewDefaultProcessingOptions(),
		metrics:   NewEmptyMetrics(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	_, _, err := s.processPhase(&extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
					{Key: ":method", RawValue: []byte("POST")},
				}},
			},
		},
	}, proc, rc)
	if err != nil {
		t.Fatalf("processPhase returned error: %v", err)
	}

	if proc.observedPhase != REQUEST_PHASE_REQUEST_HEADERS {
		t.Errorf("processor observed phase = %d, want %d", proc.observedPhase, REQUEST_PHASE_REQUEST_HEADERS)
	}
}
