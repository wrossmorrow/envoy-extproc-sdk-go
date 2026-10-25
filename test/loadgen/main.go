// Package main implements the plain gRPC load generator for the envoy ext_proc
// SDK (Go).
//
// Drives full ext_proc exchanges over a raw bidirectional gRPC stream to the
// SDK's ext_proc server (`envoy.service.ext_proc.v3.ExternalProcessor/Process`),
// without an envoy in the path, and reports per-exchange latency and errors.
//
// Each exchange mirrors the phase sequence the envoy ext_proc filter drives for
// a buffered request/response: request headers -> request body ->
// response headers -> response body, then the stream is closed. Because envoy
// (and the HTTP machinery in between) are absent, the reported latency is the
// processor-side cost of the SDK's gRPC server path, complementing the
// containerized k6 load tests in ../../loadtest that drive the full
// envoy -> extproc -> upstream chain.

package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DURATION_HEADER is the response header the server may set (via the
// ProcessingOptions.UpdateDurationHeader option) carrying the accumulated
// extproc processing time in nanoseconds.
const DURATION_HEADER = "x-extproc-duration-ns"

const (
	defaultAddr        = "127.0.0.1:50051"
	defaultIterations  = 100
	defaultConcurrency = 10
)

// ExchangeOutcome is the outcome of one full exchange: the latency of the
// exchange, and the server-reported extproc processing duration in nanoseconds
// (when the server set the duration header).
type ExchangeOutcome struct {
	Latency           time.Duration
	ExtProcDurationNs int64
}

// ExchangeError is the error that aborted one exchange, if any.
type ExchangeError struct {
	Index int
	Error string
}

// WorkerResult is the outcome of one worker task: the latencies / durations it
// observed, and the exchanges it failed on.
type WorkerResult struct {
	Latencies          []time.Duration
	ExtProcDurationsNs []int64
	Errors             []ExchangeError
}

// envoyHeaderMap builds an envoy HeaderMap from a list of header name/value
// pairs, encoding values in raw_value.
func envoyHeaderMap(pairs [][2]string) *corev3.HeaderMap {
	headers := &corev3.HeaderMap{}
	for _, pair := range pairs {
		headers.Headers = append(headers.Headers, &corev3.HeaderValue{
			Key:      pair[0],
			Value:    "",
			RawValue: []byte(pair[1]),
		})
	}
	return headers
}

// envoyRequestHeaders builds a ProcessingRequest carrying request headers
// (not yet end of stream).
func envoyRequestHeaders(endOfStream bool, pairs [][2]string) *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers:     envoyHeaderMap(pairs),
				EndOfStream: endOfStream,
			},
		},
	}
}

// envoyResponseHeaders builds a ProcessingRequest carrying response headers
// (with end of stream).
func envoyResponseHeaders(endOfStream bool) *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extprocv3.HttpHeaders{
				Headers:     &corev3.HeaderMap{},
				EndOfStream: endOfStream,
			},
		},
	}
}

// envoyRequestBody builds a ProcessingRequest carrying a request body (with
// end of stream).
func envoyRequestBody(body []byte, endOfStream bool) *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestBody{
			RequestBody: &extprocv3.HttpBody{
				Body:        body,
				EndOfStream: endOfStream,
			},
		},
	}
}

// envoyResponseBody builds a ProcessingRequest carrying a response body (with
// end of stream).
func envoyResponseBody(body []byte, endOfStream bool) *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{
				Body:        body,
				EndOfStream: endOfStream,
			},
		},
	}
}

// sendPhase sends one processing request over the stream and waits for the
// response it produces.
func sendPhase(ctx context.Context, stream extprocv3.ExternalProcessor_ProcessClient, req *extprocv3.ProcessingRequest) (*extprocv3.ProcessingResponse, error) {
	if err := stream.Send(req); err != nil {
		return nil, fmt.Errorf("failed to send processing request: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("processing response stream error: %w", err)
	}
	return resp, nil
}

// extractDurationNs returns the x-extproc-duration-ns processing duration
// (nanoseconds) reported by the server in the given response, if the server set
// it (via the ProcessingOptions.UpdateDurationHeader option) and the phase
// ended the stream. It returns false when no duration was reported.
func extractDurationNs(resp *extprocv3.ProcessingResponse) (int64, bool) {
	switch {
	case resp.GetResponseHeaders() != nil:
		common := resp.GetResponseHeaders().GetResponse()
		if common == nil {
			return 0, false
		}
		return durationFromCommonResponse(common)
	case resp.GetResponseBody() != nil:
		common := resp.GetResponseBody().GetResponse()
		if common == nil {
			return 0, false
		}
		return durationFromCommonResponse(common)
	default:
		return 0, false
	}
}

// durationFromCommonResponse reads the x-extproc-duration-ns header out of a
// phase response's common response, if present.
func durationFromCommonResponse(common *extprocv3.CommonResponse) (int64, bool) {
	mutation := common.GetHeaderMutation()
	if mutation == nil {
		return 0, false
	}
	for _, option := range mutation.GetSetHeaders() {
		header := option.GetHeader()
		if header == nil || header.GetKey() != DURATION_HEADER {
			continue
		}
		raw := header.GetRawValue()
		if len(raw) == 0 {
			return 0, false
		}
		ns, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return 0, false
		}
		return ns, true
	}
	return 0, false
}

