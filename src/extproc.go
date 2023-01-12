package extproc

import (
	"io"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type RequestProcessor interface {
	ProcessRequestHeaders(ctx *RequestContext, headers *extprocv3.HttpHeaders) (*extprocv3.CommonResponse, *extprocv3.ImmediateResponse, error)
	ProcessRequestBody(ctx *RequestContext, body *extprocv3.HttpBody) (*extprocv3.CommonResponse, *extprocv3.ImmediateResponse, error)
	ProcessRequestTrailers(ctx *RequestContext, trailers *extprocv3.HttpTrailers) (*extprocv3.HeaderMutation, error)
	ProcessResponseHeaders(ctx *RequestContext, headers *extprocv3.HttpHeaders) (*extprocv3.CommonResponse, *extprocv3.ImmediateResponse, error)
	ProcessResponseBody(ctx *RequestContext, body *extprocv3.HttpBody) (*extprocv3.CommonResponse, *extprocv3.ImmediateResponse, error)
	ProcessResponseTrailers(ctx *RequestContext, trailers *extprocv3.HttpTrailers) (*extprocv3.HeaderMutation, error)
}

type genericExtProcServer struct {
	name      string
	processor *RequestProcessor
}

func (s *genericExtProcServer) Process(srv extprocv3.ExternalProcessor_ProcessServer) error {

	var (
		rc *RequestContext
		ps time.Time
	)

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

		resp := &extprocv3.ProcessingResponse{}
		switch v := req.Request.(type) {

		case *extprocv3.ProcessingRequest_RequestHeaders:
			log.Printf("extprocv3.ProcessingRequest_RequestHeaders %v \n", v)
			h := req.Request.(*extprocv3.ProcessingRequest_RequestHeaders).RequestHeaders

			// define the request context (requires not skipping request headers)
			rc, err = NewReqCtx(h.Headers)

			ps = time.Now()
			cr, ir, err := (*s.processor).ProcessRequestHeaders(rc, h)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else if ir != nil {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_RequestHeaders{
						RequestHeaders: &extprocv3.HeadersResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *extprocv3.ProcessingRequest_RequestBody:
			log.Printf("Processing Request Body %v \n", v)
			b := req.Request.(*extprocv3.ProcessingRequest_RequestBody).RequestBody

			ps = time.Now()
			cr, ir, err := (*s.processor).ProcessRequestBody(rc, b)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else if ir != nil {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_RequestBody{
						RequestBody: &extprocv3.BodyResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *extprocv3.ProcessingRequest_RequestTrailers:
			log.Printf("Processing Request Trailers %v \n", v)
			t := req.Request.(*extprocv3.ProcessingRequest_RequestTrailers).RequestTrailers

			ps = time.Now()
			hm, err := (*s.processor).ProcessRequestTrailers(rc, t)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_RequestTrailers{
						RequestTrailers: &extprocv3.TrailersResponse{
							HeaderMutation: hm,
						},
					},
				}
			}
			break

		case *extprocv3.ProcessingRequest_ResponseHeaders:
			log.Printf("Processing Response Headers %v \n", v)
			h := req.Request.(*extprocv3.ProcessingRequest_ResponseHeaders).ResponseHeaders

			ps = time.Now()
			cr, ir, err := (*s.processor).ProcessResponseHeaders(rc, h)
			rc.duration += time.Since(ps).Nanoseconds()

			// NOTE: do we need to append header with extproc duration?
			// HeaderValueOption{
			//		append_action: OVERWRITE_IF_EXISTS_OR_ADD,
			//		Header{key: "x-extproc-duration", value: string(rc.duration)}
			// }
			//
			// or
			//
			// dhvo, _ := rc.durationHeader()

			if err != nil {
				log.Printf("process error %v", err)
			} else if ir != nil {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ResponseHeaders{
						ResponseHeaders: &extprocv3.HeadersResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *extprocv3.ProcessingRequest_ResponseBody:
			log.Printf("Processing Response Body %v \n", v)
			b := req.Request.(*extprocv3.ProcessingRequest_ResponseBody).ResponseBody

			ps = time.Now()
			cr, ir, err := (*s.processor).ProcessResponseBody(rc, b)
			rc.duration += time.Since(ps).Nanoseconds()

			// NOTE: do we need to append header with extproc duration?
			// HeaderValueOption{
			//		append_action: OVERWRITE_IF_EXISTS_OR_ADD,
			//		Header{key: "x-extproc-duration", value: string(rc.duration)}
			// }
			//
			// or
			//
			// dhvo, _ := rc.durationHeader()

			if err != nil {
				log.Printf("process error %v", err)
			} else if ir != nil {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ResponseBody{
						ResponseBody: &extprocv3.BodyResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *extprocv3.ProcessingRequest_ResponseTrailers:
			log.Printf("Processing Response Trailers %v \n", v)
			t := req.Request.(*extprocv3.ProcessingRequest_ResponseTrailers).ResponseTrailers

			ps = time.Now()
			hm, err := (*s.processor).ProcessResponseTrailers(rc, t)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else {
				resp = &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_RequestTrailers{
						RequestTrailers: &extprocv3.TrailersResponse{
							HeaderMutation: hm,
						},
					},
				}
			}
			break

		default:
			log.Printf("Unknown Request type %v\n", v)
		}

		log.Printf("extprocv3.ProcessingResponse %v \n", resp)
		if err := srv.Send(resp); err != nil {
			log.Printf("send error %v", err)
		}
	}
}
