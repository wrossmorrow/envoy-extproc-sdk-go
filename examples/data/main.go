package main

import (
	"flag"

	"github.com/google/uuid"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var (
	port = *flag.Int("port", 50051, "gRPC port (default: 50051)")
)

type dataRequestProcessor struct{}

func (s dataRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	ctx.SetValue("customId", uuid.New())
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s dataRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s dataRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s dataRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	id, _ := ctx.GetValue("customId")
	ctx.AddHeader("x-extproc-custom-data", (id.(uuid.UUID)).String())
	return ctx.ContinueRequest() // returns an error if response malformed
}

func (s dataRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s dataRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func main() {
	flag.Parse()

	eps := make(map[string]ep.RequestProcessor)
	eps["data"] = dataRequestProcessor{}
	ep.Serve(port, eps)
}
