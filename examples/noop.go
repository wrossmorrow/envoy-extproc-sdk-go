package main

import ep "github.com/wrossmorrow/envoy-extproc-sdk-go"

type noopRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func (s *noopRequestProcessor) GetName() string {
	return "noop"
}

func (s *noopRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *noopRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string, headerRawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *noopRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *noopRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *noopRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *noopRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *noopRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *noopRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *noopRequestProcessor) Finish() {}
