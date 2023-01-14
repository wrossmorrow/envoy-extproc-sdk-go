
# An Envoy ExternalProcessor SDK (go)

## Overview

[`envoy`](https://www.envoyproxy.io/), one of the most powerful and widely used reverse proxies, is able to query an [ExternalProcessor](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter) in it's filter chain. Such a processor is a gRPC service that streams messages back and forth to modify HTTP requests being processed by `envoy`. This functionality opens the door to quickly and robustly implemently customized functionality at the edge, instead of in targeted services. While powerful, implementing these services still requires dealing with complicated `envoy` specs, managing information sharing across request phases, and an understanding of gRPC, none of which are exactly straightforward. 

**The purpose of this SDK is to make development of ExternalProcessors (more) easy**. This SDK _certainly_ won't supply the most _performant_ edge functions. Much better performance will come from eschewing the ease-of-use functionality here by using a [WASM plugin](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/wasm/v3/wasm.proto) or registered [custom filter binary](https://github.com/envoyproxy/envoy-filter-example). Optimal performance isn't necessarily our goal; usability, maintainability, and low time-to-functionality is, and those aspects can often be more important than minimal request latency.

We attempt to achieve this ease largely by masking some of the details behind the datastructures `envoy` uses, which are effective but verbose and idiosyncratic. Each request generates a bidirectional gRPC stream (with at most 6 messages) and sends, in turn, data concerning request headers, request body, request trailers, response headers, response body, and response trailers (if `envoy` is configured to send all phases). The idea here is to supply functions for each phase that operate on a context and more generically typed data suitable for each phase. (See details below.)

Several examples are provided here in the [examples](#examples), which can be reviewed to examine usage patterns. 

## Usage

### TL;DR

Implement the `extproc.RequestProcessor` interface, and pass an instance to the `extproc.Serve` function. 

### Details

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
    GetName() string
    GetOptions() *ProcessingOptions
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
    extproc.Serve(50051, myRequestProcessor{})
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

    ...

}
```
for `myRequestProcessor` implementing `RequestProcessor`. The `GenericExtProcServer` handles the gRPC streaming and shared context, parsing the processing phase in the gRPC stream and calling the right `RequestProcessor` method. The header and body messages can be responded to with either a "common" or "immediate" response object (or error); the trailer methods can only mutate headers. But that should be opaque to the user of this SDK; the `RequestContext` and `RequestProcessor` are more important. 

### Context Data

The `RequestContext` is initialized with request data when request headers are received, implying that the `envoy` configuration should always have `processing_mode.request_header_mode: SEND`. Basic request data (method, path etc) are only available in this phase. As shown in the spec above, this data includes
* the HTTP `Scheme`
* the `Authority` (host)
* the HTTP `Method`
* the URL `Path`
* `envoy`'s `RequestId` (a UUID from the `x-request-id` header)
* the request processing stream start time in `Started`
* an accumulator `Duration` _for the time spent in external processing_
* a flag `EndOfStream` from gRPC messages for header and body phases

This context is carried through every request phase, meaning that data can be shared _across_ phases particularly in the generic slot `data` for arbitrary values. Define and access data with `RequestContext.SetValue` and `RequestContext.GetValue` methods. Values are stored generically as `interface{}`, so to use them you must re-type the data upoin retrieval. See the [data](#data) or [digest](#digest) examples. 

### Forming Responses

We also provide some convenience routines for operating on process phase stream responses, so that users of this SDK need to learn less about the specifics of the `envoy` datastructures. The gRPC stream response datastructures are complicated, and our aim is to utilize the `RequestContext` to guard and simplify the construction of responses with a simpler user interface. 

In particular, the methods
```go
(rc *RequestContext) ContinueRequest() error
(rc *RequestContext) CancelRequest(status int32, headers map[string]string, body string) error
```
define request phase responses for "continuing" and "responding immediately". Note that "cancelling" does not mean request failure; just "we know the response now, and don't need to process further". See the [echo](#echo) example for "OK" (200) responses from cancelling. 

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

Two methods help modify bodies: 
```go
(rc *RequestContext) ReplaceBodyChunk(body []byte) error
(rc *RequestContext) ClearBodyChunk() error
```
These are the two options currently available in `envoy` ExtProcs: replace a chunk and clear the entire chunk. Note that with buffered bodies the "chunks" should be the entire body. See the [masker](#masker) example discussed below. 

## Examples

You can run all the examples with 
```shell
cd examples && just up
```
or if you don't use [`just`](https://github.com/casey/just), 
```shell
cd examples && docker-compose build && docker-compose up
```
The compose setup runs `envoy` (see `examples/envoy.yaml`), a mock echo server (see `examples/_mocks/echo`), and several implementations of ExtProcs based on the SDK. These implementations are described below. 

Here is some sample output with the compose setup running: 
```shell
$ curl localhost:8080/resource -X POST -H 'Content-type: text/plain' -d 'hello' -s -vvv | jq .
*   Trying ::1...
* TCP_NODELAY set
* Connected to localhost (::1) port 8080 (#0)
> POST /resource HTTP/1.1
> Host: localhost:8080
> User-Agent: curl/7.64.1
> Accept: */*
> Content-type: text/plain
> Content-Length: 5
> 
} [5 bytes data]
* upload completely sent off: 5 out of 5 bytes
< HTTP/1.1 200 OK
< date: Fri, 13 Jan 2023 19:52:19 GMT
< content-type: text/plain; charset=utf-8
< x-envoy-upstream-service-time: 3
< x-extproc-request-digest: 7894e8a366f3fd045ad54c8c99fe850f0ca8b753e8590e67bb32a8f732b91c7b
< x-extproc-custom-data: 39d0739f-da17-44c7-a864-5003ac20f509
< x-extproc-started-ns: 1673639539607694718
< x-extproc-finished-ns: 1673639539625057563
< x-upstream-duration-ns: 17362958
< x-extproc-response: seen
< x-extproc-names: noop
< x-extproc-duration-ns: 3408
< server: envoy
< transfer-encoding: chunked
< 
{ [399 bytes data]
* Connection #0 to host localhost left intact
* Closing connection 0
{
  "Datetime": "2023-01-13 19:52:19.62091161 +0000 UTC",
  "Method": "POST",
  "Path": "/resource",
  "Headers": {
    "Accept": "*/*,",
    "Content-Type": "text/plain,",
    "User-Agent": "curl/7.64.1,",
    "X-Envoy-Expected-Rq-Timeout-Ms": "15000,",
    "X-Extproc-Request": "seen,",
    "X-Extproc-Started-Ns": "1673639539607694718,",
    "X-Forwarded-Proto": "http,",
    "X-Request-Id": "00859d5c-7018-4629-b7e8-04878334c808,"
  },
  "Body": "hello"
}
```

### No-op

The `noopRequestProcessor` defined in `examples/noop.go` does absolutely nothing, except use the options. Verbose stream and phase logs are emitted, and headers `x-extproc-duration-ns` and `x-extproc-names` are added to the response to the client. These headers are not injected from the processor, but rather the SDK. 

### Trivial

The `trivialRequestProcessor` defined in `examples/trivial.go` does very little: adds a header to the request sent to an upstream target and a similar header in the response to the client that simply declare the request passed through the processor. 

### Timer

The `timerRequestProcessor` defined in `examples/timer.go` adds timing headers: one to the request sent to the upstream with the Unix UTC (ns) time when the request started processing, and similar started, finished, and duration headers to the response sent to the client. Note this ExtProc uses data stored in the request context _across phases_, but not _custom_ data. 

### Data

The `dataRequestProcessor` defined in `examples/data.go` stores custom data on the request headers phase and adds that data as a header to the response for the downstream client. 

### Digest

The `digestRequestProcessor` defined in `examples/digest.go` computes a digest of the request, using `<method>:<path>[:body]`, and passes that back to the request client in the response as a header. Such digests are useful when, for example, internally examining duplicate requests (though invariantly changing body bytes, e.g. reordering JSON fields, wouldn't show up as duplication in a hash). 

### Dedup

The `dedupRequestProcessor` defined in `examples/dedup.go` computes a digest of the request as above and uses that to reject requests when another request with the same digest is still in flight (i.e., not yet responded to). You can utilize the `?delay=<int>` query param to the proxied echo server to make one "long running" (`PUT`, `POST`, or `PATCH`) request in one terminal, and another similar request in another terminal and observe the second will have a 409 response. You can change the body in the second request and see it pass through. 

### Masker

The `maskerRequestProcessor` defined in `examples/masker.go` is an example of body modification with `RequestContext.ReplaceBodyChunk`. Basically, this ExtProc examines JSON request bodies (requiring buffered bodies) and masks (with `****` for simplicity) fields with paths matching a static spec. This mimics using edge functionality to protect client-side or server-side data. 

### Echo

The `echoRequestProcessor` defined in `examples/echo.go` is an example of using an ExtProc to _respond_ to a request. If the request path starts with `/echo`, this processor responds directly instead of sending the request on to the upstream target. 
