package main

import (
	"log"

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

func (s *trivialRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	ctx.AddHeader("x-extproc-request", ep.HeaderValue{RawValue: []byte("seen")})
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s *trivialRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	ctx.AddHeader("x-extproc-response", ep.HeaderValue{RawValue: []byte("seen")})
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s *trivialRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *trivialRequestProcessor) ErrorHandler(ctx *ep.RequestContext, phase int, err error) {
	log.Printf("Error in phase %s: %v", ep.RequestPhaseToString(phase), err)
}

func (s *trivialRequestProcessor) Close(gracePeriodSeconds int32) error {
	log.Printf("Closing %s", s.GetName())
	return nil
}

func (s *trivialRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *trivialRequestProcessor) Finish() {}
