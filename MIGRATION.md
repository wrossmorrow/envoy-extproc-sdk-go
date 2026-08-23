# Migration notes

## v0.1.0 (from v0.0.x)

This release changes public API in several places and changes runtime behaviour in a few more. Read the "Header representation" and "RequestProcessor" sections first; those affect every consumer.

### Envoy no longer sends `HeaderValue.value`

More recent Envoy releases populate `HeaderValue.raw_value` for ext_proc and leave `value` empty. This is a change in Envoy, not in this SDK, but it invalidated the SDK's original header handling code that keyed off which proto field was set and could see an empty result on every request.

If you are upgrading from an older Envoy at the same time, this is the change most likely to alter what your implementations observe.

### Header representation (`AllHeaders`)

`RawHeaders` changed element type, and both maps are now always populated:

```go
// before
type AllHeaders struct {
    Headers    map[string][]string  // populated only if HeaderValue.value was set
    RawHeaders map[string][]byte    // populated only if HeaderValue.raw_value was set
}

// after
type AllHeaders struct {
    Headers    map[string][]string  // always populated, one entry per occurrence
    RawHeaders map[string][][]byte  // always populated, one entry per occurrence
}
```

Three behaviour changes come with it:

- **Values are no longer split on commas.** Previously every value was passed through `strings.Split(v, ",")`, which unintentionally corrupted `Date`, `User-Agent`, `Cookie`, and any value containing a quoted comma. Use `SplitList(name)` where a comma delimited list is genuinely expected (`Accept`, `Cache-Control`, `Vary`).
- **Repeated headers are preserved.** Previously a repeated header overwrote the earlier value; `Set-Cookie` lost all but the last. Both maps now hold one entry per occurrence, in wire order.
- **Keys are lower cased on ingest**, matching what Envoy sends. (Consistency is the reason we're not just using `net/http` `Header`s which canonicalize the names.)

Prefer the new accessors over indexing the maps. They are case insensitive:

```go
v, ok := headers.Get("content-type")   // first value
vs := headers.Values("set-cookie")     // all values, wire order
raw := headers.RawValues("x-binary")   // [][]byte, for non-UTF-8 values
if headers.Has("authorization") { ... }
parts := headers.SplitList("accept")   // opt-in comma splitting
```

### `RequestProcessor` gained two methods

Every implementation must now add these methods, even if trivial:

```go
// called when the SDK hits an error during request processing;
// phase is one of the REQUEST_PHASE_* constants
ErrorHandler(ctx *RequestContext, phase int, err error)

// called on shutdown, after in-flight streams have drained
Close(gracePeriodSeconds int32) error
```

A no-op `Close` returning `nil` and an `ErrorHandler` that logs are starting points or maybe even enough. But if you (say) write to durable data layers you might wants commits or other flushes in `Close`. 

### `Serve` and `MustServe`

```go
// before
func Serve(port int, processor RequestProcessor)

// after
func Serve(serverOptions *ServerOptions, processor RequestProcessor, logger *slog.Logger) error
func MustServe(serverOptions *ServerOptions, processor RequestProcessor, logger *slog.Logger)
```

`Serve` now returns an error, takes a `*ServerOptions` (see below) instead of a bare port, and takes an `*slog.Logger`. `ProcessingOptions` is *not* a parameter: it is read from the processor's `GetOptions()` method.

### `ServerOptions` is new; `ProcessingOptions` changed

Server level settings moved out of `ProcessingOptions` into a new `ServerOptions` type, constructible from defaults, JSON, or YAML:

```go
sopts := extproc.NewDefaultServerOptions()
// ExtProcPort                    50051
// MetricsHTTPPort                9090
// MaxConcurrentStreams           1000
// UnreadyPropagationDelaySeconds 5
// TerminationGracePeriodSeconds  10
```

`ProcessingOptions` dropped `LogPhases` and `LogStream` — logging is now done through the `*slog.Logger` passed to `Serve`, at whatever level you configure, and gained `AbortOnProcessorFailure`. The constructor was renamed to match existence of two options types:

```go
// before
opts := extproc.NewDefaultOptions()
// after
opts := extproc.NewDefaultProcessingOptions()
```

### `HeaderValue` sends `raw_value`

`ToEnvoyHeaderValue` now always emits `raw_value`, converting `Value` to bytes when that is the field you set. Setting *both* `Value` and `RawValue` is now invalid and reported by `IsValid()`; previously both were passed through to Envoy, _which is not permitted by the proto_.

### Shutdown behaviour

`Serve` now performs a real graceful shutdown on SIGTERM/SIGINT, fixing bugs in previous versions:

1. The gRPC health service starts reporting `NOT_SERVING`
2. It waits `UnreadyPropagationDelaySeconds` (default 5) so load balancers doing active health checking observe that
3. `GracefulStop` sends GOAWAY and waits for in-flight streams, bounded by `TerminationGracePeriodSeconds`, falling back to a hard `Stop`
4. The processor's `Close` runs last. Note it always runs after the server has stopped, whether streams drained or were aborted so it can flush or commit to any durable data layers (if any). Up to `TerminationGracePeriodSeconds` may have already passed, so perhaps buffer further in kubernetes configurations.

Two consequences: a `SIGTERM` now takes at least `UnreadyPropagationDelaySeconds` to return (set it to `0` in tests and local runs), and the health service now reports real status. Previously `Check` returned `SERVING` unconditionally, so `MarkUnready` had no observable effect.

### Metrics

Prometheus metrics are new in this release and are registered to a private registry, served on `MetricsHTTPPort`:

| Metric | Meaning |
| --- | --- |
| `extproc_active_streams` | gauge, streams in flight |
| `extproc_total_streams` | streams opened |
| `extproc_errored_streams` | phase processing errors |
| `extproc_stream_duration_seconds` | native histogram |
| `extproc_total_request_headers` | request header phases |
| `extproc_request_body_messages_total` | request body messages |
| `extproc_total_request_trailers` | request trailer phases |
| `extproc_total_response_headers` | response header phases |
| `extproc_response_body_messages_total` | response body messages |
| `extproc_total_response_trailers` | response trailer phases |
| `extproc_request_body_bytes_total` | request body bytes |
| `extproc_response_body_bytes_total` | response body bytes |
| `extproc_empty_responses` | phases that produced no response |
| `extproc_response_send_errors` | failures sending to Envoy |

Note that `*_body_messages_total` counts _messages_ while `*_body_bytes_total` counts _bytes_; they are not two views of the same quantity.

### Go version

The module now requires Go 1.25.
