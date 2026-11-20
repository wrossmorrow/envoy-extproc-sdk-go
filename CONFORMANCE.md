# Envoy Compatibility and `ext_proc` Feature Coverage

This page tracks the Envoy Proxy versions and `envoy.service.ext_proc.v3` features supported and verified by `envoy-extproc-sdk-go`.

Verification in this repository is intentionally split into two tiers:

- **Routine tests**: Fast unit/stream-fake tests (`go test -race ./...`) and the compact single-version integration smoke suite in [test/](test) run on every pull request in [.github/workflows/pull-request.yaml](.github/workflows/pull-request.yaml).
- **Purposeful conformance exercises**: Multi-version Envoy compatibility matrices, processing-mode permutations, HTTP/2 and trailer coverage, and new spec-extension probes live in an isolated [conformance/](conformance) suite executed intentionally (on demand, on release tags, on schedule, or on conformance changes via [.github/workflows/conformance.yaml](.github/workflows/conformance.yaml)) rather than blocking every routine commit. See [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md) for the full architecture and roadmap.

---

## Version Baseline

| Component | Current Pin / Target | Notes |
| :--- | :--- | :--- |
| Go Toolchain (`go.mod`) | `go 1.25.0` | Minimum module version in [go.mod](go.mod) |
| `go-control-plane/envoy` (`go.mod`) | `v1.37.0` | Compile-time `ext_proc` v3 protobuf definitions |
| `google.golang.org/grpc` (`go.mod`) | `v1.83.0` | Bidirectional gRPC stream server + health |
| Routine CI Envoy (`test/compose.yaml`) | `v1.35-latest` | Default tag in [test/compose.yaml](test/compose.yaml); override with `ENVOY_VERSION` |
| Multi-Version Matrix ([conformance/envoy-versions.json](conformance/envoy-versions.json)) | `v1.37` – `v1.40` + `dev` | Active 12-month supported Envoy minor releases plus `dev` canary |

> [!NOTE]
> Phases 1 and 2 of [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md) are implemented in [conformance/](conformance) and [.github/workflows/conformance.yaml](.github/workflows/conformance.yaml), covering active Envoy releases `v1.37`–`v1.40`. Phase 3 adds automated matrix reporting and release sync; Phase 4 tackles the flagged SDK gaps and new v3 spec features.

---

## `ext_proc` v3 Feature Coverage Matrix

