
# An Envoy ExternalProcessor SDK (go)

## Overview

[`envoy`](https://www.envoyproxy.io/), one of the most powerful and widely used reverse proxies, is able to query an [ExternalProcessor](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter) in it's filter chain. Such a processor is a gRPC service that . This functionality opens the door to quickly and robustly implemently customized functionality at the edge, instead of in targeted services. While powerful, implementing these services still requires dealing with complicated `envoy` specs, managing information sharing across request phases, and an understanding of gRPC, none of which are exactly straightforward. 

**The purpose of this SDK is to make development of ExternalProcessors (more) easy**. This SDK _certainly_ won't supply the most _performant_ edge functions. Much better performance will come from eschewing the ease-of-use functionality here by using a [WASM plugin](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/wasm/v3/wasm.proto) or registered [custom filter binary](https://github.com/envoyproxy/envoy-filter-example). Optimal performance isn't necessarily our goal; usability, maintainability, and low time-to-functionality is, and those aspects can often be more important than minimal request latency.

We attempt to achieve this ease largely by masking some of the details behind the datastructures `envoy` uses, which are effective but verbose and idiosyncratic. Each request generates a bidirectional gRPC stream (with at most 6 messages) and sends, in turn, data concerning request headers, request body, request trailers, response headers, response body, and response trailers (if `envoy` is configured to send all phases). The idea here is to supply functions for each phase that operate on a context and more generically typed data suitable for each phase. (See details below.)

Several examples are provided here in `examples/**/main.go`, which can be reviewed to examine usage patterns. 

## Usage

This SDK uses a `struct`
```go
type GenericExtProcServer struct {
	name      string
	processor requestProcessor
}
```
an interface 
```go
type RequestProcessor interface {
	ProcessRequestHeaders(ctx *requestContext, headers map[string][]string) error
	ProcessRequestBody(ctx *requestContext, body []byte]) error
	ProcessRequestTrailers(ctx *requestContext, trailers map[string][]string) error
	ProcessResponseHeaders(ctx *requestContext, headers map[string][]string) error
	ProcessResponseBody(ctx *requestContext, body []byte) error
	ProcessResponseTrailers(ctx *requestContext, trailers map[string][]string) error
}
```
and a context object
```go
type RequestContext struct {
	Scheme      string
	Authority   string
	Method      string
	Path        string
	RequestId   string
	Headers     map[string][]string
	Started     time.Time
	Duration    time.Duration
	EndOfStream bool
	data        map[string]interface{}
	response    PhaseResponse
}
```
that work together to allow processing of requests and responses. An ExtProc service can be run with the `Serve` method as in
```go
import  "github.com/wrossmorrow/envoy-extproc-sdk-go"

func main() {
	eps := make(map[string]extproc.RequestProcessor)
	eps["myProcessor"] = myRequestProcessor{}
	extproc.Serve(50051, eps)
}
```
or directly if you want finer grained control with code like
```go
import (
	...
	"github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

func main() {

	...

	service := &extproc.GenericExtProcServer{
		name:      "trivial",
		processor: &myRequestProcessor{},
	}
	epb.RegisterExternalProcessorServer(s, service)

}
```
for a "known" `struct` `trivialRequestProcessor` implementing the `interface` `requestProcessor`. The `genericExtProcServer` handles the gRPC communication and shared context, parsing the processing phase in the gRPC stream and calling the right `interface` method. The header and body methods can return either a "common" or "immediate" response object (or error); the trailer methods can only mutate headers. 

The `RequestContext` is initialized with request data when request headers are received, implying that the `envoy` configuration should always have `processing_mode.request_header_mode: SEND`. Basic request data (method, path etc) are only available in this phase. 

This context is carried through every request phase, meaning that data can be shared _across_ phases particularly in the generic slot (`data`) for arbitrary values. Define and access data with `RequestContext.SetValue` and `RequestContext.GetValue` methods. 

### Forming Responses

Other methods provide some convenience routines for operating on request/response headers, so that users of this SDK need to learn less about the specifics of the `envoy` datastructures. In particular, the methods
```go
(rc *RequestContext) ContinueRequest() error
(rc *RequestContext) CancelRequest(status int32, headers map[string]string, body string) error
```
define request phase responses. The gRPC stream response datastructures are complicated, and our aim is to utilize the `RequestContext` to guard and simplify the construction of responses with a simpler user interface. 

### Modifying Headers

You can add headers to a response with the convenience methods 
```go
(rc *RequestContext) AppendHeader(name string, value string) error
(rc *RequestContext) AddHeader(name string, value string) error
(rc *RequestContext) OverwriteHeader(name string, value string) error
(rc *RequestContext) AppendHeaders(headers map[string]string) error
(rc *RequestContext) AddHeaders(headers map[string]string) error
(rc *RequestContext) OverwriteHeaders(headers map[string]string) error
```
where `Append` adds header values if they exist, `Add` adds a new value only if the header doesn't exist, and `Overwrite` will add or overwrite if a header exists. The `RequestContext` should keep track of these headers and include them in a `ContinueRequest` or `CancelRequest` call. 

Headers can be removed with the
```go
(rc *RequestContext) RemoveHeader(name string) error
(rc *RequestContext) RemoveHeaders(headers []string) error
(rc *RequestContext) RemoveHeadersVariadic(headers ...string) error
```
methods, requiring only names of headers to remove. 

### Modifying Bodies

TBD

## Examples

### No-op

The `noopRequestProcessor` defined in `examples/noop/main.go` does absolutely nothing, except print logs. 

### Trivial

The `trivialRequestProcessor` defined in `examples/trivial/main.go` does very little: adds a header to the request sent to an upstream target and a similar header in the response to the client that simply declare the request passed through the processor. 

### Timer

The `timerRequestProcessor` defined in `examples/timer/main.go` adds timing headers: one to the request sent to the upstream with the Unix UTC (ns) time when the request started processing, and similar started, finished, and duration headers to the response sent to the client. Note this ExtProc uses data stored in the request context _across phases_, but not _custom_ data. 

### Data

The `dataRequestProcessor` defined in `examples/data/main.go` stores custom data on the request headers phase and adds that data as a header to the response for the downstream client. 

### Echo

The `echoRequestProcessor` defined in `examples/echo/main.go` is an example of using an ExtProc to _respond_ to a request. If the request path starts with `/echo`, this processor responds directly instead of sending the request on to the upstream target. 