// runExchange drives one full ext_proc exchange over a single bidirectional
// gRPC stream and measures it.
func runExchange(ctx context.Context, client extprocv3.ExternalProcessorClient) (*ExchangeOutcome, error) {
	start := time.Now()
	stream, err := client.Process(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open process stream: %w", err)
	}

	// Request headers phase (not yet end of stream).
	resp, err := sendPhase(ctx, stream, envoyRequestHeaders(false, [][2]string{{":method", "POST"}, {":path", "/test"}}))
	if err != nil {
		return nil, err
	}
	// Request body phase (end of stream).
	resp, err = sendPhase(ctx, stream, envoyRequestBody([]byte("{}"), true))
	if err != nil {
		return nil, err
	}
	// Response headers phase (end of stream).
	resp, err = sendPhase(ctx, stream, envoyResponseHeaders(true))
	if err != nil {
		return nil, err
	}
	// Response body phase (end of stream).
	resp, err = sendPhase(ctx, stream, envoyResponseBody([]byte("{}"), true))
	if err != nil {
		return nil, err
	}

	// The exchange is complete: the data plane closes the stream here.
	if err := stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("failed to close process stream: %w", err)
	}
	// Wait for the server to close the stream, which it does once it has
	// answered the final end-of-stream phase and seen the half-close.
	if _, err := stream.Recv(); err != nil {
		// io.EOF here is expected; ignore the error.
		_ = err
	}

	ns, _ := extractDurationNs(resp)
	return &ExchangeOutcome{
		Latency:           time.Since(start),
		ExtProcDurationNs: ns,
	}, nil
}

// runWorker runs the exchanges assigned to one worker task: indices base,
// base + stride, ... below iterations, sequentially.
func runWorker(ctx context.Context, client extprocv3.ExternalProcessorClient, base, stride, iterations int) WorkerResult {
	result := WorkerResult{
		Latencies:          []time.Duration{},
		ExtProcDurationsNs: []int64{},
		Errors:             []ExchangeError{},
	}
	index := base
	for index < iterations {
		outcome, err := runExchange(ctx, client)
		if err != nil {
			result.Errors = append(result.Errors, ExchangeError{
				Index: index,
				Error: err.Error(),
			})
		} else {
			result.Latencies = append(result.Latencies, outcome.Latency)
			result.ExtProcDurationsNs = append(result.ExtProcDurationsNs, outcome.ExtProcDurationNs)
		}
		index += stride
	}
	return result
}

// formatDurationMs formats a duration in milliseconds.
func formatDurationMs(d time.Duration) float64 {
	return d.Seconds() * 1000
}

// percentile returns the nearest-rank percentile of the sorted durations, as
// milliseconds. It returns false when there are no samples.
func percentile(sorted []time.Duration, percentile float64) (float64, bool) {
	if len(sorted) == 0 {
		return 0, false
	}
	rank := int(math.Round((float64(len(sorted)-1) * percentile)))
	return sorted[rank].Seconds() * 1000, true
}

// connect opens a gRPC client connection to the ext_proc server.
func connect(addr string) (extprocv3.ExternalProcessorClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return extprocv3.NewExternalProcessorClient(conn), nil
}

func main() {
	fs := flag.NewFlagSet("extproc-loadgen", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "the extproc gRPC server address.")
	iterations := fs.Int("iterations", defaultIterations, "the number of exchanges to run.")
	concurrency := fs.Int("concurrency", defaultConcurrency, "the number of concurrent worker exchanges.")

	fs.Parse(os.Args[1:])

	if *iterations == 0 {
		fmt.Fprintln(os.Stderr, "extproc-loadgen: iterations must be greater than zero")
		os.Exit(1)
	}

	fmt.Printf("extproc-loadgen: target extproc gRPC server: http://%s\n", *addr)
	fmt.Printf("extproc-loadgen: running %d exchanges with concurrency %d\n", *iterations, *concurrency)

	ctx := context.Background()
	client, err := connect(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extproc-loadgen: failed to connect to extproc server at %s: %v\n", *addr, err)
		os.Exit(1)
	}

	workerResults := make(chan WorkerResult, *concurrency)
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			workerResults <- runWorker(ctx, client, base, *concurrency, *iterations)
		}(i)
	}
	wg.Wait()
	close(workerResults)

	var latencies []time.Duration
	var extprocDurationsNs []int64
	var errors []ExchangeError
	for result := range workerResults {
		latencies = append(latencies, result.Latencies...)
		extprocDurationsNs = append(extprocDurationsNs, result.ExtProcDurationsNs...)
		errors = append(errors, result.Errors...)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	avgMs := 0.0
	if len(latencies) > 0 {
		var sum time.Duration
		for _, d := range latencies {
			sum += d
		}
		avgMs = formatDurationMs(sum) / float64(len(latencies))
	}

	p50Ms := 0.0
	if v, ok := percentile(latencies, 0.50); ok {
		p50Ms = v
	}
	p95Ms := 0.0
	if v, ok := percentile(latencies, 0.95); ok {
		p95Ms = v
	}
	maxMs := 0.0
	if len(latencies) > 0 {
		maxMs = formatDurationMs(latencies[len(latencies)-1])
	}

	avgDurationMs := 0.0
	if len(extprocDurationsNs) > 0 {
		var sum int64
		for _, ns := range extprocDurationsNs {
			sum += ns
		}
		avgDurationMs = float64(sum) / (float64(len(extprocDurationsNs)) * 1e6)
	}

	for _, e := range errors {
		fmt.Printf("extproc-loadgen: exchange %d failed: %s\n", e.Index, e.Error)
	}
	fmt.Printf("extproc-loadgen: finished: %d exchanges, %d errors\n", len(latencies), len(errors))
	fmt.Printf("extproc-loadgen: latency avg=%vms p50=%vms p95=%vms max=%vms\n", avgMs, p50Ms, p95Ms, maxMs)
	fmt.Printf("extproc-loadgen: extproc duration avg=%vms (%d samples)\n", avgDurationMs, len(extprocDurationsNs))

	if len(errors) > 0 {
		os.Exit(1)
	}
}
