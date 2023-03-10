package extproc

import (
	"errors"
	"io"
	"log"
	"strings"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type RequestProcessor interface {
	GetName() string
	GetOptions() *ProcessingOptions
	ProcessRequestHeaders(ctx *RequestContext, headers map[string][]string) error
	ProcessRequestBody(ctx *RequestContext, body []byte) error
	ProcessRequestTrailers(ctx *RequestContext, trailers map[string][]string) error
	ProcessResponseHeaders(ctx *RequestContext, headers map[string][]string) error
	ProcessResponseBody(ctx *RequestContext, body []byte) error
	ProcessResponseTrailers(ctx *RequestContext, trailers map[string][]string) error
}

type GenericExtProcServer struct {
	name      string
	processor *RequestProcessor
	options   *ProcessingOptions
}

func (s *GenericExtProcServer) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {

	rc := &RequestContext{}

	if s.processor == nil {
		log.Fatalf("cannot process request stream without `processor` interface")
	}

	if s.options == nil {
		s.options = NewOptions()
	}

	if s.options.LogStream {
		log.Printf("Starting request stream in \"%s\"", s.name)
	}

	ctx := srv.Context()
	for {

		select {
		case <-ctx.Done():
			if s.options.LogStream {
				log.Printf("Request stream terminated in \"%s\"", s.name)
			}
			return ctx.Err()
		default:
		}

		req, err := srv.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", err)
		}

		// clear response in the context if defined, this is not
		// carried across request phases because each one has an
		// idiosyncratic response. rc gets "initialized" during
		// RequestHeaders phase processing.
		if rc != nil {
			rc.ResetPhase()
		}
		resp, err := s.processPhase(req, *(s.processor), rc)

		if err != nil {
			log.Printf("Phase processing error %v", err)
		} else if resp == nil {
			log.Printf("Phase processing did not define a response")
			// TODO: what here?
		} else {
			if s.options.LogPhases {
				log.Printf("Sending ProcessingResponse: %v \n", resp)
			}
			if err := srv.Send(resp); err != nil {
				log.Printf("Send error %v", err)
			}
		}

	} // end for over stream messages
}

func (s *GenericExtProcServer) processPhase(req *extprocv3.ProcessingRequest, processor RequestProcessor, rc *RequestContext) (*extprocv3.ProcessingResponse, error) {

	var (
		ps  time.Time
		err error
	)

	if rc == nil {
		log.Printf("WARNING: RequestContext is undefined (nil)\n")
	}

	phase := REQUEST_PHASE_UNDETERMINED

	switch v := req.Request.(type) {

	case *extprocv3.ProcessingRequest_RequestHeaders:
		phase = REQUEST_PHASE_REQUEST_HEADERS
		if s.options.LogPhases {
			log.Printf("Processing Request Headers: %v \n", v)
		}
		h := req.Request.(*extprocv3.ProcessingRequest_RequestHeaders).RequestHeaders

		// initialize request context (requires _not_ skipping request headers)
		err = initReqCtx(rc, h.Headers)
		rc.EndOfStream = h.EndOfStream

		ps = time.Now()
		err = processor.ProcessRequestHeaders(rc, rc.Headers)
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
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_RequestBody:
		phase = REQUEST_PHASE_REQUEST_BODY
		if s.options.LogPhases {
			log.Printf("Processing Request Body: %v \n", v)
		}
		b := req.Request.(*extprocv3.ProcessingRequest_RequestBody).RequestBody
		rc.EndOfStream = b.EndOfStream

		ps = time.Now()
		err = processor.ProcessRequestBody(rc, b.Body)
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_RequestTrailers:
		phase = REQUEST_PHASE_REQUEST_TRAILERS
		if s.options.LogPhases {
			log.Printf("Processing Request Trailers: %v \n", v)
		}
		ts := req.Request.(*extprocv3.ProcessingRequest_RequestTrailers).RequestTrailers

		trailers := make(map[string][]string)
		for _, h := range ts.Trailers.Headers {
			trailers[h.Key] = strings.Split(h.Value, ",")
		}

		ps = time.Now()
		err = processor.ProcessRequestTrailers(rc, trailers)
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_ResponseHeaders:
		phase = REQUEST_PHASE_RESPONSE_HEADERS
		if s.options.LogPhases {
			log.Printf("Processing Response Headers: %v \n", v)
		}
		hs := req.Request.(*extprocv3.ProcessingRequest_ResponseHeaders).ResponseHeaders
		rc.EndOfStream = hs.EndOfStream

		// _response_ headers
		headers := make(map[string][]string)
		for _, h := range hs.Headers.Headers {
			headers[h.Key] = strings.Split(h.Value, ",")
		}

		ps = time.Now()
		err = processor.ProcessResponseHeaders(rc, headers)
		rc.Duration += time.Since(ps)
		
		if s.options.UpdateExtProcHeader {
			rc.AppendHeader("x-extproc-names", s.name)
		}
		if rc.EndOfStream && s.options.UpdateDurationHeader {
			rc.AppendHeader("x-extproc-duration-ns", strconv.FormatInt(rc.Duration.Nanoseconds(), 10))
		}

		break

	case *extprocv3.ProcessingRequest_ResponseBody:
		phase = REQUEST_PHASE_RESPONSE_BODY
		if s.options.LogPhases {
			log.Printf("Processing Response Body: %v \n", v)
		}
		b := req.Request.(*extprocv3.ProcessingRequest_ResponseBody).ResponseBody
		rc.EndOfStream = b.EndOfStream

		ps = time.Now()
		err = processor.ProcessResponseBody(rc, b.Body)
		rc.Duration += time.Since(ps)

		if rc.EndOfStream && s.options.UpdateDurationHeader {
			rc.AppendHeader("x-extproc-duration-ns", strconv.FormatInt(rc.Duration.Nanoseconds(), 10))
		}

		break

	case *extprocv3.ProcessingRequest_ResponseTrailers:
		phase = REQUEST_PHASE_RESPONSE_TRAILERS
		if s.options.LogPhases {
			log.Printf("Processing Response Trailers: %v \n", v)
		}
		ts := req.Request.(*extprocv3.ProcessingRequest_ResponseTrailers).ResponseTrailers

		trailers := make(map[string][]string)
		for _, h := range ts.Trailers.Headers {
			trailers[h.Key] = strings.Split(h.Value, ",")
		}

		ps = time.Now()
		err = processor.ProcessResponseTrailers(rc, trailers)
		rc.Duration += time.Since(ps)
		break

	default:
		if s.options.LogPhases {
			log.Printf("Unknown Request type: %v\n", v)
		}
		err = errors.New("Unknown request type")
	}

	if err != nil {
		return nil, err
	}
	return rc.GetResponse(phase)

}