| Feature / Capability | SDK Support | SDK Surface | Current Verification |
| :--- | :--- | :--- | :--- |
| Request & response header phase callbacks (`SEND`) | Supported | `RequestProcessor.ProcessRequestHeaders`, `ProcessResponseHeaders` | Unit + Routine Integration + Live Conformance (HTTP/1.1 & `h2c`, multi-value, `RawValues`, `SplitList`, `RequestContext` fields & cross-phase state) |
| Skip request headers (`request_header_mode: SKIP`) | Supported | `newRequestContext` initializes `data` map before header phase ([context.go](context.go)) | Live Conformance (`skip_request_headers_response_mutation`) |
| Header read representation (`AllHeaders`) | Supported | `AllHeaders.Get`, `Values`, `RawValues`, `Has`, `SplitList` ([headers.go](headers.go)) | Unit (`headers_test.go`) + Routine Integration (`TestHeaderParsing`) + Live Conformance |
| Header mutations (`ADD_IF_ABSENT`, `APPEND_IF_EXISTS_OR_ADD`, `OVERWRITE_IF_EXISTS_OR_ADD`, `OVERWRITE_IF_EXISTS`, remove) | Partial (Envoy quirk) | `AddHeader*`, `AppendHeader*`, `OverwriteHeader*`, `UpdateHeader*`, `RemoveHeader*` | Unit + Live Conformance (`AddHeader` on absent key, `OverwriteHeader`, `RemoveHeader`); live Envoy `v1.37`–`v1.40` ignores `append_action` (`envoyproxy/envoy#48886`) and falls back to deprecated `append=false` (overwrite), tracked via `CONFORMANCE_STRICT_APPEND_ACTION=1` |
| Body processing — `BUFFERED` | Supported | `ProcessRequestBody`, `ProcessResponseBody`, `ReplaceBody`, `ClearBody` | Unit + Routine Integration + Live Conformance (`buffered_body_replace_grow_and_shrink`, `buffered_body_clear`, `http1_chunked_response_buffered_replace_and_clear`) |
| Body processing — `STREAMED` & `BUFFERED_PARTIAL` | Supported | `ReplaceBodyChunk`, `ClearBodyChunk` | Unit + Live Conformance (`streamed_body_chunks`, `streamed_request_and_response_clear_chunk`, `buffered_partial_mode`) |
| Body mode `NONE` | Supported | `ProcessingMode` `NONE` skips body callbacks | Live Conformance (`get_nobody_mode_none`, `post_with_body_mode_none`) |
| Request & response trailers | Supported | `ProcessRequestTrailers`, `ProcessResponseTrailers`, trailer `HeaderMutation` | Unit (`server_test.go`, `phase_test.go`) + Live Conformance (`http1_trailers_roundtrip`, `h2c_trailers_roundtrip`) |
| Route cache re-evaluation (`clear_route_cache`) | Supported | `RequestContext.ClearRouteCache`, `UnClearRouteCache` | Unit + Routine Integration + Live Conformance (`clear_route_cache_upstream_switch`, `clear_route_cache_from_request_body`) |
| Immediate local responses (`ImmediateResponse`) | Partial | `RequestContext.CancelRequest` (`Status`, `Headers`, `Body`; `GrpcStatus` and `Details` not yet exposed; note Envoy `type.v3.StatusCode` omits `418`) | Unit + Routine Integration + Live Conformance (`immediate_response_request_headers`, `immediate_response_all_phases_and_trailers_fallback`) |
| Stream cancellation, timeouts, error handling & filter chaining | Supported | `codes.Canceled` handling via `ErrorHandler` ([extproc.go](extproc.go)), `AbortOnProcessorFailure`, `HealthServer`, `Serve` drain ([server.go](server.go)) | Unit (`server_test.go`, `health_test.go`) + Live Conformance (`downstream_client_cancel_graceful`, `message_timeout_fail_closed_and_fail_open`, `processor_error_continue_vs_abort`, `multi_filter_chaining`, `per_route_disabled_bypass`) |
| Processing metrics & timing headers | Supported | `Metrics` (`/metrics` OpenMetrics) ([metrics.go](metrics.go)), `UpdateExtProcHeader`, `UpdateDurationHeader` | Unit (`metrics_test.go`, `server_test.go`) + Live Conformance + Loadtest |
| Observability mode (`observability_mode: true`) | Protocol Gap | SDK currently always replies with `EmptyContinueResponse` when no response is set ([extproc.go](extproc.go)) | Tracked via `CONFORMANCE_STRICT_OBSERVABILITY=1` (`observability_mode_envoy_gap`) |
| Protocol configuration (`protocol_config`) | Not Yet Supported | `ProcessingRequest.protocol_config` is not yet surfaced on `RequestContext` | Tracked in roadmap |
| Dynamic mode override (`mode_override`) | Not Yet Supported | `ProcessingResponse.mode_override` | Tracked in roadmap |
| Dynamic metadata & attributes (`metadata_context`, `dynamic_metadata`, `attributes`) | Not Yet Supported | `ProcessingRequest` / `ProcessingResponse` metadata & CEL attribute fields | Tracked in roadmap |
| Per-message timeout override (`override_message_timeout`) | Not Yet Supported | `ProcessingResponse.override_message_timeout` | Tracked in roadmap |
| `CommonResponse_CONTINUE_AND_REPLACE` & `CommonResponse.trailers` | Not Yet Supported | Header-phase body/trailer replacement | Tracked in roadmap |
| `FULL_DUPLEX_STREAMED`, `StreamedImmediateResponse`, `RequestDrain`, `GRPC` body mode | Not Yet Supported | Requires a non-1:1 streaming model beyond `RequestProcessor` | Tracked in roadmap |

---

## Running Verification Today

Run the routine unit and stream-fake test suite (no Docker required):

```sh
just unit-test
# or with race detector (matches CI):
go test -v -race ./...
```

Run the routine Docker Compose integration suite (defaults to `ENVOY_VERSION=v1.35-latest`):

```sh
just integration-test
```

Override the Envoy container tag locally to exercise a newer Envoy release against the existing integration cases:

```sh
ENVOY_VERSION=v1.40-latest just integration-test
```

Run the purposeful multi-version conformance suite (requires Docker or `ENVOY_BIN`), or its fast zero-Docker harness self-tests:

```sh
cd conformance && go test -short -v ./...
just conformance
just conformance-version v1.40-latest
just conformance-matrix
```

Run the smoke load tests (full chain via k6, or direct gRPC via `loadgen`):

```sh
just loadtest-smoke
just loadtest-grpc-smoke
```

For the multi-version conformance design, dependency lifecycle policy, and reporting architecture, see [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md).
