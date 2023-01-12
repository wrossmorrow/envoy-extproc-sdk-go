package main

import (
	"strconv"
	"time"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type timingRequestProcessor struct{}

func (s timingRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {

	ctx.OverwriteHeader("x-extproc-started-ns", strconv.FormatInt(ctx.Started.UnixNano(), 10))
	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {

	finished := time.Now()
	duration := time.Since(ctx.Started)

	ctx.AddHeader("x-extproc-finished-ns", strconv.FormatInt(finished.UnixNano(), 10))
	ctx.AddHeader("x-upstream-duration-ns", strconv.FormatInt(duration.Nanoseconds(), 10))

	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {

	finished := time.Now()
	duration := time.Since(ctx.Started)

	ctx.OverwriteHeader("x-extproc-finished-ns", strconv.FormatInt(finished.UnixNano(), 10))
	ctx.OverwriteHeader("x-upstream-duration-ns", strconv.FormatInt(duration.Nanoseconds(), 10))

	return ctx.ContinueRequest()
}

func (s timingRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}
