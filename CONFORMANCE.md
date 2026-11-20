# Envoy Compatibility and `ext_proc` Feature Coverage

This page tracks the Envoy Proxy versions and `envoy.service.ext_proc.v3` features supported and verified by `envoy-extproc-sdk-go`.

Verification in this repository is intentionally split into two tiers:

- **Routine tests**: Fast unit/stream-fake tests (`go test -race ./...`) and the compact single-version integration smoke suite in [test/](test) run on every pull request in [.github/workflows/pull-request.yaml](.github/workflows/pull-request.yaml).
- **Purposeful conformance exercises**: Multi-version Envoy compatibility matrices, processing-mode permutations, HTTP/2 and trailer coverage, and new spec-extension probes are planned in an isolated `conformance/` suite run intentionally (on demand, on release tags, on schedule, or on conformance changes) rather than blocking every routine commit. See [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md) for the full architecture and roadmap.

---

## Version Baseline

| Component | Current Pin / Target | Notes |
| :--- | :--- | :--- |
| Go Toolchain (`go.mod`) | `go 1.25.0` | Minimum module version in [go.mod](go.mod) |
| `go-control-plane/envoy` (`go.mod`) | `v1.37.0` | Compile-time `ext_proc` v3 protobuf definitions |
| `google.golang.org/grpc` (`go.mod`) | `v1.83.0` | Bidirectional gRPC stream server + health |
| Routine CI Envoy (`test/compose.yaml`) | `v1.35-latest` | Default tag in [test/compose.yaml](test/compose.yaml); override with `ENVOY_VERSION` |
| Target Multi-Version Matrix (Planned) | `v1.37` – `v1.40` + `dev` | Active 12-month supported Envoy minor releases plus `dev` canary |

> [!NOTE]
> The multi-version `conformance/` harness described in [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md) is currently in the planning stage. Verification statuses below reflect what is actively tested in the repository today.

---

## `ext_proc` v3 Feature Coverage Matrix

| Feature / Capability | SDK Support | SDK Surface | Current Verification |
| :--- | :--- | :--- | :--- |
| Request & response header phase callbacks (`SEND`) | Supported | `RequestProcessor.ProcessRequestHeaders`, `ProcessResponseHeaders` | Unit + Routine Integration (`test/`) |
| Skip request headers (`request_header_mode: SKIP`) | Supported | `newRequestContext` initializes `data` map before header phase ([context.go](context.go)) | Code inspection / Planned Conformance |
| Header read representation (`AllHeaders`) | Supported | `AllHeaders.Get`, `Values`, `RawValues`, `Has`, `SplitList` ([headers.go](headers.go)) | Unit (`headers_test.go`) + Routine Integration (`TestHeaderParsing`) |
| Header mutations (`ADD_IF_ABSENT`, `APPEND_IF_EXISTS_OR_ADD`, `OVERWRITE_IF_EXISTS_OR_ADD`, `OVERWRITE_IF_EXISTS`, remove) | Supported | `AddHeader*`, `AppendHeader*`, `OverwriteHeader*`, `UpdateHeader*`, `RemoveHeader*` | Unit + Routine Integration (partial; `UpdateHeader` `OVERWRITE_IF_EXISTS` unit-only) |
| Body processing — `BUFFERED` | Supported | `ProcessRequestBody`, `ProcessResponseBody`, `ReplaceBody`, `ClearBody` | Unit + Routine Integration (clear tested; replace size-change `TODO` in [test/tester_test.go](test/tester_test.go)) |
| Body processing — `STREAMED` / `BUFFERED_PARTIAL` | Supported | `ReplaceBodyChunk`, `ClearBodyChunk` | Unit / Planned Conformance |
| Request & response trailers | Supported | `ProcessRequestTrailers`, `ProcessResponseTrailers`, trailer `HeaderMutation` | Unit (`server_test.go`, `phase_test.go`) / Planned Conformance |
| Route cache re-evaluation (`clear_route_cache`) | Supported | `RequestContext.ClearRouteCache`, `UnClearRouteCache` | Unit + Routine Integration (`change upstream via clear route cache`) |
| Immediate local responses (`ImmediateResponse`) | Partial | `RequestContext.CancelRequest` (`Status`, `Headers`, `Body`; `GrpcStatus` and `Details` not yet exposed) | Unit + Routine Integration (`cancel a request` status/body; headers `TODO`) |
| Stream cancellation & graceful shutdown | Supported | `codes.Canceled` handling via `ErrorHandler` ([extproc.go](extproc.go)), `HealthServer`, `Serve` drain ([server.go](server.go)) | Unit (`server_test.go`, `health_test.go`) / Planned Conformance |
| Processing metrics & timing headers | Supported | `Metrics` (`/metrics` OpenMetrics) ([metrics.go](metrics.go)), `UpdateExtProcHeader`, `UpdateDurationHeader` | Unit (`metrics_test.go`, `server_test.go`) + Loadtest |
| Observability mode (`observability_mode: true`) | Protocol Gap | SDK currently always replies with `EmptyContinueResponse` when no response is set ([extproc.go](extproc.go)) | Tracked in [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md) |
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

Run the smoke load tests (full chain via k6, or direct gRPC via `loadgen`):

```sh
just loadtest-smoke
just loadtest-grpc-smoke
```

For the multi-version conformance design, dependency lifecycle policy, and reporting architecture, see [docs/CONFORMANCE_PLAN.md](docs/CONFORMANCE_PLAN.md).
