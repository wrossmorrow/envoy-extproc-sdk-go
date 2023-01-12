package main

import (
	"strconv"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type timingRequestProcessor struct{}

func (s timingRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) error {

	val := strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx.OverwriteHeader("x-extproc-started-ns", val)
	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body *pb.HttpBody) error {
	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) error {
	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers *pb.HttpHeaders) error {

	finished := time.Now()
	duration := time.Since(finished)

	ctx.AddHeader("x-extproc-finished-ns", strconv.FormatInt(finished.UnixNano(), 10))
	ctx.AddHeader("x-upstream-duration-ns", strconv.FormatInt(duration.Nanoseconds(), 10))

	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body *pb.HttpBody) error {

	finished := time.Now()
	duration := time.Since(finished)

	ctx.OverwriteHeader("x-extproc-finished-ns", strconv.FormatInt(finished.UnixNano(), 10))
	ctx.OverwriteHeader("x-upstream-duration-ns", strconv.FormatInt(duration.Nanoseconds(), 10))

	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers *pb.HttpTrailers) error {
	return ctx.ContinueRequest()
}
