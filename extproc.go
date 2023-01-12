package extproc

import (
	"errors"
	"io"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type RequestProcessor interface {
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
}

func (s *GenericExtProcServer) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {

	rc := &RequestContext{}

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

		// clear response in the context if defined, this is not
		// carried across request phases because each one has an
		// idiosyncratic response. rc gets "initialized" during
		// RequestHeaders phase processing. 
		if rc != nil {
			rc.ResetPhase()
		}
		resp, err := processPhase(req, *(s.processor), rc)

		if err != nil {
			log.Printf("processing error %v", err)
		} else if resp == nil {
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
		log.Printf("Request context is nil\n")
	}

	phase := REQUEST_PHASE_UNDETERMINED

	switch v := req.Request.(type) {

	case *extprocv3.ProcessingRequest_RequestHeaders:
		phase = REQUEST_PHASE_REQUEST_HEADERS
		log.Printf("extprocv3.ProcessingRequest_RequestHeaders %v \n", v)
		h := req.Request.(*extprocv3.ProcessingRequest_RequestHeaders).RequestHeaders

		// initialize request context (requires _not_ skipping request headers)
		err = initReqCtx(rc, h.Headers)
		rc.EndOfStream = h.EndOfStream

		ps = time.Now()
		err = processor.ProcessRequestHeaders(rc, rc.Headers)
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_RequestBody:
		phase = REQUEST_PHASE_REQUEST_BODY
		log.Printf("Processing Request Body %v \n", v)
		b := req.Request.(*extprocv3.ProcessingRequest_RequestBody).RequestBody
		rc.EndOfStream = b.EndOfStream

		ps = time.Now()
		err = processor.ProcessRequestBody(rc, b.Body)
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_RequestTrailers:
		phase = REQUEST_PHASE_REQUEST_TRAILERS
		log.Printf("Processing Request Trailers %v \n", v)
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
		log.Printf("Processing Response Headers %v \n", v)
		hs := req.Request.(*extprocv3.ProcessingRequest_ResponseHeaders).ResponseHeaders
		rc.EndOfStream = hs.EndOfStream

		headers := make(map[string][]string)
		for _, h := range hs.Headers.Headers {
			headers[h.Key] = strings.Split(h.Value, ",")
		}

		ps = time.Now()
		err = processor.ProcessResponseHeaders(rc, headers)
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_ResponseBody:
		phase = REQUEST_PHASE_RESPONSE_BODY
		log.Printf("Processing Response Body %v \n", v)
		b := req.Request.(*extprocv3.ProcessingRequest_ResponseBody).ResponseBody
		rc.EndOfStream = b.EndOfStream

		ps = time.Now()
		err = processor.ProcessResponseBody(rc, b.Body)
		rc.Duration += time.Since(ps)
		break

	case *extprocv3.ProcessingRequest_ResponseTrailers:
		phase = REQUEST_PHASE_RESPONSE_TRAILERS
		log.Printf("Processing Response Trailers %v \n", v)
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
		log.Printf("Unknown Request type %v\n", v)
		err = errors.New("Unknown request type")
	}

	if err != nil {
		return nil, err
	}
	return rc.GetResponse(phase)

}
