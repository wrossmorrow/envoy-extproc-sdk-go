package main

import (
	"flag"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var (
	port = *flag.Int("port", 50051, "gRPC port (default: 50051)")
)

type noopRequestProcessor struct{}

func (s noopRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s noopRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s noopRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s noopRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s noopRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s noopRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func main() {
	flag.Parse()

	eps := make(map[string]ep.RequestProcessor)
	eps["noop"] = noopRequestProcessor{}
	ep.Serve(port, eps)
}
