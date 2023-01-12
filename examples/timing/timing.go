package main

import (
	"strconv"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type timingRequestProcessor struct{}

func (s timingRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {

	val := strconv.FormatInt(time.Now().UnixNano(), 10)
	cr, _ := ctx.FormCommonResponse() // TODO: don't ignore error
	ctx.AddHeader(cr.HeaderMutation, "x-extproc-started-ns", val, "OVERWRITE_IF_EXISTS_OR_ADD")
	return cr, nil, nil
}

func (s timingRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s timingRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error) {
	return &pb.HeaderMutation{}, nil
}

func (s timingRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	return &pb.CommonResponse{}, nil, nil
}

func (s timingRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error) {

	var val string

	finished := time.Now()
	duration := time.Since(finished)
	cr, _ := ctx.FormCommonResponse() // TODO: don't ignore error

	val = strconv.FormatInt(finished.UnixNano(), 10)
	ctx.AddHeader(cr.HeaderMutation, "x-extproc-finished-ns", val, "OVERWRITE_IF_EXISTS_OR_ADD")

	val = strconv.FormatInt(duration.Nanoseconds(), 10)
	ctx.AddHeader(cr.HeaderMutation, "x-upstream-duration-ns", val, "OVERWRITE_IF_EXISTS_OR_ADD")

	return cr, nil, nil

}

func (s timingRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error) {
	return &pb.HeaderMutation{}, nil
}
