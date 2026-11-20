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
> Phases 1–3 of [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md) are complete: the [conformance/](conformance) harness, [conformance/features.yaml](conformance/features.yaml) manifest, report generator (`just conformance-report`), deterministic Docker/CI module checks, and automated Envoy release-window sync ([.github/workflows/envoy-versions-sync.yaml](.github/workflows/envoy-versions-sync.yaml)) are in place. Phase 4 covers the flagged SDK gaps and new v3 spec extensions.

---

## `ext_proc` v3 Feature Coverage Matrix

<!-- BEGIN GENERATED CONFORMANCE MATRIX -->

| Category | Capability | SDK Status | SDK Surface | v1.37-latest | v1.38-latest | v1.39-latest | v1.40-latest | Notes |
| :--- | :--- | :--- | :--- | :---: | :---: | :---: | :---: | :--- |
| Headers | Request & response header callbacks (SEND) | Supported | `RequestProcessor.ProcessRequestHeaders, ProcessResponseHeaders` | PASS (3/3) | PASS (3/3) | PASS (3/3) | PASS (3/3) | HTTP/1.1 & h2c; multi-value, RawValues, SplitList, RequestContext fields & cross-phase state |
| Headers | Skip request headers (request_header_mode: SKIP) | Supported | `newRequestContext` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | Response-only processing without initial request headers phase |
| Headers | Header read representation (AllHeaders) | Supported | `Get, Values, RawValues, Has, SplitList` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | Wire-order repeated headers, comma preservation, opt-in SplitList, raw non-UTF-8 bytes |
| Headers | Header mutations (add absent, overwrite, remove) | Supported | `AddHeader*, OverwriteHeader*, RemoveHeader*` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | HTTP/1.1 & h2c |
| Headers | Header append_action on existing keys (ADD_IF_ABSENT, APPEND) | Partial | `AddHeader*, AppendHeader*, UpdateHeader*` | KNOWN GAP | KNOWN GAP | KNOWN GAP | KNOWN GAP | Envoy ext_proc ignores append_action (envoyproxy/envoy#48886); gated by CONFORMANCE_STRICT_APPEND_ACTION=1 |
| Bodies | Body mode NONE (bodyless & pass-through) | Supported | `ProcessingMode NONE` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | GET without body and POST pass-through without invoking body callbacks |
| Bodies | Buffered body processing (grow, shrink, clear, chunked upstream) | Supported | `Process*Body, ReplaceBody, ClearBody` | PASS (3/3) | PASS (3/3) | PASS (3/3) | PASS (3/3) | Updates Content-Length and handles chunked HTTP/1.1 upstream assembly |
| Bodies | Streamed body processing (chunk replace & clear) | Supported | `ReplaceBodyChunk, ClearBodyChunk` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | Multi-chunk HTTP/1.1 and HTTP/2 DATA frames without mutating Content-Length |
| Bodies | Buffered-partial body processing | Supported | `Process*Body (BUFFERED_PARTIAL)` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | Verified both within and exceeding per_request_buffer_limit_bytes |
| Trailers | Request & response trailers (HTTP/1.1 & h2c) | Supported | `ProcessRequestTrailers, ProcessResponseTrailers` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | Chunked HTTP/1.1 & HTTP/2 trailer read/overwrite/add + counter metrics |
| Routing & Local Reply | Route cache re-evaluation (header & body phases) | Supported | `ClearRouteCache, UnClearRouteCache` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | Upstream re-routing via :authority mutation and ClearRouteCache |
| Routing & Local Reply | Immediate local responses & trailer fallback | Partial | `CancelRequest` | PASS (2/2) | PASS (2/2) | PASS (2/2) | PASS (2/2) | GrpcStatus & Details not yet exposed; Envoy StatusCode omits 418 |
| Routing & Local Reply | Per-route filter disable (ExtProcPerRoute) | Supported | `ExtProcPerRoute.disabled` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | Processor bypassed when disabled on route |
| Resilience & Chaining | Downstream client cancellation (send & recv) | Supported | `ErrorHandler (codes.Canceled)` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | Graceful teardown with zero errored-stream or phase-error counts |
| Resilience & Chaining | Per-message timeout (fail-closed & fail-open) | Supported | `message_timeout, failure_mode_allow` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | 504 when failure_mode_allow=false; upstream pass-through when true |
| Resilience & Chaining | Processor failure modes (continue vs abort) | Supported | `AbortOnProcessorFailure` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | Covers continue-on-error, abort fail-closed, and abort fail-open |
| Resilience & Chaining | Multi-filter ext_proc chaining | Supported | `GenericExtProcServer` | PASS (1/1) | PASS (1/1) | PASS (1/1) | PASS (1/1) | Sequential two-filter header, raw-byte, and body mutations |
| Spec Extensions | Observability mode (observability_mode: true) | Protocol Gap | `GenericExtProcServer.Process` | KNOWN GAP | KNOWN GAP | KNOWN GAP | KNOWN GAP | SDK currently sends EmptyContinueResponse; gated by CONFORMANCE_STRICT_OBSERVABILITY=1 |
| Spec Extensions | Protocol configuration (protocol_config) | Not Yet Supported | `ProcessingRequest.protocol_config` | — | — | — | — | Not yet surfaced on RequestContext |
| Spec Extensions | Dynamic mode override (mode_override) | Not Yet Supported | `ProcessingResponse.mode_override` | — | — | — | — | Not yet exposed |
| Spec Extensions | Dynamic metadata & CEL attributes | Not Yet Supported | `metadata_context, dynamic_metadata, attributes` | — | — | — | — | Not yet exposed |
| Spec Extensions | Per-message timeout override | Not Yet Supported | `override_message_timeout` | — | — | — | — | Not yet exposed |
| Spec Extensions | CONTINUE_AND_REPLACE & full-duplex streaming | Not Yet Supported | `CONTINUE_AND_REPLACE, FULL_DUPLEX_STREAMED, GRPC` | — | — | — | — | Requires non-1:1 streaming model beyond RequestProcessor |

<!-- END GENERATED CONFORMANCE MATRIX -->

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
just conformance-report
just sync-envoy-versions
```

Run the smoke load tests (full chain via k6, or direct gRPC via `loadgen`):

```sh
just loadtest-smoke
just loadtest-grpc-smoke
```

For the multi-version conformance design, dependency lifecycle policy, and reporting architecture, see [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md).
