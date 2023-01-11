package main

import (
	"io"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type requestProcessor interface {
	ProcessRequestHeaders(ctx *requestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessRequestBody(ctx *requestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessRequestTrailers(ctx *requestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error)
	ProcessResponseHeaders(ctx *requestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessResponseBody(ctx *requestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessResponseTrailers(ctx *requestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error)
}

type genericExtProcServer struct {
	name      string
	processor requestProcessor
}

func (s *genericExtProcServer) Process(srv pb.ExternalProcessor_ProcessServer) error {

	var (
		rc *requestContext
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

		resp := &pb.ProcessingResponse{}
		switch v := req.Request.(type) {

		case *pb.ProcessingRequest_RequestHeaders:
			log.Printf("pb.ProcessingRequest_RequestHeaders %v \n", v)
			h := req.Request.(*pb.ProcessingRequest_RequestHeaders).RequestHeaders

			// define the request context (requires not skipping request headers)
			rc, err = NewReqCtx(h.Headers)

			ps = time.Now()
			cr, ir, err := s.processor.ProcessRequestHeaders(rc, h)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else if ir != nil {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_RequestHeaders{
						RequestHeaders: &pb.HeadersResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *pb.ProcessingRequest_RequestBody:
			log.Printf("Processing Request Body %v \n", v)
			b := req.Request.(*pb.ProcessingRequest_RequestBody).RequestBody

			ps = time.Now()
			cr, ir, err := s.processor.ProcessRequestBody(rc, b)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else if ir != nil {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_RequestBody{
						RequestBody: &pb.BodyResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *pb.ProcessingRequest_RequestTrailers:
			log.Printf("Processing Request Trailers %v \n", v)
			t := req.Request.(*pb.ProcessingRequest_RequestTrailers).RequestTrailers

			ps = time.Now()
			hm, err := s.processor.ProcessRequestTrailers(rc, t)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_RequestTrailers{
						RequestTrailers: &pb.TrailersResponse{
							HeaderMutation: hm,
						},
					},
				}
			}
			break

		case *pb.ProcessingRequest_ResponseHeaders:
			log.Printf("Processing Response Headers %v \n", v)
			h := req.Request.(*pb.ProcessingRequest_ResponseHeaders).ResponseHeaders

			ps = time.Now()
			cr, ir, err := s.processor.ProcessResponseHeaders(rc, h)
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
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ResponseHeaders{
						ResponseHeaders: &pb.HeadersResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *pb.ProcessingRequest_ResponseBody:
			log.Printf("Processing Response Body %v \n", v)
			b := req.Request.(*pb.ProcessingRequest_ResponseBody).ResponseBody

			ps = time.Now()
			cr, ir, err := s.processor.ProcessResponseBody(rc, b)
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
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: ir,
					},
				}
			} else {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ResponseBody{
						ResponseBody: &pb.BodyResponse{
							Response: cr,
						},
					},
				}
			}
			break

		case *pb.ProcessingRequest_ResponseTrailers:
			log.Printf("Processing Response Trailers %v \n", v)
			t := req.Request.(*pb.ProcessingRequest_ResponseTrailers).ResponseTrailers

			ps = time.Now()
			hm, err := s.processor.ProcessResponseTrailers(rc, t)
			rc.duration += time.Since(ps).Nanoseconds()

			if err != nil {
				log.Printf("process error %v", err)
			} else {
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_RequestTrailers{
						RequestTrailers: &pb.TrailersResponse{
							HeaderMutation: hm,
						},
					},
				}
			}
			break

		default:
			log.Printf("Unknown Request type %v\n", v)
		}

		log.Printf("pb.ProcessingResponse %v \n", resp)
		if err := srv.Send(resp); err != nil {
			log.Printf("send error %v", err)
		}
	}
}
