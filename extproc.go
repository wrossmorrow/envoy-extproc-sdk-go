package extproc

import (
	"errors"
	"io"
	"log/slog"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type RequestProcessor interface {
	GetName() string
	GetOptions() *ProcessingOptions

	// Process headers for requests or responses.
	ProcessRequestHeaders(ctx *RequestContext, headers AllHeaders) error
	ProcessResponseHeaders(ctx *RequestContext, headers AllHeaders) error

	// Process bodies (or body chunks) for requests or responses.
	// Note we don't buffer bodies if streamed, so data can be partial.
	ProcessResponseBody(ctx *RequestContext, body []byte) error
	ProcessRequestBody(ctx *RequestContext, body []byte) error

	// Process trailers for requests or responses.
	ProcessRequestTrailers(ctx *RequestContext, trailers AllHeaders) error
	ProcessResponseTrailers(ctx *RequestContext, trailers AllHeaders) error

	// ErrorHandler is called when an error occurs in this SDK during request
	// processing. An implemented handler can implement any logic it likes.
	// Phase is the "enum" constants from context.go.
	ErrorHandler(ctx *RequestContext, phase int, err error)

	// Get notified this processor should shut down
	Close(gracePeriodSeconds int32) error
}

type GenericExtProcServer struct {
	name      string
	processor RequestProcessor
	options   *ProcessingOptions
	metrics   *Metrics
	logger    *slog.Logger
}

func (s *GenericExtProcServer) Close(gracePeriodSeconds int32) error {
	return s.processor.Close(gracePeriodSeconds)
}

func (s *GenericExtProcServer) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {
	if s.processor == nil {
		msg := "cannot process request stream without `processor` interface"
		s.logger.Error(msg)
		return errors.New(msg)
	}

	s.logger.Debug("Starting request stream", slog.String("name", s.name))

	rc := newRequestContext()
	ctx := srv.Context()

	// a stream is counted as errored at most once, however many phases failed;
	// PhaseErrors carries the per-phase count
	errored := false

	s.metrics.TotalStreams.Inc()

	s.metrics.ActiveStreams.Inc()
	defer func() {
		s.metrics.ActiveStreams.Dec()
		if errored {
			s.metrics.ErroredStreams.Inc()
		}
	}()

	ss := time.Now()
	defer func() {
		dur := time.Since(ss).Seconds()
		s.metrics.StreamDurationSeconds.Observe(float64(dur))
	}()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("Request stream terminated", slog.String("name", s.name))
			return ctx.Err()

		default:
		}

		req, err := srv.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if status.Code(err) == codes.Canceled {
				s.logger.Debug("Processing stream cancelled", slog.String("error", err.Error()))
				return nil
			}
			errored = true
			s.processor.ErrorHandler(rc, REQUEST_PHASE_UNDETERMINED, err)
			return status.Errorf(codes.Unknown, "failed to receive stream request: %v", err)
		}

		// clear response in the context if defined, this is not carried across request phases because each
		// one has an idiosyncratic response. rc gets "initialized" during RequestHeaders phase processing.
		err = rc.ResetPhase()
		if err != nil {
			errored = true
			s.metrics.PhaseErrors.Inc()
			s.logger.Error("Phase processing error", slog.String("phase", RequestPhaseToString(REQUEST_PHASE_UNDETERMINED)), slog.String("error", err.Error()))
			if s.options.AbortOnProcessorFailure {
				return status.Errorf(codes.Unknown, "failed to reset extproc context: %v", err)
			}
		}

		resp, phase, err := s.processPhase(req, s.processor, rc)
		phase_name := RequestPhaseToString(phase)
		if err != nil {
			errored = true
			s.metrics.PhaseErrors.Inc()
			s.logger.Error("Phase processing error", slog.String("phase", phase_name), slog.String("error", err.Error()))
			if s.options.AbortOnProcessorFailure {
				return status.Errorf(codes.Unknown, "processor failed and abort requested: %v", err)
			}
		}
		if resp == nil {
			s.metrics.TotalEmptyResponses.Inc()
			s.logger.Warn("Phase processing did not define a response", slog.String("phase", RequestPhaseToString(phase)))
			resp, err = rc.EmptyContinueResponse(phase)
			if err != nil {
				errored = true
				s.logger.Error("Failed to construct empty continue response for phase", slog.String("phase", phase_name), slog.String("error", err.Error()))
				return status.Errorf(codes.Unknown, "failed to construct empty continue response: %v", err)
			}
		}

		s.logger.Debug("Sending ProcessingResponse", slog.String("phase", phase_name), slog.Any("response", resp))
		if err := srv.Send(resp); err != nil {
			errored = true
			s.metrics.ResponseSendErrors.Inc()
			s.logger.Error("Send ProcessingResponse error", slog.String("phase", phase_name), slog.String("error", err.Error()))
			s.processor.ErrorHandler(rc, phase, err)
			return status.Errorf(codes.Unknown, "failed to send response to envoy: %v", err)
		}

	} // end for over stream messages
}

