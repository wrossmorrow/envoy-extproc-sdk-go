package main

import (
	"io"
	"log"
	"strconv"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type extProcServer struct{}

func (s *extProcServer) Process(srv pb.ExternalProcessor_ProcessServer) error {

	log.Println("Got stream:  -->  ")
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
			r := req.Request
			h := r.(*pb.ProcessingRequest_RequestHeaders)
			//log.Printf("Got RequestHeaders.Attributes %v", h.RequestHeaders.Attributes)
			//log.Printf("Got RequestHeaders.Headers %v", h.RequestHeaders.Headers)

			isPOST := false
			for _, n := range h.RequestHeaders.Headers.Headers {
				if n.Key == ":method" && n.Value == "POST" {
					isPOST = true
					break
				}
			}

			for _, n := range h.RequestHeaders.Headers.Headers {
				log.Printf("Header %s %s", n.Key, n.Value)
				if n.Key == "user" && isPOST {
					log.Printf("Processing User Header")
					rhq := &pb.HeadersResponse{
						Response: &pb.CommonResponse{
							HeaderMutation: &pb.HeaderMutation{
								RemoveHeaders: []string{"content-length", "user"},
							},
						},
					}

					resp = &pb.ProcessingResponse{
						Response: &pb.ProcessingResponse_RequestHeaders{
							RequestHeaders: rhq,
						},
						ModeOverride: &v3.ProcessingMode{
							RequestBodyMode:    v3.ProcessingMode_BUFFERED,
							ResponseHeaderMode: v3.ProcessingMode_SKIP,
							ResponseBodyMode:   v3.ProcessingMode_NONE,
						},
					}
				}
			}
			break

		case *pb.ProcessingRequest_RequestBody:

			r := req.Request
			b := r.(*pb.ProcessingRequest_RequestBody)
			log.Printf("   RequestBody: %s", string(b.RequestBody.Body))
			log.Printf("   EndOfStream: %T", b.RequestBody.EndOfStream)
			if b.RequestBody.EndOfStream {

				bytesToSend := append(b.RequestBody.Body, []byte(` baaar `)...)
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_RequestBody{
						RequestBody: &pb.BodyResponse{
							Response: &pb.CommonResponse{
								HeaderMutation: &pb.HeaderMutation{
									SetHeaders: []*core.HeaderValueOption{
										{
											Header: &core.HeaderValue{
												Key:   "Content-Length",
												Value: strconv.Itoa(len(bytesToSend)),
											},
										},
									},
								},
								BodyMutation: &pb.BodyMutation{
									Mutation: &pb.BodyMutation_Body{
										Body: bytesToSend,
									},
								},
							},
						},
					},
					ModeOverride: &v3.ProcessingMode{
						ResponseHeaderMode: v3.ProcessingMode_SEND,
						ResponseBodyMode:   v3.ProcessingMode_NONE,
					},
				}
			}
			break

		case *pb.ProcessingRequest_ResponseHeaders:
			log.Printf("pb.ProcessingRequest_ResponseHeaders %v \n", v)
			r := req.Request
			h := r.(*pb.ProcessingRequest_ResponseHeaders)

			responseSize := 0
			for _, n := range h.ResponseHeaders.Headers.Headers {
				if n.Key == "content-length" {
					responseSize, _ = strconv.Atoi(n.Value)
					break
				}
			}

			log.Println("  Removing access-control-allow-* headers")
			rhq := &pb.HeadersResponse{
				Response: &pb.CommonResponse{
					HeaderMutation: &pb.HeaderMutation{
						RemoveHeaders: []string{"access-control-allow-origin", "access-control-allow-credentials"},
						SetHeaders: []*core.HeaderValueOption{
							{
								Header: &core.HeaderValue{
									Key:   "content-type",
									Value: "text/plain",
								},
							},
							{
								Header: &core.HeaderValue{
									Key:   "content-length",
									Value: strconv.Itoa(responseSize + len([]byte(` qux`))),
								},
							},
						},
					},
				},
			}
			resp = &pb.ProcessingResponse{
				Response: &pb.ProcessingResponse_ResponseHeaders{
					ResponseHeaders: rhq,
				},
				ModeOverride: &v3.ProcessingMode{
					ResponseBodyMode: v3.ProcessingMode_BUFFERED,
				},
			}
			break

		case *pb.ProcessingRequest_ResponseBody:
			log.Printf("pb.ProcessingRequest_ResponseBody %v \n", v)
			r := req.Request
			b := r.(*pb.ProcessingRequest_ResponseBody)
			if b.ResponseBody.EndOfStream {
				bytesToSend := append(b.ResponseBody.Body, []byte(` qux`)...)
				resp = &pb.ProcessingResponse{
					Response: &pb.ProcessingResponse_ResponseBody{
						ResponseBody: &pb.BodyResponse{
							Response: &pb.CommonResponse{
								BodyMutation: &pb.BodyMutation{
									Mutation: &pb.BodyMutation_Body{
										Body: bytesToSend,
									},
								},
							},
						},
					},
				}
			}

			break

		default:
			log.Printf("Unknown Request type %v\n", v)
		}

		if err := srv.Send(resp); err != nil {
			log.Printf("send error %v", err)
		}
	}
}
