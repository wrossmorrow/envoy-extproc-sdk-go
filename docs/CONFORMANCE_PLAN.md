# Envoy Conformance and Feature Coverage Plan

This document outlines a path for expanding `envoy-extproc-sdk-go` from single-version integration checks into an extensible, multi-version Envoy conformance and `ext_proc` v3 feature-coverage suite.

---

## 1. Goals & Core Testing Philosophy: Routine vs. Purposeful Conformance

Everyday development and pull-request validation have different priorities from multi-version Envoy compatibility verification:

- **Routine tests** must remain fast, small, and deterministic. This covers the Go unit and stream-fake tests (`go test -race ./...`), static checks (`go vet` with `shadow`, build checks), and the compact single-version integration smoke suite in [test/](../test) run by [.github/workflows/pull-request.yaml](../.github/workflows/pull-request.yaml).
- **Conformance and feature-extension exercises** should live separately from routine tests and be **purposefully executed**. Multi-version Envoy container matrices, processing-mode permutations, HTTP/2 and trailer exercises, and protocol feature probes are heavier and should not slow down everyday commits.

Concretely, conformance exercises belong in an isolated top-level `conformance/` directory with:

- Its own Go module (`conformance/go.mod` using `replace github.com/wrossmorrow/envoy-extproc-sdk-go => ../`) so test-harness dependencies never leak into SDK consumers.
- Dedicated `just` recipes (`just conformance`, `just conformance-version <tag>`, `just conformance-matrix`) in [justfile](../justfile).
- A dedicated GitHub Actions workflow (`.github/workflows/conformance.yaml`) triggered purposefully: manually via `workflow_dispatch`, on release tags (`v*`), on a weekly schedule (including an `envoyproxy/envoy:dev` canary leg), when `conformance/**` changes, or on PRs carrying an explicit opt-in label (e.g., `run-conformance`).

---

## 2. Current Baseline & Structural Locks

The repository already has solid unit coverage over phases, headers, metrics, options, health checks, and stream cancellation ([phase_test.go](../phase_test.go), [server_test.go](../server_test.go)), plus a Docker Compose integration stack ([test/](../test)) and k6 / plain-gRPC load tests ([loadtest/](../loadtest)). Several structural locks currently limit conformance coverage:

1. **Pinned, EOL Envoy Version**: Both [test/compose.yaml](../test/compose.yaml) and [examples/compose.yaml](../examples/compose.yaml) default to `envoyproxy/envoy:v1.35-latest`. Under Envoy's 12-month release lifecycle, `v1.35` is end-of-life; the active supported minor releases as of late 2026 are `v1.37`, `v1.38`, `v1.39`, and `v1.40`. CI in [.github/workflows/pull-request.yaml](../.github/workflows/pull-request.yaml) tests only that single default tag.
2. **Why `ext_proc` v3 Backwards Compatibility Is Necessary but Not Sufficient**: Envoy's `envoy.service.ext_proc.v3` protobuf contract guarantees wire compatibility, which keeps baseline SDK framing intact across releases. However, Envoy runtime behavior on existing fields still shifts across minor versions—for example, Envoy moving from `HeaderValue.value` to `HeaderValue.raw_value` (documented in [MIGRATION.md](../MIGRATION.md)), stricter `content-length` matching on `BUFFERED` body mutations, and mid-stream reset or cancellation timing edge cases.
3. **Single `ProcessingMode` Configuration**: [test/envoy.yaml](../test/envoy.yaml) configures only one static mode (`request_header_mode: SEND`, `response_header_mode: SEND`, `request_body_mode: BUFFERED`, `response_body_mode: BUFFERED`, `request_trailer_mode: SKIP`, `response_trailer_mode: SKIP`) over HTTP/1.1.
4. **Action-in-Body Test Coupling**: [test/tester.go](../test/tester.go) unmarshals `TesterRequestBody` from the HTTP JSON request body to decide what mutations to apply. That design cannot exercise `GET` or bodyless requests, `request_body_mode: NONE`, non-JSON or binary payloads, multi-chunk `STREAMED` body processing, or `request_header_mode: SKIP`.

---

## 3. Functional Testing Needs

Functional verification falls into two parts: **(A) Conformance of features the SDK already implements**, and **(B) `ext_proc` v3 spec feature coverage** to track protocol gaps and future SDK extensions.

### Part A — Protocol Conformance of Current SDK Surface

