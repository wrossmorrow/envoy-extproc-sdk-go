# Migration notes

## v0.4.0 (from v0.3.x)

This release is purely additive and has no breaking source-level API changes — existing `RequestProcessor` implementations compile and run without modification.

### Dynamic processing mode overrides (`mode_override`)

Added per-request dynamic processing mode overrides for `REQUEST_HEADERS` and `RESPONSE_HEADERS` phases (requires `allow_mode_override: true` on Envoy's `ext_proc` filter):

- Exported `HeaderSendMode` (`HeaderModeDefault`, `HeaderModeSend`, `HeaderModeSkip`), `BodySendMode` (`BodyModeNone`, `BodyModeStreamed`, `BodyModeBuffered`, `BodyModeBufferedPartial`, `BodyModeFullDuplexStreamed`, `BodyModeGRPC`), `ModeOverride`, and `ModeOverride.ToEnvoyProcessingMode()`.
- Added `RequestContext.OverrideMode(m ModeOverride) error`, `RequestContext.ClearModeOverride()`, and `RequestContext.SkipRemainingPhases() error` (cleared between phases by `ResetPhase()` and omitted on `ImmediateResponse`).
- Updated `examples/masker.go` and `examples/envoy.yaml` to opt dynamically into `BodyModeBuffered` only for JSON payloads.
- **Envoy notes**: `BodySendMode` has no `DEFAULT` value (`BodyModeNone` is `0`). Additionally, when Envoy configures `allowed_override_modes`, Envoy resolves `DEFAULT` header/trailer fields in `response.mode_override` against the filter's static `processing_mode` before checking the allowlist, so `allowed_override_modes` entries must specify explicit `SEND`/`SKIP` values rather than `DEFAULT`.

### Stream protocol configuration (`protocol_config`)

- Added `ProtocolConfig` (`RequestBodyMode`, `ResponseBodyMode`, `SendBodyWithoutWaitingForHeaderResponse`) and `RequestContext.ProtocolConfig` (`*ProtocolConfig`), captured from the first `ProcessingRequest` of each stream (including under `request_header_mode: SKIP`) and retained across all phases of that stream.
- Added `RequestContext.DefaultModeOverride() ModeOverride`, pre-populated with Envoy's configured `RequestBodyMode` and `ResponseBodyMode` from `ProtocolConfig` so callers overriding a single body direction in `ProcessRequestHeaders` do not zero the other direction to `BodyModeNone` (`0`).

### Dynamic metadata and CEL attributes (`metadata_context`, `dynamic_metadata`, `attributes`)

- **Inbound (`RequestContext`)**: Added stream-accumulated `Metadata` (`map[string]map[string]any` for `metadata_options.forwarding_namespaces.untyped`), `TypedMetadata` (`map[string]*anypb.Any` for `metadata_options.forwarding_namespaces.typed`), and `Attributes` (`map[string]any` flattening evaluated `request_attributes` and `response_attributes` from Envoy's `"envoy.filters.http.ext_proc"` attribute map).
- **Outbound (`RequestContext`)**: Added per-phase `SetDynamicMetadata(namespace, key string, value any) error`, `SetDynamicMetadataNamespace(namespace string, values map[string]any) error` (all-or-nothing `structpb` validation), and `ClearDynamicMetadata()`, attached across all 6 phase responses as well as `ImmediateResponse` (`CancelRequest`).
- **Envoy notes**: Returned namespaces are ignored unless listed in `metadata_options.receiving_namespaces.untyped`, and `protobuf.Struct` encodes numeric values as `float64` when read back via `AsMap()`.

### Per-message timeout overrides (`override_message_timeout`)

- Added `RequestContext.OverrideMessageTimeout(timeout time.Duration) error`, which immediately transmits an interim PGV-valid `ProcessingResponse` mid-phase asking Envoy to replace the current phase's `message_timeout` timer (`1ms <= timeout <= max_message_timeout`, at most once per phase, and a safe no-op when `ObservabilityMode` is `true`).
- **Operational warning**: `ext_proc` executes inline on Envoy's request/response hot path. Extending per-message timeouts pins downstream streams and any `BUFFERED` bodies in memory, sends an extra gRPC message on the stream, and multiplies concurrent active streams under load (`ServerOptions.MaxConcurrentStreams`, Envoy cluster circuit breakers, and route timeouts must be sized accordingly). Envoy ignores overrides unless `max_message_timeout >= timeout` is configured (`0` by default = disabled).

## v0.3.0 (from v0.2.x)

This release has no breaking source-level API changes — existing `RequestProcessor` implementations compile without modification.

### Observability mode (`observability_mode: true`)

`RequestContext.ObservabilityMode` exposes `ProcessingRequest.observability_mode`. When `true`, processor callbacks run and SDK metrics are recorded while `GenericExtProcServer` suppresses `ProcessingResponse` sends (keeping Envoy's `spurious_msgs_received == 0`), synthetic response headers (`UpdateExtProcHeader`, `UpdateDurationHeader`), and `extproc_empty_responses` increments.

Note Envoy's `ExternalProcessor.observability_mode` requirement: Envoy only delivers body messages in observability mode when the body mode is `STREAMED`, `GRPC`, or `NONE` (not `BUFFERED`).

### Exported server constructor (`NewGenericExtProcServer`)

`NewGenericExtProcServer(processor, metrics, logger)` is now exported so callers can register `GenericExtProcServer` on custom `grpc.Server` instances or in-process test harnesses without calling `Serve`.

### Header append actions enforced (`AddHeader`, `AppendHeader`, `UpdateHeader`)

Envoy's `ext_proc` filter ignores `HeaderValueOption.append_action` and evaluates deprecated `HeaderValueOption.append` (defaulting to `false` / overwrite; see [`envoyproxy/envoy#48886`](https://github.com/envoyproxy/envoy/issues/48886)), which previously caused both `AddHeader` and `AppendHeader` to overwrite existing headers:

- `AppendHeader` / `AppendHeaders` (`APPEND_IF_EXISTS_OR_ADD`) now set `HeaderValueOption.Append = true` so Envoy appends values rather than overwriting (including multi-filter `x-extproc-names` and `x-extproc-duration-ns`).
- `AddHeader` / `AddHeaders` (`ADD_IF_ABSENT`) and `UpdateHeader(..., "OVERWRITE_IF_EXISTS")` are enforced against the phase headers observed on the stream. Callers that previously called `AddHeader` while unintentionally relying on overwriting an existing header should switch to `OverwriteHeader`. *(Under `request_header_mode: SKIP`, incoming request headers are not sent to the SDK, so request-header existence is not observed during `RequestBody`.)*

## v0.2.0 – v0.2.1 (from v0.1.x)

- **Graceful stream cancellation (`v0.2.0`)**: Mid-stream downstream or Envoy cancellations (`codes.Canceled` / `context.Canceled`) during `Recv`, `Send`, or idle `ctx.Done()` end the stream gracefully without incrementing error counters while still notifying `ErrorHandler` for per-request state cleanup.
- **Processing duration metric (`v0.2.1`)**: Added the `extproc_external_processing_seconds` native histogram measuring cumulative processor callback time per stream (distinct from `extproc_stream_duration_seconds`).

## v0.1.0 (from v0.0.x)

This release changes public API in several places and changes runtime behavior in a few more. Read the "Header representation" and "RequestProcessor" sections first; those affect every consumer.

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

Three behavior changes come with it:

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

### Body mutation split from `Content-Length`

`ReplaceBodyChunk` and `ClearBodyChunk` no longer touch `Content-Length`. They operate on the body of *one message*, and under a STREAMED body mode that message is only part of the body, so its length is not the message length. Setting the header there produced a wrong `Content-Length` on every streamed chunk.

Two whole-body methods now carry that behavior:

```go
// before: set Content-Length to len(body) regardless of processing mode
ctx.ReplaceBodyChunk(body)
ctx.ClearBodyChunk()

// after: pick by what you actually configured in envoy
ctx.ReplaceBody(body)      // BUFFERED - one message is the whole body; sets Content-Length
ctx.ClearBody()            // BUFFERED - clears and sets Content-Length: 0
ctx.ReplaceBodyChunk(body) // STREAMED - one chunk; leaves Content-Length alone
ctx.ClearBodyChunk()       // STREAMED - clears this chunk only
```

If your filter config uses `request_body_mode: BUFFERED` / `response_body_mode: BUFFERED` — which the examples and the test harness do — replace `ReplaceBodyChunk` with `ReplaceBody` and `ClearBodyChunk` with `ClearBody` to keep the old behavior. Under STREAMED, the chunk methods are what you wanted all along.

Note also that a chunked HTTP/1.1 message carries no `Content-Length`, so neither whole-body method produces a meaningful header in that case.

The `Content-Length` constant is also now lower cased (`content-length`), matching the header names envoy actually sends; the previous mixed-case key could fail to match the existing header when overwriting.

### Shutdown behavior

`Serve` now performs a real graceful shutdown on SIGTERM/SIGINT, fixing bugs in previous versions:

1. The gRPC health service starts reporting `NOT_SERVING`
2. It waits `UnreadyPropagationDelaySeconds` (default 5) so load balancers doing active health checking observe that
3. `GracefulStop` sends GOAWAY and waits for in-flight streams, bounded by `TerminationGracePeriodSeconds`, falling back to a hard `Stop`
4. The processor's `Close` runs last. Note it always runs after the server has stopped, whether streams drained or were aborted so it can flush or commit to any durable data layers (if any). Worst case, shutdown takes `UnreadyPropagationDelaySeconds + TerminationGracePeriodSeconds` before `Close` is called; 15s with defaults. A Kubernetes pod's own `terminationGracePeriodSeconds` (default 30) must exceed that plus however long your `Close` needs.

Two consequences: a `SIGTERM` now takes at least `UnreadyPropagationDelaySeconds` to return (set it to `0` in tests and local runs), and the health service now reports real status. Previously `Check` returned `SERVING` unconditionally, so `MarkUnready` had no observable effect.

### Metrics

Prometheus metrics are new in this release and are registered to a private registry, served on `MetricsHTTPPort`:

| Metric | Meaning |
| --- | --- |
| `extproc_active_streams` | gauge, streams in flight |
| `extproc_total_streams` | streams opened |
| `extproc_errored_streams` | streams that ended in error, counted once per stream |
| `extproc_phase_errors_total` | phase processing errors, may repeat within one stream |
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

Likewise `extproc_errored_streams` and `extproc_phase_errors_total` are different questions: the first is how many requests were affected, the second how many phases actually failed. With `AbortOnProcessorFailure: false` a stream continues past a failed phase, so one stream can contribute several phase errors. The ratio tells you whether you are seeing one pathological stream or a broad failure.

### Go version

The module now requires Go 1.25.
