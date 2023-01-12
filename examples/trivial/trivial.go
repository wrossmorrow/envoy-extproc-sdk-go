package main

import (
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type trivialRequestProcessor struct{}

func (s trivialRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	ctx.AddHeader("x-extproc-request", "seen")
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s trivialRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s trivialRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s trivialRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s trivialRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	ctx.AddHeader("x-extproc-response", "seen")
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s trivialRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}
