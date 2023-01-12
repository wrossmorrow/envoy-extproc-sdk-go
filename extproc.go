package extproc

import (
	"errors"
	"io"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type RequestProcessor interface {
	ProcessRequestHeaders(ctx *RequestContext, headers *extprocv3.HttpHeaders) error
	ProcessRequestBody(ctx *RequestContext, body *extprocv3.HttpBody) error
	ProcessRequestTrailers(ctx *RequestContext, trailers *extprocv3.HttpTrailers) error
	ProcessResponseHeaders(ctx *RequestContext, headers *extprocv3.HttpHeaders) error
	ProcessResponseBody(ctx *RequestContext, body *extprocv3.HttpBody) error
	ProcessResponseTrailers(ctx *RequestContext, trailers *extprocv3.HttpTrailers) error
}

type GenericExtProcServer struct {
	name      string
	processor *RequestProcessor
}

func (s *GenericExtProcServer) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {

	var rc *RequestContext

	if s.processor == nil {
		log.Fatalf("cannot process request stream without `processor` interface")
	}

	log.Println("Starting request stream")
	ctx := srv.Context()
	for {

		select {
		case <-ctx.Done():
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

		// clear response in the context if defined
		if rc != nil {
			rc.ResetResponse()
		}
		resp, err := processPhase(req, *(s.processor), rc)

		if resp == nil {
			log.Printf("processing did not define response")
			// TODO: what here?
		} else {
			log.Printf("extprocv3.ProcessingResponse %v \n", resp)
			if err := srv.Send(resp); err != nil {
				log.Printf("send error %v", err)
			}
		}

	} // end for over stream messages
}

func processPhase(req *extprocv3.ProcessingRequest, processor RequestProcessor, rc *RequestContext) (*extprocv3.ProcessingResponse, error) {

	var (
		ps  time.Time
		err error
	)

	if rc == nil {
		log.Printf("Request context is nil at %v \n", req.Request.(type))
	}

	phase := REQUEST_PHASE_UNDETERMINED

	switch v := req.Request.(type) {

	case *extprocv3.ProcessingRequest_RequestHeaders:
		phase = REQUEST_PHASE_REQUEST_HEADERS
		log.Printf("extprocv3.ProcessingRequest_RequestHeaders %v \n", v)
		h := req.Request.(*extprocv3.ProcessingRequest_RequestHeaders).RequestHeaders

		// define the request context (requires _not_ skipping request headers)
		if rc != nil {
			log.Printf("Request context is not nil in request headers phase")
		}
		rc, err = NewReqCtx(h.Headers)

		ps = time.Now()
		err = processor.ProcessRequestHeaders(rc, h)
		rc.duration += time.Since(ps).Nanoseconds()
		break

	case *extprocv3.ProcessingRequest_RequestBody:
		phase = REQUEST_PHASE_REQUEST_BODY
		log.Printf("Processing Request Body %v \n", v)
		b := req.Request.(*extprocv3.ProcessingRequest_RequestBody).RequestBody

		ps = time.Now()
		err = processor.ProcessRequestBody(rc, b)
		rc.duration += time.Since(ps).Nanoseconds()
		break

	case *extprocv3.ProcessingRequest_RequestTrailers:
		phase = REQUEST_PHASE_REQUEST_TRAILERS
		log.Printf("Processing Request Trailers %v \n", v)
		t := req.Request.(*extprocv3.ProcessingRequest_RequestTrailers).RequestTrailers

		ps = time.Now()
		err = processor.ProcessRequestTrailers(rc, t)
		rc.duration += time.Since(ps).Nanoseconds()
		break

	case *extprocv3.ProcessingRequest_ResponseHeaders:
		phase = REQUEST_PHASE_RESPONSE_HEADERS
		log.Printf("Processing Response Headers %v \n", v)
		h := req.Request.(*extprocv3.ProcessingRequest_ResponseHeaders).ResponseHeaders

		ps = time.Now()
		err = processor.ProcessResponseHeaders(rc, h)
		rc.duration += time.Since(ps).Nanoseconds()
		break

	case *extprocv3.ProcessingRequest_ResponseBody:
		phase = REQUEST_PHASE_RESPONSE_BODY
		log.Printf("Processing Response Body %v \n", v)
		b := req.Request.(*extprocv3.ProcessingRequest_ResponseBody).ResponseBody

		ps = time.Now()
		err = processor.ProcessResponseBody(rc, b)
		rc.duration += time.Since(ps).Nanoseconds()
		break

	case *extprocv3.ProcessingRequest_ResponseTrailers:
		phase = REQUEST_PHASE_RESPONSE_TRAILERS
		log.Printf("Processing Response Trailers %v \n", v)
		t := req.Request.(*extprocv3.ProcessingRequest_ResponseTrailers).ResponseTrailers

		ps = time.Now()
		err = processor.ProcessResponseTrailers(rc, t)
		rc.duration += time.Since(ps).Nanoseconds()
		break

	default:
		log.Printf("Unknown Request type %v\n", v)
		err = errors.New("Unknown request type")
	}

	if err != nil {
		log.Printf("process error %v", err)
		return nil, err
	}
	return rc.GetResponse(phase)

}
