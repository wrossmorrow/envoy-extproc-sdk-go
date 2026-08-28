package main

import (
	"encoding/json"
	"log"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type UpstreamBodySchema struct {
	Upstream string `json:"upstream"`
}

type routingRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func (s *routingRequestProcessor) GetName() string {
	return "routing"
}

func (s *routingRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *routingRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	log.Printf("Router got request headers")
	ctx.SetValue("routed", false)
	// parse if there is a routing header
	if upstream, ok := ctx.AllHeaders.Get("x-route-to-upstream"); ok {
		ctx.SetValue("routed", true)
		ctx.OverwriteHeader(":authority", ep.HeaderValue{RawValue: []byte(upstream)})
		ctx.ClearRouteCache()
		log.Printf("Router got routing request header, routing to %s", upstream)
	}

	response, _ := ctx.GetResponse(ctx.GetPhase())
	log.Printf("Phase %s, CommonResponse: %v", ctx.GetPhaseName(), response)

	return ctx.ContinueRequest()
}

func (s *routingRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	log.Printf("Router got request body")
	if routed, err := ctx.GetValue("routed"); err != nil || !routed.(bool) {
		// parse request body, assuming buffered and JSON in a specific schema declaring the upstream
		var parsedBody UpstreamBodySchema
		err = json.Unmarshal(body, &parsedBody)
		if err != nil {
			log.Printf("Could not parse request schema: %v", err)
		} else {
			ctx.SetValue("routed", true)
			ctx.OverwriteHeader(":authority", ep.HeaderValue{RawValue: []byte(parsedBody.Upstream)})
			ctx.ClearRouteCache()
			log.Printf("Router got routing request body field, routing to %s", parsedBody.Upstream)
		}
	}
	return ctx.ContinueRequest()
}

func (s *routingRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *routingRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *routingRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *routingRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *routingRequestProcessor) ErrorHandler(ctx *ep.RequestContext, phase int, err error) {
	log.Printf("Error in phase %s: %v", ep.RequestPhaseToString(phase), err)
}

func (s *routingRequestProcessor) Close(gracePeriodSeconds int32) error {
	log.Printf("Closing %s", s.GetName())
	return nil
}

func (s *routingRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}
