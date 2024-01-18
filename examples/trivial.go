package main

import (
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type trivialRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func (s *trivialRequestProcessor) GetName() string {
	return "trivial"
}

func (s *trivialRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *trivialRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string, headerRawValues map[string][]byte) error {
	ctx.AddHeader("x-extproc-request", "seen")
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s *trivialRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	ctx.AddHeader("x-extproc-response", "seen")
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s *trivialRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *trivialRequestProcessor) Finish() {}