1. **Close Existing Integration Gaps ([test/tester_test.go](../test/tester_test.go), [test/cases.yaml](../test/cases.yaml))**:
   - **Body replacement size changes (`ReplaceBody`)**: Both growing and shrinking request and response bodies in `BUFFERED` mode, confirming Envoy accepts the SDK's updated `content-length` header and delivers the full mutated body (currently `TODO` in [test/tester_test.go](../test/tester_test.go)).
   - **`ImmediateResponse` (`CancelRequest`) across all 4 valid phases**: Exercising `CancelRequest` from `ProcessRequestHeaders`, `ProcessRequestBody`, `ProcessResponseHeaders`, and `ProcessResponseBody`, and asserting status codes, custom response headers, and body bytes end-to-end.
   - **Header mutation actions & edge cases**: Exercising `ADD_IF_ABSENT` (both when the header is already present and when absent), `APPEND_IF_EXISTS_OR_ADD`, `OVERWRITE_IF_EXISTS_OR_ADD`, `OVERWRITE_IF_EXISTS` (via `UpdateHeader`), repeated headers such as `Set-Cookie` preserved in wire order, non-UTF-8 `RawValue` bytes, and `:authority` routing mutations with `ClearRouteCache`.
2. **`ProcessingMode` Permutations**:
   - `NONE`, `BUFFERED`, `STREAMED` (multi-chunk `ReplaceBodyChunk` and `ClearBodyChunk` where `EndOfStream` is `true` only on the final chunk and `content-length` is untouched), and `BUFFERED_PARTIAL`.
   - `request_header_mode: SKIP` (verifying `newRequestContext` in [context.go](../context.go) supports response-only processing without `initReqCtx`, and aligning [README.md](../README.md) with that support).
   - `request_trailer_mode: SEND` and `response_trailer_mode: SEND` (reading and mutating trailers in `ProcessRequestTrailers` and `ProcessResponseTrailers`).
3. **HTTP Codec & Transport Variants**:
   - **HTTP/1.1**: explicit `Content-Length` vs. `Transfer-Encoding: chunked` (confirming `ReplaceBody` / `ClearBody` behavior on chunked responses).
   - **HTTP/2 (`h2c` / TLS ALPN)**: pseudo-headers (`:scheme`, `:authority`, `:method`, `:path`), `DATA` frame `END_STREAM` signaling, HTTP/2 trailers, and gRPC pass-through traffic.
4. **Resilience, Lifecycle & Filter Configurations Against Live Envoy**:
   - **Mid-stream downstream disconnect**: Aborting an HTTP client mid-request and mid-response against live Envoy, asserting the SDK hits its graceful `codes.Canceled` branch in [extproc.go](../extproc.go), invokes `ErrorHandler`, and records zero `extproc_errored_streams` / `extproc_phase_errors_total`.
   - **`message_timeout` expiration**: Testing both `failure_mode_allow: false` (Envoy returns error status) and `failure_mode_allow: true` (Envoy fails open and proxies to upstream).
   - **`AbortOnProcessorFailure`**: Testing `true` (gRPC error aborts stream) vs. `false` (stream logs phase error and continues).
   - **Routing & filter chaining**: `ExtProcPerRoute` (`disabled: true` and per-route `processing_mode` overrides) and multi-filter `ext_proc` chains updating `x-extproc-names`.

### Part B — Envoy `ext_proc` v3 Spec Feature Audit & Extension Roadmap

Auditing [extproc.go](../extproc.go) and [context.go](../context.go) against `github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3` (`v1.37.0`) highlights the `ext_proc` v3 protocol surface that a feature matrix should track:

