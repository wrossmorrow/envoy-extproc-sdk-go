# Envoy Conformance Suite

Isolated multi-version Envoy Proxy conformance exercises for `envoy-extproc-sdk-go`.

This module is intentionally separate from the root unit tests and the routine Docker Compose smoke tests in [../test](../test): routine PR iteration stays small and fast, while conformance exercises are purposefully executed against supported Envoy releases (`v1.37`–`v1.40` plus `dev` canary as configured in [envoy-versions.json](envoy-versions.json)).

## How It Works

Only Envoy runs out-of-process:

- The Go test binary runs an in-process HTTP/1.1 + `h2c` upstream server ([upstream.go](upstream.go)), an in-process SDK `GenericExtProcServer` ([processor.go](processor.go)) dispatched by `x-conformance-case`, and the HTTP/1.1 + HTTP/2 client tests ([conformance_test.go](conformance_test.go)).
- [bootstrap.go](bootstrap.go) builds a `go-control-plane` v3 configuration covering multiple per-route `ProcessingMode` profiles (`buffered`, `streamed`, `trailers`, `none`, `skip-req-headers`, `disabled`).
- [envoy_runner.go](envoy_runner.go) launches `envoyproxy/envoy:<tag>` via Docker (or a local Envoy binary when `ENVOY_BIN` is set) and asserts Envoy admin stats report zero `spurious_msgs_received`.

## Commands

Run fast harness self-tests (zero Docker required, always safe in offline sandboxes):

```sh
cd conformance && go test -short -v ./...
```

Run against the default Envoy image (`envoyproxy/envoy` tag from `envoy-versions.json`) via Docker:

```sh
just conformance
```

Run against a specific Envoy image tag:

```sh
just conformance-version v1.39-latest
```

Run against a local Envoy binary (e.g., via `func-e`):

```sh
ENVOY_BIN=/path/to/envoy just conformance
```

Run sequentially across all supported Envoy versions in `envoy-versions.json`:

```sh
just conformance-matrix
```

## Environment Variables

| Variable | Default | Purpose |
| :--- | :--- | :--- |
| `ENVOY_VERSION` | `default` in `envoy-versions.json` | Envoy image tag (`envoyproxy/envoy:<ENVOY_VERSION>`) |
| `ENVOY_BIN` | *(empty)* | Path to a local Envoy binary (skips Docker when set) |
| `CONFORMANCE_REQUIRE_ENVOY` | `0` | Set to `1` to fail instead of skipping when neither Docker nor `ENVOY_BIN` is available |
| `CONFORMANCE_STRICT_APPEND_ACTION` | `0` | Set to `1` to enable strict `AddHeader`/`AppendHeader` assertions reproducing `envoyproxy/envoy#48886` |
| `CONFORMANCE_STRICT_OBSERVABILITY` | `0` | Set to `1` to enable strict `observability_mode: true` assertions reproducing the SDK response-suppression gap |
