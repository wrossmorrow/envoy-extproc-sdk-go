package main

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type trivialRequestProcessor struct{}

func (s trivialRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	cr, _ := ctx.FormCommonResponse() // TODO: don't ignore error
	ctx.AddHeader(cr.HeaderMutation, "x-extproc-request", "seen", "OVERWRITE_IF_EXISTS_OR_ADD")
	return cr, nil, nil
}

func (s trivialRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s trivialRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error) {
	return &pb.HeaderMutation{}, nil
}

func (s trivialRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s trivialRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	cr, _ := ctx.FormCommonResponse() // TODO: don't ignore error
	ctx.AddHeader(cr.HeaderMutation, "x-extproc-response", "seen", "OVERWRITE_IF_EXISTS_OR_ADD")
	return cr, nil, nil
}

func (s trivialRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error) {
	return &pb.HeaderMutation{}, nil
}
