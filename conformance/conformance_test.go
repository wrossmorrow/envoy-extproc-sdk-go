package conformance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	extproc "github.com/wrossmorrow/envoy-extproc-sdk-go"
	"golang.org/x/net/http2"
)

func decodeEcho(t *testing.T, raw []byte) UpstreamEcho {
	t.Helper()
	var echo UpstreamEcho
	if err := json.Unmarshal(raw, &echo); err != nil {
		t.Fatalf("decode UpstreamEcho failed: %v (raw=%q)", err, string(raw))
	}
	return echo
}

func doRequest(t *testing.T, client *http.Client, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		t.Fatalf("read response body: %v", rerr)
	}
	return resp, body
}

func TestEnvoyConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Envoy conformance suite in -short mode")
	}

	version, err := ResolveEnvoyVersion()
	if err != nil {
		t.Fatalf("ResolveEnvoyVersion failed: %v", err)
	}

	primaryUpstream, err := StartUpstreamServer("primary")
	if err != nil {
		t.Fatalf("start primary upstream: %v", err)
	}
	t.Cleanup(func() { _ = primaryUpstream.Close() })

	secondaryUpstream, err := StartUpstreamServer("secondary")
	if err != nil {
		t.Fatalf("start secondary upstream: %v", err)
	}
	t.Cleanup(func() { _ = secondaryUpstream.Close() })

	extprocSrv, err := StartConformanceExtProcServer(&extproc.ProcessingOptions{
		UpdateExtProcHeader:  true,
		UpdateDurationHeader: true,
	}, nil)
	if err != nil {
		t.Fatalf("start conformance extproc server: %v", err)
	}
	t.Cleanup(extprocSrv.Close)

	abortExtprocSrv, err := StartConformanceExtProcServerWithName("conformance-abort", &extproc.ProcessingOptions{
		UpdateExtProcHeader:     true,
		AbortOnProcessorFailure: true,
	}, nil)
	if err != nil {
		t.Fatalf("start abort conformance extproc server: %v", err)
	}
	t.Cleanup(abortExtprocSrv.Close)

	secondExtprocSrv, err := StartConformanceExtProcServerWithName("conformance-second", &extproc.ProcessingOptions{
		UpdateExtProcHeader: true,
	}, nil)
	if err != nil {
		t.Fatalf("start secondary conformance extproc server: %v", err)
	}
	t.Cleanup(secondExtprocSrv.Close)

	runner, err := StartEnvoyRunner(context.Background(), EnvoyConfig{
		Version:               version,
		PrimaryUpstreamPort:   primaryUpstream.Port(),
		SecondaryUpstreamPort: secondaryUpstream.Port(),
		ExtProcPort:           extprocSrv.Port(),
		AbortExtProcPort:      abortExtprocSrv.Port(),
		SecondaryExtProcPort:  secondExtprocSrv.Port(),
	})
	if err != nil {
		if os.Getenv("CONFORMANCE_REQUIRE_ENVOY") == "1" {
			t.Fatalf("Envoy runtime required (CONFORMANCE_REQUIRE_ENVOY=1) for %q: %v", version, err)
		}
		t.Skipf("skipping live Envoy conformance suite for %q (set ENVOY_BIN or start Docker; set CONFORMANCE_REQUIRE_ENVOY=1 to enforce): %v", version, err)
	}
	t.Cleanup(func() { _ = runner.Close() })

	http1Client := &http.Client{Timeout: 15 * time.Second}
	h2cClient := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}

	t.Run("get_nobody_mode_none", func(t *testing.T) {
		caseID := "get-nobody-mode-none"
		var bodyCalled atomic.Bool
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				return ctx.AddHeader("x-get-seen", extproc.HeaderValue{RawValue: []byte("true")})
			},
			RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
				bodyCalled.Store(true)
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/none/ping", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if echo.Method != http.MethodGet || echo.BodyBytes != 0 {
			t.Errorf("unexpected echo: %+v", echo)
		}
		if got := echo.Headers["x-get-seen"]; !slices.Equal(got, []string{"true"}) {
			t.Errorf("upstream x-get-seen = %v, want [true]", got)
		}
		if bodyCalled.Load() {
			t.Error("ProcessRequestBody should not run under request_body_mode: NONE")
		}
	})

	runHeaderMutationCase := func(t *testing.T, client *http.Client, caseID, wantProto string) {
		t.Helper()
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				_ = ctx.AddHeader("x-req-add", extproc.HeaderValue{RawValue: []byte("added")})
				_ = ctx.OverwriteHeader("x-req-overwrite", extproc.HeaderValue{RawValue: []byte("after")})
				_ = ctx.RemoveHeader("x-req-remove")
				return ctx.ContinueRequest()
			},
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				_ = ctx.AddHeader("x-resp-add", extproc.HeaderValue{RawValue: []byte("added")})
				_ = ctx.OverwriteHeader("x-resp-overwrite", extproc.HeaderValue{RawValue: []byte("updated")})
				_ = ctx.RemoveHeader(CaseHeader)
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodPost, runner.ProxyURL()+"/mode/buffered/headers", strings.NewReader("hello"))
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)
		req.Header.Set("x-req-overwrite", "before")
		req.Header.Set("x-req-remove", "drop-me")

		resp, body := doRequest(t, client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if echo.Proto != wantProto {
			t.Errorf("upstream proto = %q, want %q", echo.Proto, wantProto)
		}
		if got := echo.Headers["x-req-add"]; !slices.Equal(got, []string{"added"}) {
			t.Errorf("x-req-add = %v, want [added]", got)
		}
		if got := echo.Headers["x-req-overwrite"]; !slices.Equal(got, []string{"after"}) {
			t.Errorf("x-req-overwrite = %v, want [after]", got)
		}
		if _, exists := echo.Headers["x-req-remove"]; exists {
			t.Errorf("x-req-remove should have been removed, got %v", echo.Headers["x-req-remove"])
		}

		if got := resp.Header.Get("x-resp-add"); got != "added" {
			t.Errorf("resp x-resp-add = %q, want %q", got, "added")
		}
		if got := resp.Header.Get("x-resp-overwrite"); got != "updated" {
			t.Errorf("resp x-resp-overwrite = %q, want %q", got, "updated")
		}
		if got := resp.Header.Get(CaseHeader); got != "" {
			t.Errorf("resp %s should have been removed, got %q", CaseHeader, got)
		}
		if got := resp.Header.Get("x-extproc-names"); got != "conformance" {
			t.Errorf("x-extproc-names = %q, want %q", got, "conformance")
		}
		if got := resp.Header.Get("x-extproc-duration-ns"); got == "" {
			t.Error("expected non-empty x-extproc-duration-ns header")
		}
	}

	t.Run("headers_mutations_http1", func(t *testing.T) {
		runHeaderMutationCase(t, http1Client, "headers-http1", "HTTP/1.1")
	})

	t.Run("headers_mutations_h2c", func(t *testing.T) {
		runHeaderMutationCase(t, h2cClient, "headers-h2c", "HTTP/2.0")
	})

	t.Run("header_append_action_envoy_gap", func(t *testing.T) {
		if os.Getenv("CONFORMANCE_STRICT_APPEND_ACTION") != "1" {
			t.Skip("known Envoy ext_proc limitation (envoyproxy/envoy#48886): HeaderMutation ignores HeaderValueOption.append_action and defaults deprecated append=false (overwrite); tracked for SDK follow-up")
		}

		caseID := "header-append-action-strict"
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				_ = ctx.AddHeader("x-req-existing", extproc.HeaderValue{RawValue: []byte("ignored")})
				_ = ctx.AppendHeader("x-req-append", extproc.HeaderValue{RawValue: []byte("second")})
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/none/append-action", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)
		req.Header.Set("x-req-existing", "keep")
		req.Header.Set("x-req-append", "first")

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if got := echo.Headers["x-req-existing"]; !slices.Equal(got, []string{"keep"}) {
			t.Errorf("x-req-existing (ADD_IF_ABSENT) = %v, want [keep]", got)
		}
		joined := strings.Join(echo.Headers["x-req-append"], ",")
		if !strings.Contains(joined, "first") || !strings.Contains(joined, "second") {
			t.Errorf("x-req-append (APPEND_IF_EXISTS_OR_ADD) = %v, want both first and second", echo.Headers["x-req-append"])
		}
	})

	t.Run("buffered_body_replace_grow_and_shrink", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			reqIn       string
			reqMutated  string
			respIn      string
			respMutated string
		}{
			{
				name:        "grow",
				reqIn:       "hi",
				reqMutated:  "expanded-request-payload-from-extproc",
				respIn:      "ok",
				respMutated: "expanded-response-payload-from-extproc",
			},
			{
				name:        "shrink",
				reqIn:       "very-long-original-request-payload-to-shrink",
				reqMutated:  "tiny",
				respIn:      "very-long-original-response-payload-to-shrink",
				respMutated: "min",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				caseID := "buffered-replace-" + tc.name
				extprocSrv.Processor.Register(caseID, CaseHandler{
					RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
						return ctx.ReplaceBody([]byte(tc.reqMutated))
					},
					ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
						return ctx.ReplaceBody([]byte(tc.respMutated))
					},
				})
				t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

				// Verify request-body mutation with normal JSON echo first.
				caseReqOnly := caseID + "-req"
				extprocSrv.Processor.Register(caseReqOnly, CaseHandler{
					RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
						return ctx.ReplaceBody([]byte(tc.reqMutated))
					},
				})
				t.Cleanup(func() { extprocSrv.Processor.Unregister(caseReqOnly) })

				req1, rerr := http.NewRequest(http.MethodPost, runner.ProxyURL()+"/mode/buffered/body", strings.NewReader(tc.reqIn))
				if rerr != nil {
					t.Fatal(rerr)
				}
				req1.Header.Set(CaseHeader, caseReqOnly)
				resp1, body1 := doRequest(t, http1Client, req1)
				if resp1.StatusCode != http.StatusOK {
					t.Fatalf("req replace status = %d, want 200 (body=%q)", resp1.StatusCode, string(body1))
				}
				echo := decodeEcho(t, body1)
				if echo.Body != tc.reqMutated {
					t.Errorf("upstream saw request body %q, want %q", echo.Body, tc.reqMutated)
				}

				// Verify response-body mutation with x-upstream-raw-body.
				req2, rerr := http.NewRequest(http.MethodPost, runner.ProxyURL()+"/mode/buffered/body", strings.NewReader(tc.reqIn))
				if rerr != nil {
					t.Fatal(rerr)
				}
				req2.Header.Set(CaseHeader, caseID)
				req2.Header.Set("x-upstream-raw-body", tc.respIn)
				resp2, body2 := doRequest(t, http1Client, req2)
				if resp2.StatusCode != http.StatusOK {
					t.Fatalf("resp replace status = %d, want 200 (body=%q)", resp2.StatusCode, string(body2))
				}
				if string(body2) != tc.respMutated {
					t.Errorf("client got response body %q, want %q", string(body2), tc.respMutated)
				}
			})
		}
	})

	t.Run("streamed_body_chunks", func(t *testing.T) {
		caseID := "streamed-body-chunks"
		var respChunks atomic.Int32
		extprocSrv.Processor.Register(caseID, CaseHandler{
			ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
				respChunks.Add(1)
				if len(body) > 0 {
					return ctx.ReplaceBodyChunk(bytes.ToUpper(body))
				}
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/streamed/chunks", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)
		req.Header.Set("x-upstream-raw-body", "chunka-chunkb-chunkc")
		req.Header.Set("x-upstream-chunks", "3")

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		if string(body) != "CHUNKA-CHUNKB-CHUNKC" {
			t.Errorf("streamed mutated body = %q, want %q", string(body), "CHUNKA-CHUNKB-CHUNKC")
		}
		if respChunks.Load() == 0 {
			t.Error("expected ProcessResponseBody to receive streamed chunks")
		}
	})

	t.Run("h2c_trailers_roundtrip", func(t *testing.T) {
		caseID := "h2c-trailers"
		var reqTrailersCalled, respTrailersCalled atomic.Bool
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestTrailers: func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
				reqTrailersCalled.Store(true)
				if got, ok := trailers.Get("x-req-trailer"); !ok || got != "from-client" {
					t.Errorf("ProcessRequestTrailers x-req-trailer = (%q, %v), want (from-client, true)", got, ok)
				}
				_ = ctx.OverwriteHeader("x-req-trailer", extproc.HeaderValue{RawValue: []byte("from-extproc-req")})
				return ctx.AddHeader("x-extproc-req-trailer", extproc.HeaderValue{RawValue: []byte("added")})
			},
			ResponseTrailers: func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
				respTrailersCalled.Store(true)
				if got, ok := trailers.Get("x-srv-trailer"); !ok || got != "from-server" {
					t.Errorf("ProcessResponseTrailers x-srv-trailer = (%q, %v), want (from-server, true)", got, ok)
				}
				_ = ctx.OverwriteHeader("x-srv-trailer", extproc.HeaderValue{RawValue: []byte("from-extproc-resp")})
				return ctx.AddHeader("x-extproc-resp-trailer", extproc.HeaderValue{RawValue: []byte("added")})
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodPost, runner.ProxyURL()+"/mode/trailers/test", struct{ io.Reader }{strings.NewReader("payload")})
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.ContentLength = -1
		req.Header.Set(CaseHeader, caseID)
		req.Header.Set("x-upstream-trailer-x-srv-trailer", "from-server")
		req.Trailer = http.Header{
			"X-Req-Trailer":         []string{"from-client"},
			"X-Extproc-Req-Trailer": nil,
		}

		resp, body := doRequest(t, h2cClient, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		if !reqTrailersCalled.Load() {
			t.Error("ProcessRequestTrailers was not invoked")
		}
		if !respTrailersCalled.Load() {
			t.Error("ProcessResponseTrailers was not invoked")
		}

		echo := decodeEcho(t, body)
		if got := echo.Trailers["x-req-trailer"]; !slices.Equal(got, []string{"from-extproc-req"}) {
			t.Errorf("upstream x-req-trailer = %v, want [from-extproc-req]", got)
		}
		if got := echo.Trailers["x-extproc-req-trailer"]; !slices.Equal(got, []string{"added"}) {
			t.Errorf("upstream x-extproc-req-trailer = %v, want [added]", got)
		}
		if got := resp.Trailer.Get("X-Srv-Trailer"); got != "from-extproc-resp" {
			t.Errorf("client response trailer x-srv-trailer = %q, want %q", got, "from-extproc-resp")
		}
		if got := resp.Trailer.Get("X-Extproc-Resp-Trailer"); got != "added" {
			t.Errorf("client response trailer x-extproc-resp-trailer = %q, want %q", got, "added")
		}
	})

	t.Run("skip_request_headers_response_mutation", func(t *testing.T) {
		caseID := "skip-req-headers"
		var reqHeadersCalled atomic.Bool
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				reqHeadersCalled.Store(true)
				return ctx.ContinueRequest()
			},
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				return ctx.AddHeader("x-resp-only", extproc.HeaderValue{RawValue: []byte("from-skip-mode")})
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/skip-req-headers/test", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		if reqHeadersCalled.Load() {
			t.Error("ProcessRequestHeaders should not be called under request_header_mode: SKIP")
		}
		if got := resp.Header.Get("x-resp-only"); got != "from-skip-mode" {
			t.Errorf("response x-resp-only = %q, want %q", got, "from-skip-mode")
		}
	})

	t.Run("clear_route_cache_upstream_switch", func(t *testing.T) {
		caseID := "clear-route-cache"
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				if err := ctx.OverwriteHeader(":authority", extproc.HeaderValue{RawValue: []byte("upstream-secondary")}); err != nil {
					return err
				}
				ctx.ClearRouteCache()
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/buffered/route", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if echo.Server != "secondary" {
			t.Errorf("routed server = %q, want %q", echo.Server, "secondary")
		}
	})

	t.Run("immediate_response_request_headers", func(t *testing.T) {
		caseID := "immediate-req-headers"
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				return ctx.CancelRequest(
					http.StatusTooManyRequests,
					map[string]extproc.HeaderValue{"x-local-reply": {RawValue: []byte("yes")}},
					"short-circuit",
				)
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/buffered/cancel", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
		}
		if got := resp.Header.Get("x-local-reply"); got != "yes" {
			t.Errorf("x-local-reply = %q, want %q", got, "yes")
		}
		if string(body) != "short-circuit" {
			t.Errorf("body = %q, want %q", string(body), "short-circuit")
		}
	})

	t.Run("per_route_disabled_bypass", func(t *testing.T) {
		caseID := "per-route-disabled"
		var called atomic.Bool
		extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				called.Store(true)
				return ctx.ContinueRequest()
			},
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				called.Store(true)
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { extprocSrv.Processor.Unregister(caseID) })

		req, rerr := http.NewRequest(http.MethodGet, runner.ProxyURL()+"/mode/disabled/check", nil)
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		if called.Load() {
			t.Error("processor should not be invoked when route disables ext_proc")
		}
		if got := resp.Header.Get("x-extproc-names"); got != "" {
			t.Errorf("x-extproc-names = %q, want empty on disabled route", got)
		}
	})

	runPhase2Scenarios(t, conformanceEnv{
		runner:           runner,
		extprocSrv:       extprocSrv,
		abortExtprocSrv:  abortExtprocSrv,
		secondExtprocSrv: secondExtprocSrv,
		http1Client:      http1Client,
		h2cClient:        h2cClient,
	})

	runner.AssertNoProtocolErrors(t)
}
