package main

import (
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type echoRequestProcessor struct{}

func (s echoRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) error {
	switch ctx.Method {
	// cancel request when there is no body
	case "HEAD":
	case "OPTIONS":
	case "GET":
	case "DELETE":
		return ctx.CancelRequest(200, make(map[string]string), "")
	default: break
	}
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body *pb.HttpBody) error {
	return ctx.CancelRequest(200, make(map[string]string), string(body.Body))
}

func (s echoRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) error {
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) error {
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body *pb.HttpBody) error {
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) error {
	return ctx.ContinueRequest()
}