| Capability (`ext_proc` v3) | Current SDK Status | Gap / Conformance Notes |
| :--- | :--- | :--- |
| 6 phase callbacks, `HeaderMutation`, `ClearRouteCache` | Supported | Needs multi-mode, HTTP/2, and trailer end-to-end coverage |
| `BodyMutation` (`Body`, `ClearBody`) | Supported | Needs `STREAMED` chunking & `BUFFERED` size-change E2E tests |
| `ImmediateResponse` (`Status`, `Headers`, `Body`) | Supported (partial) | Missing `GrpcStatus` and `Details` (`%RESPONSE_CODE_DETAILS%`) |
| `ObservabilityMode` (`ProcessingRequest.observability_mode`) | **Protocol Hazard** | When `true`, Envoy forbids sending a `ProcessingResponse`; [extproc.go](../extproc.go) currently sends `EmptyContinueResponse` |
| `ProtocolConfiguration` (`ProcessingRequest.protocol_config`) | Ignored | Sent on the first message in newer Envoys (`RequestBodyMode`, `ResponseBodyMode`, `SendBodyWithoutWaitingForHeaderResponse`); exposing it on `RequestContext` helps detect `BUFFERED` vs. `STREAMED` |
| `ModeOverride` (`ProcessingResponse.mode_override`) | Not implemented | High-value capability (`allow_mode_override` / `allowed_override_modes` in Envoy 1.33–1.40) for dynamic per-request body/trailer toggling |
| `MetadataContext` & `DynamicMetadata` | Not implemented | High-value for reading `metadata_context` and writing `dynamic_metadata` (`metadata_options`) |
| `Attributes` (`request_attributes` / `response_attributes`) | Not implemented | Read-only CEL/connection attributes on `ProcessingRequest.attributes` |
| `OverrideMessageTimeout` | Not implemented | Allows extending per-phase timeout dynamically up to `max_message_timeout` |
| `CommonResponse_CONTINUE_AND_REPLACE` (+ `Trailers`) | Not implemented | Replace body/trailers directly during headers phase without separate body turns |
| `FULL_DUPLEX_STREAMED`, `StreamedImmediateResponse`, `RequestDrain`, `GRPC` body mode | Not implemented | Breaks the 1-request-to-1-response loop in [extproc.go](../extproc.go); evaluate behind a dedicated streaming interface rather than overloading `RequestProcessor` |

---

## 4. Testing Approach & Platform Architecture (`conformance/`)

### Inverted In-Process Topology

Instead of building and coordinating four Docker containers for every Envoy version and filter configuration via Docker Compose, invert the harness so **only Envoy runs out-of-process**:

```text
[ go test runner (HTTP/1.1, h2c, gRPC test client) ]
        │
        ├── HTTP/1.1 or h2c ──► [ Envoy (envoyproxy/envoy:vX.Y or ENVOY_BIN) ]
        │                             │
        │        ext_proc gRPC ───────┴──► [ In-process SDK GenericExtProcServer (:ephemeral) ]
        │
        └── proxy target ────────────────► [ In-process Go Upstream (:ephemeral, h2c + trailers + chunks) ]
```

Benefits of this architecture:

1. **Zero container image builds**: No `Dockerfile` build step or external echo server image is required during conformance runs. Both the SDK server and the upstream HTTP/1.1 + `h2c` server run inside the `go test` process on ephemeral ports, giving tests direct programmatic control over upstream trailers, streamed chunk delays, status codes, and connection aborts.
2. **Header/ID-driven case dispatch**: Register per-case processor behavior in an in-process Go registry keyed by a request header (e.g., `x-conformance-case: <id>`). This decouples test routing from the HTTP body and immediately enables `GET`/bodyless requests, arbitrary binary payloads, `STREAMED` chunks, and `request_body_mode: NONE`.
3. **Single Envoy startup per version**: Generate Envoy's bootstrap YAML/JSON in Go from `go-control-plane` v3 structs serialized via `protojson` (which also verifies v3 config schema compatibility). Use one Envoy process per matrix version with:
   - Per-route `ExtProcPerRoute` overrides on `/mode/buffered/*`, `/mode/streamed/*`, `/mode/none/*`, `/mode/trailers/*`, `/mode/skip-req-headers/*`, and `/mode/disabled/*`.
   - Separate listeners/filter chains for filter-wide settings that `ExtProcPerRoute` cannot override (e.g., `observability_mode: true`, `allow_mode_override: true`, `failure_mode_allow: true`, short `message_timeout`, multi-filter chaining).
4. **Dual `EnvoyRunner` Abstraction**:
   - **Container runner (default, cross-platform)**: Launches `envoyproxy/envoy:${ENVOY_VERSION}` via Docker (either lightweight `docker run` or `testcontainers-go` isolated inside `conformance/go.mod`), using `host.docker.internal` (with `--add-host=host.docker.internal:host-gateway` on Linux) so macOS Apple Silicon (`linux/arm64`) and Linux CI (`linux/amd64`) execute identically.
   - **Native binary / `func-e` runner (`ENVOY_BIN` opt-in)**: Spawns a local Envoy binary directly against `127.0.0.1` when available on Linux/CI for ~200ms startup with zero Docker overhead.
5. **Fast Protocol Invariant Checks**:
   - **Protobuf PGV validation**: `go-control-plane` generates `ValidateAll()` (via `protoc-gen-validate`) on every `ext_proc` message. Calling `resp.ValidateAll()` in unit and wire tests ([server_test.go](../server_test.go), [phase_test.go](../phase_test.go)) catches invalid outbound `ProcessingResponse` structures in milliseconds without running Envoy.
   - **Envoy Admin Stats assertions**: After conformance cases (or suites), query Envoy's admin endpoint (`:9901/stats?filter=ext_proc&format=json`) and assert `spurious_msgs_received == 0` and zero unexpected `streams_failed`. This catches silent protocol errors that `failure_mode_allow: true` or fallback paths might otherwise hide.

