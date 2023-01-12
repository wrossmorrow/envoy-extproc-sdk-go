package main

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type trivialRequestProcessor struct{}

func (s trivialRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) error {
	ctx.AddHeader("x-extproc-request", "seen")
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s trivialRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body *pb.HttpBody) error {
	return ctx.ContinueRequest()
}

func (s trivialRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) error {
	return ctx.ContinueRequest()
}

func (s trivialRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) error {
	return ctx.ContinueRequest()
}

func (s trivialRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body *pb.HttpBody) error {
	ctx.AddHeader("x-extproc-response", "seen")
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s trivialRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) error {
	return ctx.ContinueRequest()
}
