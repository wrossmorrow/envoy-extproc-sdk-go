package main

import (
	"github.com/google/uuid"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type dataRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func (s *dataRequestProcessor) GetName() string {
	return "data"
}

func (s *dataRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *dataRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	ctx.SetValue("customId", uuid.New())
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s *dataRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *dataRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s *dataRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	id, _ := ctx.GetValue("customId")
	ctx.AddHeader("x-extproc-custom-data", (id.(uuid.UUID)).String())
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s *dataRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *dataRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s *dataRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *dataRequestProcessor) Finish() {}
