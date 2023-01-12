
# An Envoy ExternalProcessor SDK (go)

## Overview

[`envoy`](https://www.envoyproxy.io/), one of the most powerful and widely used reverse proxies, is able to query an [ExternalProcessor](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter) gRPC service in it's filter chain. This functionality opens the door to quickly and robustly implemently customized functionality at the edge, instead of in targeted services. While powerful, implementing these services still requires dealing with complicated `envoy` specs, managing information sharing across request phases, and an understanding of gRPC, none of which are exactly straightforward. 

**The purpose of this SDK is to make development of ExternalProcessors (more) easy**. This SDK _certainly_ won't supply the most _performant_ edge functions. Much better performance will come from eschewing the ease-of-use functionality here by packing processor functions together in one filter or even _not using an ExternalProcessor at all_ but instead using a [WASM plugin](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/wasm/v3/wasm.proto) or registered [custom filter binary](https://github.com/envoyproxy/envoy-filter-example). Optimal performance isn't necessarily our goal; usability, maintainability, and low time-to-functionality is, and those aspects can often be more important than minimal request latency. 

### Usage

This SDK uses a `struct`
```go
type genericExtProcServer struct {
	name      string
	processor requestProcessor
}
```
and an interface 
```go
type requestProcessor interface {
	ProcessRequestHeaders(ctx *requestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessRequestBody(ctx *requestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessRequestTrailers(ctx *requestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error)
	ProcessResponseHeaders(ctx *requestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessResponseBody(ctx *requestContext, body *pb.HttpBody) (*pb.CommonResponse, *pb.ImmediateResponse, error)
	ProcessResponseTrailers(ctx *requestContext, trailers *pb.HttpTrailers) (*pb.HeaderMutation, error)
}
```
(as well as a context object and some helper functions) that work together to process requests and responses. A gRPC server is registered with, for example,  
```go
import "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

...

	extproc := &genericExtProcServer{
		name:      "trivial",
		processor: &trivialRequestProcessor{},
	}
	epb.RegisterExternalProcessorServer(s, extproc)
```
for a "known" `struct` `trivialRequestProcessor` implementing the `interface` `requestProcessor`. The `genericExtProcServer` handles the gRPC communication and shared context, parsing the processing phase in the gRPC stream and calling the right `interface` method. The header and body methods can return either a "common" or "immediate" response object (or error); the trailer methods can only mutate headers. 

The `requestContext` `struct` passed to any method is typed as follows:
```go
type requestContext struct {
	scheme    string // defined on RequestHeaders phase
	authority string // defined on RequestHeaders phase
	method    string // defined on RequestHeaders phase
	path      string // defined on RequestHeaders phase
	requestId string // defined on RequestHeaders phase
	started   int64 // defined on RequestHeaders phase
	duration  int64 // updated (accumulated) at every phase
	data      map[string]interface{} // user-defined data

	// methods: 
	// 
	// FormCommonResponse() (*pb.CommonResponse, error)
	// FormImmediateResponse(status int32, body string) (*pb.ImmediateResponse, error)
	// AddHeader(hm *pb.HeaderMutation, name string, value string, action string) error
	// AddHeaders(hm *pb.HeaderMutation, headers map[string]string, action string) error
	// RemoveHeader(hm *pb.HeaderMutation, name string) error
	// RemoveHeaders(hm *pb.HeaderMutation, headers ...string) error
}
```
As detailed in the comments, this context is initialized with request data when request headers are received (implying that the `envoy` configuration should always have `processing_mode.request_header_mode: SEND`). This context is carried through every request phase, meaning that data can be shared _across_ phases particularly in the generic slot (`data`) for arbitrary values. The methods annotated in comments provide some convenience routines for operating on request/response headers, so that users of this SDK need to learn less about the specifics of the `envoy` datastructures. For example, the following adds a request header seen by an upstream: 
```go
func (s *trivialRequestProcessor) ProcessRequestHeaders(ctx *requestContext, headers *pb.HttpHeaders) (*pb.CommonResponse, *pb.ImmediateResponse, error) {
	cr, _ := ctx.FormCommonResponse()
	ctx.AddHeader(cr.HeaderMutation, "x-extproc-request", "seen", "OVERWRITE_IF_EXISTS_OR_ADD")
	return cr, nil, nil
}
```
