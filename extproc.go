package extproc

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/uuid"
)

// Primary interface for supported request processing that SDK users must
// implement, passing a complying type to `GenericExtProcServer` or `Serve`.
//
// TODO: Passing through health check calls would help support better reasoning
// about dependencies for external processing (e.g., DB or kafka availability)
type RequestProcessor interface {
	GetName() string
	GetOptions() *ProcessingOptions

	ProcessRequestHeaders(ctx *RequestContext, headers AllHeaders) error
	ProcessRequestTrailers(ctx *RequestContext, trailers AllHeaders) error
	ProcessResponseHeaders(ctx *RequestContext, headers AllHeaders) error
	ProcessResponseTrailers(ctx *RequestContext, trailers AllHeaders) error

	ProcessResponseBody(ctx *RequestContext, body []byte) error
	ProcessRequestBody(ctx *RequestContext, body []byte) error
}

// Definition for DRY body handling
type BodyHandler func(*RequestContext, []byte) error

// Generic type for an external processor to which we can attach a gRPC bidi stream
// implementation.
type GenericExtProcServer struct {
	name      string
	processor RequestProcessor
	options   *ProcessingOptions
	logger    *slog.Logger
}

// Implementation of the bidi stream `Process` in an external processor. Given the
// type data `processor` and `options`, this intends to manage construction and
// updating of a `RequestContext` and calls to the `processor`'s phase-specific
// methods.
func (s *GenericExtProcServer) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {

	streamId, _ := uuid.NewV7()

	logger := s.logger.With(slog.Any("extproc_name", s.name), slog.Any("stream_id", streamId))

	if s.processor == nil {
		msg := "cannot process request stream without `processor` interface"
		logger.Error(msg)
		return fmt.Errorf(msg)
	}

	if s.options == nil {
		s.options = NewDefaultOptions()
	}

	if s.options.LogStream {
		logger.Info("Starting request stream")
	}

	rc := &RequestContext{
		extProcOptions: s.options,
		StreamId:       streamId,
	}
	ctx := srv.Context()

	for {
		select {
		case <-ctx.Done():
			if s.options.LogStream {
				logger.Info("Request stream terminated")
			}
			return ctx.Err()

		default:
		}

		req, err := srv.Recv()
		if errors.Is(err, io.EOF) {
			if s.options.LogStream {
				logger.Info("Request stream terminated", slog.Any("condition", "EOF"))
			}
			return nil
		}
		if status.Code(err) == codes.Canceled {
			if s.options.LogStream {
				logger.Info("Request stream terminated", slog.Any("condition", "CANCELLED"))
			}
			return nil
		}
		if err != nil {
			if s.options.LogStream {
				logger.Error("Failed to read stream", slog.Any("condition", "ERROR"))
			}
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", err)
		}

		// clear response in the context if defined, this is not
		// carried across request phases because each one has an
		// idiosyncratic response. rc gets "initialized" during
		// RequestHeaders phase processing.
		_ = rc.ResetPhase()

		resp, err := s.processPhase(req, s.processor, rc, logger)
		if err != nil {
			logger.Debug("Phase processing error", slog.Any("error", err))
			return status.Errorf(codes.Unknown, "error processing phase: %v", err)
		}
		if resp == nil {
			logger.Error("Phase processing error", slog.Any("error", "no response or error"))
			// TODO: what here? continue request? response cannot really be null
			return status.Errorf(codes.Unknown, "error processing phase: no response and no error")
		}
		if s.options.LogPhases {
			logger.Debug("Sending ProcessingResponse", slog.Any("response", resp))
			logger.Info("Sending ProcessingResponse")
		}
		if err := srv.Send(resp); err != nil {
			logger.Error("Send response error", slog.Any("error", err))
			return status.Errorf(codes.Unknown, "error sending response: %v", err)
		}
		// TODO: enable stream cancellation, may have a leak without it?

	} // end for over stream messages
}

