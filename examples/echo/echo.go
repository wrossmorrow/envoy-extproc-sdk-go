package main

import (
	"log"
	"strings"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type echoRequestProcessor struct{}

func joinHeaders(mvhs map[string][]string) map[string]string {
	hs := make(map[string]string)
	for n, vs := range mvhs {
		hs[n] = strings.Join(vs, ",")
	}
	return hs
}

func (s echoRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	log.Printf("Method: %s", ctx.Method)

	switch ctx.Method {
	// cancel request when there is no body
	case "HEAD", "OPTIONS", "GET", "DELETE":
		return ctx.CancelRequest(200, joinHeaders(ctx.Headers), "")
	default:
		break
	}
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.CancelRequest(200, joinHeaders(ctx.Headers), string(body))
}

func (s echoRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s echoRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}