### Separation in Local Workflows and CI

- **Local `justfile` recipes**:
  - Routine work continues using `just test`, `just unit-test`, and `just integration-test`.
  - Purposeful conformance uses dedicated recipes:
    - `just conformance` (runs `conformance/` against the current default supported Envoy version),
    - `just conformance-version v1.40-latest` (runs against a specific Envoy tag or `ENVOY_BIN`),
    - `just conformance-matrix` (loops across all versions listed in `conformance/envoy-versions.json`).
- **GitHub Actions workflows**:
  - Keep [.github/workflows/pull-request.yaml](../.github/workflows/pull-request.yaml) focused on `build-check`, `vet-check`, `unit-test`, and the fast single-version `integration-test` smoke run.
  - Add `.github/workflows/conformance.yaml` with purposeful triggers:
    - `workflow_dispatch` (with inputs for `envoy_version` / `all` and optional `-run` Go test filter),
    - `schedule` (weekly cron running the full supported matrix plus `dev` canary),
    - `push.tags: ['v*']` (release verification gate),
    - `pull_request` **only** when `conformance/**` changes or an opt-in label (e.g., `run-conformance`) is applied.

---

## 5. Dependency Management Needs & Improvements

### Decouple Compile-Time API Version from Runtime Envoy Matrix

- **Compile-time (`go-control-plane/envoy`)**: Root [go.mod](../go.mod) pins one recent `github.com/envoyproxy/go-control-plane/envoy` release (`v1.37.0` currently) to provide the v3 service and filter protobuf definitions.
- **Runtime conformance manifest (`conformance/envoy-versions.json`)**: Store the runtime Envoy target matrix in one declarative file rather than hardcoding versions inside YAML workflows or Compose files:

```json
{
  "default": "v1.40-latest",
  "supported": [
    "v1.37-latest",
    "v1.38-latest",
    "v1.39-latest",
    "v1.40-latest"
  ],
  "canary": [
    "dev"
  ]
}
```

GitHub Actions reads `conformance/envoy-versions.json` via `fromJson` to build `strategy.matrix.envoy`, so adding `v1.41-latest` and retiring `v1.37-latest` requires updating only this JSON file.

### MVS Discipline & Multi-Module Hygiene

- **Protect downstream consumers from MVS floor bumps**: Avoid running blanket `go get -u` ([justfile](../justfile)) against root [go.mod](../go.mod) unless a bugfix or new API field specifically requires it; bumping `google.golang.org/grpc`, `google.golang.org/protobuf`, or the `go` directive in the root module forces every SDK consumer onto that minimum version under Go Minimal Version Selection (MVS).
- **Isolate test-only dependencies**: Keep `testcontainers-go`, `h2c`/test utilities, and report tooling strictly in `conformance/go.mod` (or `test/go.mod`), never in root [go.mod](../go.mod).
- **Go toolchain matrix**: Run root `unit-test` across the `go.mod` floor (`1.25`) and active supported Go releases (`1.26` and `1.27`).
- **Keep module tidy in sync**: Add a `just tidy-all` recipe and a CI drift check (`go mod tidy && git diff --exit-code`) across root, `examples/`, `test/`, and `conformance/`.
- **Deterministic Dockerfiles**: Replace `RUN go mod tidy && go mod download` in [examples/Dockerfile](../examples/Dockerfile) and [test/Dockerfile](../test/Dockerfile) with `RUN go mod download` and `go build -mod=readonly` so container builds cannot silently mutate module graphs.
- **Pin external tools**: Pin `grafana/k6` in [justfile](../justfile) and pin `shadow` / `golangci-lint` versions in CI rather than pulling `:latest` / `@latest`.

### Automated Extensibility to New Envoy Releases

1. **Renovate (or Dependabot)**: Configure grouped dependency PRs for `go-control-plane`, `grpc`, and GitHub Actions so API updates stay batched and reviewable.
2. **Scheduled Envoy Release Sync Workflow (weekly cron)**:
   - Query `https://endoflife.date/api/envoy.json` (or `envoyproxy/envoy` GitHub Releases) to determine the active 12-month supported Envoy minor versions.
   - Update `conformance/envoy-versions.json` (and optionally the default `ENVOY_VERSION` in [test/compose.yaml](../test/compose.yaml) / [examples/compose.yaml](../examples/compose.yaml)) and open an automated PR when a new Envoy minor release ships or an old minor branch reaches EOL.
   - Run the `envoyproxy/envoy:dev` canary leg weekly (`continue-on-error: true`) so breaking Envoy filter changes surface weeks before a quarterly release.

