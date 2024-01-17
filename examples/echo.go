package main

import (
	"regexp"
	"strings"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type echoRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func joinHeaders(mvhs map[string][]string) map[string]string {
	hs := make(map[string]string)
	for n, vs := range mvhs {
		hs[n] = strings.Join(vs, ",")
	}
	return hs
}

func (s *echoRequestProcessor) GetName() string {
	return "echo"
}

func (s *echoRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *echoRequestProcessor) PreprocessContext(ctx *ep.RequestContext) error {
	echoPathRx, _ := regexp.Compile("/echo/.*")
	ctx.SetValue("echoPath", echoPathRx)
	return nil
}

func (s *echoRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	match, _ := regexp.MatchString("/echo/.*", ctx.Path)
	if !match {
		return ctx.ContinueRequest()
	}

	if ctx.EndOfStream {
		return ctx.CancelRequest(200, joinHeaders(ctx.Headers), "")
	}
	return ctx.ContinueRequest()

	// switch ctx.Method {
	// // cancel request when there is no body
	// case "HEAD", "OPTIONS", "GET", "DELETE":
	// 	return ctx.CancelRequest(200, joinHeaders(ctx.Headers), "")
	// default:
	// 	break
	// }
	// return ctx.ContinueRequest()
}

func (s *echoRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	match, _ := regexp.MatchString("/echo/.*", ctx.Path)
	if !match {
		return ctx.ContinueRequest()
	}
	return ctx.CancelRequest(200, joinHeaders(ctx.Headers), string(body))
}

func (s *echoRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s *echoRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s *echoRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return ctx.ContinueRequest()
}

func (s *echoRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s *echoRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *echoRequestProcessor) Finish() {}