// Internal per-phase processing logic, with a defined `RequestContext` and `RequestProcessor`
func (s *GenericExtProcServer) processPhase(procReq *extprocv3.ProcessingRequest, processor RequestProcessor, rc *RequestContext, logger *slog.Logger) (*extprocv3.ProcessingResponse, error) {
	if rc == nil {
		logger.Warn("RequestContext is undefined (nil)")
	}

	var (
		ps  time.Time
		err error
	)

	phase := REQUEST_PHASE_UNDETERMINED

	switch req := procReq.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:

		if s.options.LogPhases {
			logger.Info("Processing request headers")
		}

		phase = REQUEST_PHASE_REQUEST_HEADERS
		h := req.RequestHeaders

		// TODO: err check, but what is an error?
		ah, _ := NewAllHeadersFromEnvoyHeaderMap(h.Headers)

		// initialize request context (requires _not_ skipping request headers)
		_ = initReqCtx(rc, &ah)
		rc.EndOfStream = h.EndOfStream

		// set content-type, content-encoding, and/or transfer-encoding as available
		rc.bodybuffer = NewEncodedBodyFromHeaders(rc.AllHeaders)

		// TODO: _Could_ stack processors internally, e.g.
		//
		// 		for _, p := range s.processors { err = p.ProcessRequestHeaders(...); if err != nil { break } }
		//
		// This might get confusing though? Also response phase order
		// would need to be inverted.
		//
		// In any case, it would be a distinctly different behavior than
		// stacking ExtProcs in envoy. Until there is a need for this,
		// it's much easier to reason about one processor per ExtProc.
		// Users can "stack" whatever behaviors they like in the processors
		// themselves anyway.
		ps = time.Now()
		err = processor.ProcessRequestHeaders(rc, *rc.AllHeaders)
		rc.Duration += time.Since(ps)

	case *extprocv3.ProcessingRequest_RequestBody:

		if s.options.LogPhases {
			logger.Info("Processing request body")
		}

		phase = REQUEST_PHASE_REQUEST_BODY
		b := req.RequestBody
		rc.EndOfStream = b.EndOfStream

		ps = time.Now()
		err = rc.handleBodyChunk(processor.ProcessRequestBody, s.options, b.Body)
		rc.Duration += time.Since(ps)

	case *extprocv3.ProcessingRequest_RequestTrailers:

		if s.options.LogPhases {
			logger.Info("Processing request trailers")
		}

		phase = REQUEST_PHASE_REQUEST_TRAILERS
		ts := req.RequestTrailers

		// TODO: err check, but what is an error?
		trailers, _ := NewAllHeadersFromEnvoyHeaderMap(ts.Trailers)

		ps = time.Now()
		err = processor.ProcessRequestTrailers(rc, trailers)
		rc.Duration += time.Since(ps)

	case *extprocv3.ProcessingRequest_ResponseHeaders:

		if s.options.LogPhases {
			logger.Info("Processing response headers")
		}

		phase = REQUEST_PHASE_RESPONSE_HEADERS
		hs := req.ResponseHeaders
		rc.EndOfStream = hs.EndOfStream

		// _response_ headers

		// TODO: err check, but what is an error?
		headers, _ := NewAllHeadersFromEnvoyHeaderMap(hs.Headers)

		// set status (ignoring error if found, 0 default)
		_ = rc.parseStatusFromResponseHeaders(headers)

		// remove "envoy" headers from (copied) headers, so clients don't need to parse
		headers.DropHeadersNamedStartingWith(":")

		rc.AllHeaders = &headers

		// set content-type, content-encoding, and/or transfer-encoding as available
		rc.bodybuffer = NewEncodedBodyFromHeaders(&headers)

		ps = time.Now()
		err = processor.ProcessResponseHeaders(rc, headers)
		rc.Duration += time.Since(ps)

		if s.options.UpdateExtProcHeader {
			rc.AppendHeader("x-extproc-names", HeaderValue{RawValue: []byte(s.name)})
		}
		if rc.EndOfStream && s.options.UpdateDurationHeader {
			rc.AppendHeader("x-extproc-duration-ns", HeaderValue{RawValue: []byte(strconv.FormatInt(rc.Duration.Nanoseconds(), 10))})
		}

	case *extprocv3.ProcessingRequest_ResponseBody:

		if s.options.LogPhases {
			logger.Info("Processing response body")
		}

		phase = REQUEST_PHASE_RESPONSE_BODY
		b := req.ResponseBody
		rc.EndOfStream = b.EndOfStream

		ps = time.Now()
		err = rc.handleBodyChunk(processor.ProcessResponseBody, s.options, b.Body)
		rc.Duration += time.Since(ps)

		if rc.EndOfStream && s.options.UpdateDurationHeader {
			rc.AppendHeader("x-extproc-duration-ns", HeaderValue{RawValue: []byte(strconv.FormatInt(rc.Duration.Nanoseconds(), 10))})
		}

	case *extprocv3.ProcessingRequest_ResponseTrailers:

		if s.options.LogPhases {
			logger.Info("Processing response trailers")
		}

		phase = REQUEST_PHASE_RESPONSE_TRAILERS
		ts := req.ResponseTrailers

		// TODO: err check, but what is an error?
		trailers, _ := NewAllHeadersFromEnvoyHeaderMap(ts.Trailers)

		ps = time.Now()
		err = processor.ProcessResponseTrailers(rc, trailers)
		rc.Duration += time.Since(ps)

	default:
		logger.Warn("Unknown Request type", slog.Any("type", req))
		err = errors.New("unknown request type")
	}
	if err != nil {
		return nil, err
	}

	return rc.GetResponse(phase)
}
