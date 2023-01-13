package main

import (
	"flag"
	"regexp"
	"strings"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var (
	port = *flag.Int("port", 50051, "gRPC port (default: 50051)")
)

// type arrayFlags []string

// func (i *arrayFlags) String() string {
//     return "my string representation"
// }

// func (i *arrayFlags) Set(value string) error {
//     *i = append(*i, value)
//     return nil
// }

// var myFlags arrayFlags

// flag.Var(&myFlags, "list1", "Some description for this param.")

type echoRequestProcessor struct{}

func joinHeaders(mvhs map[string][]string) map[string]string {
	hs := make(map[string]string)
	for n, vs := range mvhs {
		hs[n] = strings.Join(vs, ",")
	}
	return hs
}

func (s echoRequestProcessor) PreprocessContext(ctx *ep.RequestContext) error {
	echoPathRx, _ := regexp.Compile("/echo/.*")
	ctx.SetValue("echoPath", echoPathRx)
	return nil
}

func (s echoRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {

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

func (s echoRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	match, _ := regexp.MatchString("/echo/.*", ctx.Path)
	if !match {
		return ctx.ContinueRequest()
	}
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

func main() {
	flag.Parse()

	eps := make(map[string]ep.RequestProcessor)
	eps["echo"] = echoRequestProcessor{}
	ep.Serve(port, eps)
}