---

## 6. Results Reporting

Avoid brittle log scraping by building reporting on Go's structured `go test -json` event stream paired with a declarative feature manifest:

1. **Feature Manifest (`conformance/features.yaml`)**:
   - Declares every tracked `ext_proc` v3 capability (`id`, `category`, `description`, `sdk_status`: `supported | partial | unsupported`, `min_envoy_version`, and `test_prefix`).
   - Because unsupported features are listed explicitly in `features.yaml`, reports show `Not Implemented` cleanly instead of silently omitting unimplemented spec surface.
2. **Structured Per-Version Artifacts**:
   - Each CI matrix leg runs `ENVOY_VERSION=${envoy} go test -v -json ./... > results-${envoy}.json` inside `conformance/` and uploads `results-${envoy}.json` (along with Envoy `:9901/stats` and container logs on failure).
3. **Fan-In Aggregator (`conformance/cmd/report`)**:
   - Consumes `conformance/features.yaml` + all `results-*.json` artifacts and produces three outputs:
     1. **CI Run Summary (`$GITHUB_STEP_SUMMARY`)**: Writes a **Capability × Envoy Version** Markdown table directly on the GitHub Actions run page (e.g., columns `v1.37-latest`, `v1.38-latest`, `v1.39-latest`, `v1.40-latest`, `dev (canary)` with cell statuses such as `Pass (8/8)`, `Fail (7/8)`, or `Not Implemented`).
     2. **Committed Compatibility Matrix ([CONFORMANCE.md](../CONFORMANCE.md))**: Regenerates the supported-versions & feature matrix table in [CONFORMANCE.md](../CONFORMANCE.md) on release or scheduled sync PRs so user-facing docs match observed test runs.
     3. **Machine-Readable Summary (`conformance-summary.json`)**: Compact JSON summary usable for release notes or a README compatibility badge.

---

## 7. Phased Implementation Roadmap

1. **Phase 1 — Foundation & Multi-Version Harness**:
   - Scaffold `conformance/` (`go.mod`, `envoy-versions.json`, `EnvoyRunner`, in-process upstream, header-dispatched conformance processor).
   - Add `resp.ValidateAll()` assertions to existing unit tests ([server_test.go](../server_test.go), [phase_test.go](../phase_test.go)).
   - Add `just conformance*` recipes and `.github/workflows/conformance.yaml` (`workflow_dispatch` + weekly schedule + `v*` tags).
2. **Phase 2 — Full Conformance of Existing SDK Features**:
   - Cover all `ProcessingMode` values (`NONE`, `BUFFERED`, `STREAMED`, `BUFFERED_PARTIAL`, `request_header_mode: SKIP`, `SEND` trailers).
   - Cover HTTP/1.1 (chunked + content-length) and HTTP/2 (`h2c`), `ReplaceBody` size mutations, all-phase `CancelRequest`, all `HeaderAppendAction` modes, downstream cancellation (`codes.Canceled`), timeouts, `ExtProcPerRoute`, and Envoy `:9901/stats` `spurious_msgs_received == 0` checks.
3. **Phase 3 — Reporting & Release Automation**:
   - Add `conformance/features.yaml` and `conformance/cmd/report` (`$GITHUB_STEP_SUMMARY` + generated [CONFORMANCE.md](../CONFORMANCE.md) updates).
   - Clean up multi-module/Dockerfile build hygiene (`-mod=readonly`, `tidy-all`, pinned tool tags) and enable the weekly Envoy release-window sync workflow + Renovate config.
4. **Phase 4 — SDK Feature Extensions (Guided by the Matrix)**:
   - Address `ObservabilityMode` protocol compliance first (suppress outbound `ProcessingResponse` when `observability_mode == true`).
   - Expose `ProtocolConfiguration` on `RequestContext`, then evaluate incremental additions for `ModeOverride`, `MetadataContext`/`DynamicMetadata`, `Attributes`, `OverrideMessageTimeout`, `ImmediateResponse` (`GrpcStatus`, `Details`), and `CONTINUE_AND_REPLACE`.
   - Keep `FULL_DUPLEX_STREAMED` / `StreamedImmediateResponse` / `GRPC` body mode separate behind a dedicated streaming interface design if adopted later.