func (s *GenericExtProcServer) processPhase(procReq *extprocv3.ProcessingRequest, processor RequestProcessor, rc *RequestContext) (*extprocv3.ProcessingResponse, int, error) {
	if rc == nil {
		s.logger.Warn("RequestContext is undefined (nil)")
	}

	var (
		ps  time.Time
		dur time.Duration
		err error
	)

	phase := REQUEST_PHASE_UNDETERMINED

	ps = time.Now()
	switch req := procReq.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		phase = REQUEST_PHASE_REQUEST_HEADERS
		rc.setPhase(phase)
		s.logger.Debug("Processing request Headers", slog.Any("request", req))
		s.metrics.TotalRequestHeaders.Inc()

		h := req.RequestHeaders

		// initialize request context (requires _not_ skipping request headers
		// in envoy extproc filter config, otherwise this will be nil)
		err = initReqCtx(rc, h.Headers)
		if err != nil {
			s.logger.Error("RequestContext initialization error", slog.String("error", err.Error()))
			s.processor.ErrorHandler(rc, phase, err)
			return nil, phase, err
		}
		rc.EndOfStream = h.EndOfStream

		err = processor.ProcessRequestHeaders(rc, rc.AllHeaders) //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur

	case *extprocv3.ProcessingRequest_RequestBody:
		phase = REQUEST_PHASE_REQUEST_BODY
		rc.setPhase(phase)
		s.logger.Debug("Processing request Body", slog.Any("request", req))
		s.metrics.TotalRequestBody.Inc()

		b := req.RequestBody
		s.metrics.BodyBytesReceived.Add(float64(len(b.Body)))

		rc.EndOfStream = b.EndOfStream

		err = processor.ProcessRequestBody(rc, b.Body) //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur

	case *extprocv3.ProcessingRequest_RequestTrailers:
		phase = REQUEST_PHASE_REQUEST_TRAILERS
		rc.setPhase(phase)
		s.logger.Debug("Processing request Trailers", slog.Any("request", req))
		s.metrics.TotalRequestTrailers.Inc()

		ts := req.RequestTrailers

		var trailers AllHeaders
		trailers, err = genHeaders(ts.Trailers)
		if err != nil {
			s.logger.Error("Failed to generate request trailers", slog.String("error", err.Error()))
			s.processor.ErrorHandler(rc, phase, err)
			return nil, phase, err
		}

		err = processor.ProcessRequestTrailers(rc, trailers) //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur

	case *extprocv3.ProcessingRequest_ResponseHeaders:
		phase = REQUEST_PHASE_RESPONSE_HEADERS
		rc.setPhase(phase)
		s.logger.Debug("Processing response Headers", slog.Any("request", req))
		s.metrics.TotalResponseHeaders.Inc()

		hs := req.ResponseHeaders
		rc.EndOfStream = hs.EndOfStream

		var headers AllHeaders
		headers, err = genHeaders(hs.Headers)
		if err != nil {
			s.logger.Error("Failed to generate response headers", slog.String("error", err.Error()))
			s.processor.ErrorHandler(rc, phase, err)
			return nil, phase, err
		}

		err = processor.ProcessResponseHeaders(rc, headers) //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur

		if s.options.UpdateExtProcHeader {
			rc.AppendHeader("x-extproc-names", HeaderValue{RawValue: []byte(s.name)})
		}
		if rc.EndOfStream && s.options.UpdateDurationHeader {
			rc.AppendHeader("x-extproc-duration-ns", HeaderValue{RawValue: []byte(strconv.FormatInt(rc.Duration.Nanoseconds(), 10))})
		}

	case *extprocv3.ProcessingRequest_ResponseBody:
		phase = REQUEST_PHASE_RESPONSE_BODY
		rc.setPhase(phase)
		s.logger.Debug("Processing response Body", slog.Any("request", req))
		s.metrics.TotalResponseBody.Inc()

		b := req.ResponseBody
		rc.EndOfStream = b.EndOfStream

		s.metrics.BodyBytesReturned.Add(float64(len(b.Body)))

		err = processor.ProcessResponseBody(rc, b.Body) //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur

		if rc.EndOfStream && s.options.UpdateDurationHeader {
			rc.AppendHeader("x-extproc-duration-ns", HeaderValue{RawValue: []byte(strconv.FormatInt(rc.Duration.Nanoseconds(), 10))})
		}

	case *extprocv3.ProcessingRequest_ResponseTrailers:
		phase = REQUEST_PHASE_RESPONSE_TRAILERS
		rc.setPhase(phase)
		s.logger.Debug("Processing response Trailers", slog.Any("request", req))
		s.metrics.TotalResponseTrailers.Inc()

		ts := req.ResponseTrailers

		var trailers AllHeaders
		trailers, err = genHeaders(ts.Trailers)
		if err != nil {
			s.logger.Error("Failed to generate request trailers", slog.String("error", err.Error()))
			s.processor.ErrorHandler(rc, phase, err)
			return nil, phase, err
		}

		err = processor.ProcessResponseTrailers(rc, trailers) //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur

	default:
		s.logger.Warn("Unknown request type", slog.Any("request", req))
		err = errors.New("unknown request type") //nolint:errcheck,staticcheck
		dur = time.Since(ps)
		rc.Duration += dur
	}

	if err != nil {
		return nil, phase, err
	}

	s.logger.Debug("Phase processing complete", slog.String("phase", RequestPhaseToString(phase)), slog.String("duration", dur.String()))
	s.logger.Debug("RequestContext state", slog.Any("context", rc))
	response, err := rc.GetResponse(phase)
	return response, phase, err
}
