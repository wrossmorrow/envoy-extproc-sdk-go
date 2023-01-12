package main

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type noopRequestProcessor struct{}

func (s noopRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s noopRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s noopRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error) {
	return &pb.HeaderMutation{}, nil
}

func (s noopRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s noopRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s noopRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error) {
	return &pb.HeaderMutation{}, nil
}
