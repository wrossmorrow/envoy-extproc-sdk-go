package conformance

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extproc "github.com/wrossmorrow/envoy-extproc-sdk-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type conformanceEnv struct {
	runner           *EnvoyRunner
	extprocSrv       *ConformanceExtProcServer
	abortExtprocSrv  *ConformanceExtProcServer
	secondExtprocSrv *ConformanceExtProcServer
	http1Client      *http.Client
	h2cClient        *http.Client
}

type routePayload struct {
	Upstream string `json:"upstream"`
}

func mustCounter(t *testing.T, srv *ConformanceExtProcServer, name string) float64 {
	t.Helper()
	val, err := srv.CounterValue(name)
	if err != nil {
		t.Fatalf("CounterValue(%q): %v", name, err)
	}
	return val
}

func runPhase2Scenarios(t *testing.T, env conformanceEnv) {
	t.Run("post_with_body_mode_none", func(t *testing.T) {
		caseID := "post-body-mode-none"
		var reqBodyCalled, respBodyCalled atomic.Bool
		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
				reqBodyCalled.Store(true)
				return ctx.ContinueRequest()
			},
			ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
				respBodyCalled.Store(true)
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

		req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/none/echo", strings.NewReader("untouched-body"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, env.http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if echo.Body != "untouched-body" {
			t.Errorf("echo.Body = %q, want %q", echo.Body, "untouched-body")
		}
		if reqBodyCalled.Load() {
			t.Error("ProcessRequestBody called under request_body_mode: NONE")
		}
		if respBodyCalled.Load() {
			t.Error("ProcessResponseBody called under response_body_mode: NONE")
		}
	})

	t.Run("buffered_body_clear", func(t *testing.T) {
		for _, protoCase := range []struct {
			name   string
			client *http.Client
		}{
			{"http1", env.http1Client},
			{"h2c", env.h2cClient},
		} {
			t.Run(protoCase.name, func(t *testing.T) {
				reqCase := "buffered-clear-req-" + protoCase.name
				env.extprocSrv.Processor.Register(reqCase, CaseHandler{
					RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
						return ctx.ClearBody()
					},
				})
				t.Cleanup(func() { env.extprocSrv.Processor.Unregister(reqCase) })

				req1, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/buffered/clear", strings.NewReader("drop-request-body"))
				if err != nil {
					t.Fatal(err)
				}
				req1.Header.Set(CaseHeader, reqCase)
				resp1, body1 := doRequest(t, protoCase.client, req1)
				if resp1.StatusCode != http.StatusOK {
					t.Fatalf("clear request status = %d, want 200 (body=%q)", resp1.StatusCode, string(body1))
				}
				echo := decodeEcho(t, body1)
				if echo.Body != "" || echo.BodyBytes != 0 {
					t.Errorf("upstream saw cleared request body %q (%d bytes), want empty", echo.Body, echo.BodyBytes)
				}

				respCase := "buffered-clear-resp-" + protoCase.name
				env.extprocSrv.Processor.Register(respCase, CaseHandler{
					ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
						return ctx.ClearBody()
					},
				})
				t.Cleanup(func() { env.extprocSrv.Processor.Unregister(respCase) })

				req2, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/buffered/clear", strings.NewReader("keep-req"))
				if err != nil {
					t.Fatal(err)
				}
				req2.Header.Set(CaseHeader, respCase)
				req2.Header.Set("x-upstream-raw-body", "drop-response-body")
				resp2, body2 := doRequest(t, protoCase.client, req2)
				if resp2.StatusCode != http.StatusOK {
					t.Fatalf("clear response status = %d, want 200 (body=%q)", resp2.StatusCode, string(body2))
				}
				if len(body2) != 0 {
					t.Errorf("client got cleared response body %q, want empty", string(body2))
				}
			})
		}
	})

	t.Run("http1_chunked_response_buffered_replace_and_clear", func(t *testing.T) {
		replaceCase := "chunked-buffered-replace"
		var sawFull atomic.Bool
		env.extprocSrv.Processor.Register(replaceCase, CaseHandler{
			ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
				if string(body) == "chunk1-chunk2-chunk3" && ctx.EndOfStream {
					sawFull.Store(true)
				}
				return ctx.ReplaceBody([]byte("replaced"))
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(replaceCase) })

		req1, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/mode/buffered/chunked-upstream", nil)
		if err != nil {
			t.Fatal(err)
		}
		req1.Header.Set(CaseHeader, replaceCase)
		req1.Header.Set("x-upstream-raw-body", "chunk1-chunk2-chunk3")
		req1.Header.Set("x-upstream-chunks", "3")

		resp1, body1 := doRequest(t, env.http1Client, req1)
		if resp1.StatusCode != http.StatusOK {
			t.Fatalf("replace chunked status = %d, want 200 (body=%q)", resp1.StatusCode, string(body1))
		}
		if !sawFull.Load() {
			t.Error("expected BUFFERED mode to assemble chunked upstream body before calling ProcessResponseBody")
		}
		if string(body1) != "replaced" {
			t.Errorf("replaced chunked body = %q, want %q", string(body1), "replaced")
		}

		clearCase := "chunked-buffered-clear"
		env.extprocSrv.Processor.Register(clearCase, CaseHandler{
			ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
				return ctx.ClearBody()
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(clearCase) })

		req2, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/mode/buffered/chunked-upstream", nil)
		if err != nil {
			t.Fatal(err)
		}
		req2.Header.Set(CaseHeader, clearCase)
		req2.Header.Set("x-upstream-raw-body", "chunk1-chunk2-chunk3")
		req2.Header.Set("x-upstream-chunks", "3")

		resp2, body2 := doRequest(t, env.http1Client, req2)
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("clear chunked status = %d, want 200 (body=%q)", resp2.StatusCode, string(body2))
		}
		if len(body2) != 0 {
			t.Errorf("cleared chunked body = %q, want empty", string(body2))
		}
	})

	t.Run("streamed_request_and_response_clear_chunk", func(t *testing.T) {
		caseID := "streamed-clear-chunk"
		var sawReqEOS, sawRespEOS atomic.Bool
		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
				if ctx.EndOfStream {
					sawReqEOS.Store(true)
				}
				if bytes.Equal(body, []byte("drop-me|")) {
					return ctx.ClearBodyChunk()
				}
				if bytes.Contains(body, []byte("drop-me|")) {
					return ctx.ReplaceBodyChunk(bytes.ReplaceAll(body, []byte("drop-me|"), nil))
				}
				return ctx.ContinueRequest()
			},
			ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
				if ctx.EndOfStream {
					sawRespEOS.Store(true)
				}
				return ctx.ClearBodyChunk()
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

		// Drive distinct HTTP/2 DATA frames via io.Pipe for reliable chunk delivery.
		pr, pw := io.Pipe()
		go func() {
			defer pw.Close()
			_, _ = pw.Write([]byte("drop-me|"))
			time.Sleep(15 * time.Millisecond)
			_, _ = pw.Write([]byte("keep-req"))
		}()

		req1, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/streamed/req-clear", pr)
		if err != nil {
			t.Fatal(err)
		}
		req1.ContentLength = -1
		req1.Header.Set(CaseHeader, caseID)

		// First validate response ClearBodyChunk wipes the streamed response.
		resp1, body1 := doRequest(t, env.h2cClient, req1)
		if resp1.StatusCode != http.StatusOK {
			t.Fatalf("streamed clear status = %d, want 200 (body=%q)", resp1.StatusCode, string(body1))
		}
		if len(body1) != 0 {
			t.Errorf("streamed ClearBodyChunk response = %q, want empty", string(body1))
		}
		if !sawReqEOS.Load() || !sawRespEOS.Load() {
			t.Errorf("EndOfStream flags: req=%v resp=%v, want both true", sawReqEOS.Load(), sawRespEOS.Load())
		}

		// Now pass response through untouched so we observe the cleared/mutated request body in echo.
		reqOnlyCase := "streamed-clear-req-only"
		env.extprocSrv.Processor.Register(reqOnlyCase, CaseHandler{
			RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
				if bytes.Equal(body, []byte("drop-me|")) {
					return ctx.ClearBodyChunk()
				}
				if bytes.Contains(body, []byte("drop-me|")) {
					return ctx.ReplaceBodyChunk(bytes.ReplaceAll(body, []byte("drop-me|"), nil))
				}
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(reqOnlyCase) })

		pr2, pw2 := io.Pipe()
		go func() {
			defer pw2.Close()
			_, _ = pw2.Write([]byte("drop-me|"))
			time.Sleep(15 * time.Millisecond)
			_, _ = pw2.Write([]byte("keep-req"))
		}()

		req2, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/streamed/req-clear-echo", pr2)
		if err != nil {
			t.Fatal(err)
		}
		req2.ContentLength = -1
		req2.Header.Set(CaseHeader, reqOnlyCase)

		resp2, body2 := doRequest(t, env.h2cClient, req2)
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("streamed echo status = %d, want 200 (body=%q)", resp2.StatusCode, string(body2))
		}
		echo := decodeEcho(t, body2)
		if echo.Body != "keep-req" {
			t.Errorf("upstream saw streamed body %q, want %q", echo.Body, "keep-req")
		}
	})

	t.Run("buffered_partial_mode", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			payload string
		}{
			{"within_limit", "short-payload"},
			{"exceeds_limit", strings.Repeat("x", 256)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				caseID := "buffered-partial-" + tc.name
				var reqLen, respLen atomic.Int64
				env.extprocSrv.Processor.Register(caseID, CaseHandler{
					RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
						reqLen.Store(int64(len(body)))
						return ctx.ContinueRequest()
					},
					ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
						respLen.Store(int64(len(body)))
						return ctx.ContinueRequest()
					},
				})
				t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

				req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/buffered-partial/test", strings.NewReader(tc.payload))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(CaseHeader, caseID)

				resp, body := doRequest(t, env.http1Client, req)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
				}
				echo := decodeEcho(t, body)
				if echo.Body != tc.payload {
					t.Errorf("upstream saw %d bytes, want %d", len(echo.Body), len(tc.payload))
				}
				if reqLen.Load() == 0 {
					t.Error("expected ProcessRequestBody to receive buffered partial bytes")
				}
				if respLen.Load() == 0 {
					t.Error("expected ProcessResponseBody to receive buffered partial bytes")
				}
			})
		}
	})

	t.Run("http1_trailers_roundtrip", func(t *testing.T) {
		caseID := "http1-trailers"
		reqBefore := mustCounter(t, env.extprocSrv, "extproc_total_request_trailers")
		respBefore := mustCounter(t, env.extprocSrv, "extproc_total_response_trailers")

		var reqTrailersCalled, respTrailersCalled atomic.Bool
		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestTrailers: func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
				reqTrailersCalled.Store(true)
				if got, ok := trailers.Get("x-req-trailer"); !ok || got != "h1-client" {
					t.Errorf("ProcessRequestTrailers x-req-trailer = (%q, %v), want (h1-client, true)", got, ok)
				}
				_ = ctx.OverwriteHeader("x-req-trailer", extproc.HeaderValue{RawValue: []byte("h1-extproc-req")})
				return ctx.AddHeader("x-extproc-req-trailer", extproc.HeaderValue{RawValue: []byte("h1-added")})
			},
			ResponseTrailers: func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
				respTrailersCalled.Store(true)
				if got, ok := trailers.Get("x-srv-trailer"); !ok || got != "h1-server" {
					t.Errorf("ProcessResponseTrailers x-srv-trailer = (%q, %v), want (h1-server, true)", got, ok)
				}
				_ = ctx.OverwriteHeader("x-srv-trailer", extproc.HeaderValue{RawValue: []byte("h1-extproc-resp")})
				return ctx.AddHeader("x-extproc-resp-trailer", extproc.HeaderValue{RawValue: []byte("h1-added")})
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

		req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/trailers/http1", struct{ io.Reader }{strings.NewReader("h1-trailer-payload")})
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = -1
		req.Header.Set(CaseHeader, caseID)
		req.Header.Set("x-upstream-trailer-x-srv-trailer", "h1-server")
		req.Trailer = http.Header{
			"X-Req-Trailer":         []string{"h1-client"},
			"X-Extproc-Req-Trailer": nil,
		}

		resp, body := doRequest(t, env.http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		if !reqTrailersCalled.Load() || !respTrailersCalled.Load() {
			t.Errorf("trailers callbacks called: req=%v resp=%v", reqTrailersCalled.Load(), respTrailersCalled.Load())
		}

		echo := decodeEcho(t, body)
		if got := echo.Trailers["x-req-trailer"]; !slices.Equal(got, []string{"h1-extproc-req"}) {
			t.Errorf("upstream x-req-trailer = %v, want [h1-extproc-req]", got)
		}
		if got := echo.Trailers["x-extproc-req-trailer"]; !slices.Equal(got, []string{"h1-added"}) {
			t.Errorf("upstream x-extproc-req-trailer = %v, want [h1-added]", got)
		}
		if got := resp.Trailer.Get("X-Srv-Trailer"); got != "h1-extproc-resp" {
			t.Errorf("client trailer X-Srv-Trailer = %q, want %q", got, "h1-extproc-resp")
		}
		if got := resp.Trailer.Get("X-Extproc-Resp-Trailer"); got != "h1-added" {
			t.Errorf("client trailer X-Extproc-Resp-Trailer = %q, want %q", got, "h1-added")
		}

		if reqAfter := mustCounter(t, env.extprocSrv, "extproc_total_request_trailers"); reqAfter != reqBefore+1 {
			t.Errorf("extproc_total_request_trailers = %v, want %v", reqAfter, reqBefore+1)
		}
		if respAfter := mustCounter(t, env.extprocSrv, "extproc_total_response_trailers"); respAfter != respBefore+1 {
			t.Errorf("extproc_total_response_trailers = %v, want %v", respAfter, respBefore+1)
		}
	})

	t.Run("headers_multivalue_and_context_state", func(t *testing.T) {
		caseID := "headers-context-state"
		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, headers extproc.AllHeaders) error {
				if got := headers.Values("x-multi"); !slices.Equal(got, []string{"first", "second"}) {
					t.Errorf("headers.Values(x-multi) = %v, want [first second]", got)
				}
				if got := headers.Values("x-date-like"); !slices.Equal(got, []string{"Mon, 20 Nov 2026 10:00:00 GMT"}) {
					t.Errorf("headers.Values(x-date-like) = %v, want single comma-preserved entry", got)
				}
				if got := headers.SplitList("x-list"); !slices.Equal(got, []string{"one", "two", "three"}) {
					t.Errorf("headers.SplitList(x-list) = %v, want [one two three]", got)
				}
				if ctx.Method != http.MethodPost {
					t.Errorf("ctx.Method = %q, want POST", ctx.Method)
				}
				if ctx.Path != "/mode/buffered/ctx" {
					t.Errorf("ctx.Path = %q, want /mode/buffered/ctx", ctx.Path)
				}
				if ctx.FullPath != "/mode/buffered/ctx?alpha=1&beta=2" {
					t.Errorf("ctx.FullPath = %q, want /mode/buffered/ctx?alpha=1&beta=2", ctx.FullPath)
				}
				if ctx.Scheme == "" || ctx.Authority == "" || ctx.RequestID == "" {
					t.Errorf("expected Scheme/Authority/RequestID populated, got %q/%q/%q", ctx.Scheme, ctx.Authority, ctx.RequestID)
				}
				if ctx.GetPhase() != extproc.REQUEST_PHASE_REQUEST_HEADERS {
					t.Errorf("phase = %d, want REQUEST_PHASE_REQUEST_HEADERS", ctx.GetPhase())
				}
				return ctx.SetValue("trace", "from-req-headers")
			},
			RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
				if ctx.GetPhase() != extproc.REQUEST_PHASE_REQUEST_BODY {
					t.Errorf("phase = %d, want REQUEST_PHASE_REQUEST_BODY", ctx.GetPhase())
				}
				v, err := ctx.GetValue("trace")
				if err != nil {
					return err
				}
				return ctx.SetValue("trace", v.(string)+"->req-body")
			},
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				if ctx.GetPhase() != extproc.REQUEST_PHASE_RESPONSE_HEADERS {
					t.Errorf("phase = %d, want REQUEST_PHASE_RESPONSE_HEADERS", ctx.GetPhase())
				}
				v, err := ctx.GetValue("trace")
				if err != nil {
					return err
				}
				return ctx.SetValue("trace", v.(string)+"->resp-headers")
			},
			ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
				if ctx.GetPhase() != extproc.REQUEST_PHASE_RESPONSE_BODY {
					t.Errorf("phase = %d, want REQUEST_PHASE_RESPONSE_BODY", ctx.GetPhase())
				}
				v, err := ctx.GetValue("trace")
				if err != nil {
					return err
				}
				return ctx.ReplaceBody([]byte(v.(string) + "->resp-body"))
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

		req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/buffered/ctx?alpha=1&beta=2", strings.NewReader("body"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(CaseHeader, caseID)
		req.Header.Add("x-multi", "first")
		req.Header.Add("x-multi", "second")
		req.Header.Set("x-date-like", "Mon, 20 Nov 2026 10:00:00 GMT")
		req.Header.Set("x-list", "one, two , , three")

		resp, body := doRequest(t, env.http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		want := "from-req-headers->req-body->resp-headers->resp-body"
		if string(body) != want {
			t.Errorf("cross-phase trace body = %q, want %q", string(body), want)
		}
	})

	t.Run("clear_route_cache_from_request_body", func(t *testing.T) {
		caseID := "clear-route-cache-body"
		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
				var target routePayload
				if err := json.Unmarshal(body, &target); err != nil {
					return err
				}
				if err := ctx.OverwriteHeader(":authority", extproc.HeaderValue{RawValue: []byte(target.Upstream)}); err != nil {
					return err
				}
				ctx.ClearRouteCache()
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

		req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/buffered/body-route", strings.NewReader(`{"upstream":"upstream-secondary"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, env.http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if echo.Server != "secondary" {
			t.Errorf("routed server = %q, want %q", echo.Server, "secondary")
		}
	})

	t.Run("immediate_response_all_phases_and_trailers_fallback", func(t *testing.T) {
		t.Run("request_body", func(t *testing.T) {
			caseID := "imm-req-body"
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					return ctx.CancelRequest(http.StatusForbidden, map[string]extproc.HeaderValue{
						"x-imm-phase": {RawValue: []byte("request-body")},
					}, "blocked-in-req-body")
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/buffered/imm", strings.NewReader("payload"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)
			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.StatusCode)
			}
			if resp.Header.Get("x-imm-phase") != "request-body" {
				t.Errorf("x-imm-phase = %q, want request-body", resp.Header.Get("x-imm-phase"))
			}
			if string(body) != "blocked-in-req-body" {
				t.Errorf("body = %q, want blocked-in-req-body", string(body))
			}
		})

		t.Run("response_headers", func(t *testing.T) {
			caseID := "imm-resp-headers"
			var respBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.CancelRequest(http.StatusTooManyRequests, map[string]extproc.HeaderValue{
						"x-imm-phase": {RawValue: []byte("response-headers")},
					}, "blocked-in-resp-headers")
				},
				ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
					respBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/mode/buffered/imm", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)
			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("status = %d, want 429", resp.StatusCode)
			}
			if resp.Header.Get("x-imm-phase") != "response-headers" {
				t.Errorf("x-imm-phase = %q, want response-headers", resp.Header.Get("x-imm-phase"))
			}
			if string(body) != "blocked-in-resp-headers" {
				t.Errorf("body = %q, want blocked-in-resp-headers", string(body))
			}
			if respBodyCalled.Load() {
				t.Error("ProcessResponseBody should not run after CancelRequest in ResponseHeaders")
			}
		})

		t.Run("response_body", func(t *testing.T) {
			caseID := "imm-resp-body"
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
					return ctx.CancelRequest(http.StatusUnprocessableEntity, map[string]extproc.HeaderValue{
						"x-imm-phase": {RawValue: []byte("response-body")},
					}, "blocked-in-resp-body")
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/mode/buffered/imm", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)
			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", resp.StatusCode)
			}
			if resp.Header.Get("x-imm-phase") != "response-body" {
				t.Errorf("x-imm-phase = %q, want response-body", resp.Header.Get("x-imm-phase"))
			}
			if string(body) != "blocked-in-resp-body" {
				t.Errorf("body = %q, want blocked-in-resp-body", string(body))
			}
		})

		t.Run("trailers_fallback", func(t *testing.T) {
			caseID := "imm-trailers-fallback"
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				ResponseTrailers: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.CancelRequest(http.StatusTooManyRequests, map[string]extproc.HeaderValue{
						"x-trailer-fallback": {RawValue: []byte("kept")},
					}, "ignored-in-trailers")
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/trailers/cancel-fallback", strings.NewReader("ok"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)
			req.Header.Set("x-upstream-trailer-x-srv-tr", "orig")

			resp, _ := doRequest(t, env.h2cClient, req)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("trailers fallback status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Trailer.Get("X-Trailer-Fallback"); got != "kept" {
				t.Errorf("response trailer X-Trailer-Fallback = %q, want %q", got, "kept")
			}
		})
	})

	t.Run("downstream_client_cancel_graceful", func(t *testing.T) {
		erroredBefore := mustCounter(t, env.extprocSrv, "extproc_errored_streams")
		phaseErrBefore := mustCounter(t, env.extprocSrv, "extproc_phase_errors_total")
		sendErrBefore := mustCounter(t, env.extprocSrv, "extproc_response_send_errors")

		t.Run("during_send", func(t *testing.T) {
			caseID := "client-cancel-during-send"
			entered := make(chan struct{})
			release := make(chan struct{})
			gotErr := make(chan error, 1)

			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					close(entered)
					<-release
					return ctx.ContinueRequest()
				},
				OnError: func(_ *extproc.RequestContext, _ int, err error) {
					select {
					case gotErr <- err:
					default:
					}
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.runner.ProxyURL()+"/mode/none/cancel-send", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			clientDone := make(chan error, 1)
			go func() {
				resp, derr := env.h2cClient.Do(req)
				if resp != nil {
					_ = resp.Body.Close()
				}
				clientDone <- derr
			}()

			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("timed out waiting for RequestHeaders entry")
			}

			cancel()
			select {
			case <-clientDone:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("timed out waiting for cancelled client request to return")
			}

			// Let Envoy reset the ext_proc stream before ProcessRequestHeaders returns.
			time.Sleep(50 * time.Millisecond)
			close(release)

			select {
			case herr := <-gotErr:
				if status.Code(herr) != codes.Canceled && !errors.Is(herr, context.Canceled) {
					t.Errorf("ErrorHandler error = %v, want Canceled", herr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for ErrorHandler cancellation notification")
			}
		})

		t.Run("during_recv", func(t *testing.T) {
			caseID := "client-cancel-during-recv"
			chunk1Seen := make(chan struct{})
			gotErr := make(chan error, 1)

			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					select {
					case <-chunk1Seen:
					default:
						close(chunk1Seen)
					}
					return ctx.ContinueRequest()
				},
				OnError: func(_ *extproc.RequestContext, _ int, err error) {
					select {
					case gotErr <- err:
					default:
					}
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			pr, pw := io.Pipe()
			defer pw.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.runner.ProxyURL()+"/mode/streamed/cancel-recv", pr)
			if err != nil {
				t.Fatal(err)
			}
			req.ContentLength = -1
			req.Header.Set(CaseHeader, caseID)

			clientDone := make(chan error, 1)
			go func() {
				resp, derr := env.h2cClient.Do(req)
				if resp != nil {
					_ = resp.Body.Close()
				}
				clientDone <- derr
			}()

			if _, werr := pw.Write([]byte("part1")); werr != nil {
				t.Fatalf("write chunk 1: %v", werr)
			}

			select {
			case <-chunk1Seen:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for chunk 1 in RequestBody")
			}

			// SDK is now blocked in srv.Recv() waiting for the rest of the streamed body.
			time.Sleep(30 * time.Millisecond)
			cancel()

			select {
			case <-clientDone:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for cancelled client request to return")
			}

			// Envoy may tear down an active mid-stream gRPC channel via either
			// RST_STREAM (codes.Canceled -> OnError) or clean half-close (io.EOF).
			// Either way, the SDK must finish with zero active/errored streams.
			select {
			case herr := <-gotErr:
				t.Logf("during_recv observed OnError: %v", herr)
				if status.Code(herr) != codes.Canceled && !errors.Is(herr, context.Canceled) {
					t.Errorf("ErrorHandler error = %v, want Canceled", herr)
				}
			case <-time.After(250 * time.Millisecond):
				active, gerr := env.extprocSrv.GaugeValue("extproc_active_streams")
				if gerr != nil {
					t.Fatalf("GaugeValue(extproc_active_streams): %v", gerr)
				}
				t.Logf("during_recv ended without OnError (clean EOF), extproc_active_streams=%v", active)
				if active != 0 {
					t.Errorf("extproc_active_streams = %v, want 0 after mid-stream cancel", active)
				}
			}
		})

		if got := mustCounter(t, env.extprocSrv, "extproc_errored_streams"); got != erroredBefore {
			t.Errorf("extproc_errored_streams = %v, want %v (unchanged after cancellations)", got, erroredBefore)
		}
		if got := mustCounter(t, env.extprocSrv, "extproc_phase_errors_total"); got != phaseErrBefore {
			t.Errorf("extproc_phase_errors_total = %v, want %v (unchanged after cancellations)", got, phaseErrBefore)
		}
		if got := mustCounter(t, env.extprocSrv, "extproc_response_send_errors"); got != sendErrBefore {
			t.Errorf("extproc_response_send_errors = %v, want %v (unchanged after cancellations)", got, sendErrBefore)
		}
	})

	t.Run("message_timeout_fail_closed_and_fail_open", func(t *testing.T) {
		t.Run("fail_closed", func(t *testing.T) {
			caseID := "timeout-fail-closed"
			release := make(chan struct{})
			done := make(chan struct{})
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					defer close(done)
					<-release
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/profile/timeout-fail-closed/ping", nil)
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			close(release)
			<-done
			if resp.StatusCode != http.StatusGatewayTimeout {
				t.Errorf("fail_closed timeout status = %d, want 504 (body=%q)", resp.StatusCode, string(body))
			}
		})

		t.Run("fail_open", func(t *testing.T) {
			caseID := "timeout-fail-open"
			release := make(chan struct{})
			done := make(chan struct{})
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					defer close(done)
					<-release
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/profile/timeout-fail-open/ping", nil)
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			close(release)
			<-done
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("fail_open timeout status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			echo := decodeEcho(t, body)
			if echo.Server != "primary" {
				t.Errorf("routed server = %q, want primary", echo.Server)
			}
		})
	})

	t.Run("processor_error_continue_vs_abort", func(t *testing.T) {
		t.Run("continue_on_failure", func(t *testing.T) {
			caseID := "err-continue"
			phaseErrBefore := mustCounter(t, env.extprocSrv, "extproc_phase_errors_total")
			emptyBefore := mustCounter(t, env.extprocSrv, "extproc_empty_responses")

			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(_ *extproc.RequestContext, _ extproc.AllHeaders) error {
					return errors.New("simulated non-abort processor error")
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/mode/buffered/continue-err", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			if got := mustCounter(t, env.extprocSrv, "extproc_phase_errors_total"); got != phaseErrBefore+1 {
				t.Errorf("extproc_phase_errors_total = %v, want %v", got, phaseErrBefore+1)
			}
			if got := mustCounter(t, env.extprocSrv, "extproc_empty_responses"); got != emptyBefore+1 {
				t.Errorf("extproc_empty_responses = %v, want %v", got, emptyBefore+1)
			}
		})

		t.Run("abort_fail_closed", func(t *testing.T) {
			caseID := "err-abort-closed"
			erroredBefore := mustCounter(t, env.abortExtprocSrv, "extproc_errored_streams")

			env.abortExtprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(_ *extproc.RequestContext, _ extproc.AllHeaders) error {
					return errors.New("abort stream fail closed")
				},
			})
			t.Cleanup(func() { env.abortExtprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/profile/abort-fail-closed/test", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, _ := doRequest(t, env.http1Client, req)
			if resp.StatusCode < 500 {
				t.Errorf("abort_fail_closed status = %d, want >= 500", resp.StatusCode)
			}
			if got := mustCounter(t, env.abortExtprocSrv, "extproc_errored_streams"); got != erroredBefore+1 {
				t.Errorf("extproc_errored_streams = %v, want %v", got, erroredBefore+1)
			}
		})

		t.Run("abort_fail_open", func(t *testing.T) {
			caseID := "err-abort-open"
			erroredBefore := mustCounter(t, env.abortExtprocSrv, "extproc_errored_streams")

			env.abortExtprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(_ *extproc.RequestContext, _ extproc.AllHeaders) error {
					return errors.New("abort stream fail open")
				},
			})
			t.Cleanup(func() { env.abortExtprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/profile/abort-fail-open/test", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("abort_fail_open status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			echo := decodeEcho(t, body)
			if echo.Server != "primary" {
				t.Errorf("routed server = %q, want primary", echo.Server)
			}
			if got := mustCounter(t, env.abortExtprocSrv, "extproc_errored_streams"); got != erroredBefore+1 {
				t.Errorf("extproc_errored_streams = %v, want %v", got, erroredBefore+1)
			}
		})
	})

	t.Run("multi_filter_chaining", func(t *testing.T) {
		caseID := "multi-filter-chain"
		rawBytes := []byte{0xff, 0xfe, 'o', 'k'}

		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				_ = ctx.AddHeader("x-chain-step1", extproc.HeaderValue{RawValue: []byte("ok")})
				return ctx.AddHeader("x-raw-bin", extproc.HeaderValue{RawValue: rawBytes})
			},
			RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
				if string(body) != "initial" {
					t.Errorf("first filter saw body %q, want initial", string(body))
				}
				return ctx.ReplaceBody([]byte("after-first"))
			},
		})
		env.secondExtprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, headers extproc.AllHeaders) error {
				if got, ok := headers.Get("x-chain-step1"); !ok || got != "ok" {
					t.Errorf("second filter x-chain-step1 = (%q, %v), want (ok, true)", got, ok)
				}
				raws := headers.RawValues("x-raw-bin")
				if len(raws) != 1 || !bytes.Equal(raws[0], rawBytes) {
					t.Errorf("second filter RawValues(x-raw-bin) = %v, want [%x]", raws, rawBytes)
				}
				_ = ctx.RemoveHeader("x-raw-bin")
				if len(raws) == 1 {
					_ = ctx.AddHeader("x-raw-hex", extproc.HeaderValue{RawValue: []byte(hex.EncodeToString(raws[0]))})
				}
				return ctx.AddHeader("x-chain-step2", extproc.HeaderValue{RawValue: []byte("ok")})
			},
			RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
				if string(body) != "after-first" {
					t.Errorf("second filter saw body %q, want after-first", string(body))
				}
				return ctx.ReplaceBody([]byte("after-second"))
			},
		})
		t.Cleanup(func() {
			env.extprocSrv.Processor.Unregister(caseID)
			env.secondExtprocSrv.Processor.Unregister(caseID)
		})

		req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/chained/test", strings.NewReader("initial"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, env.http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if got := echo.Headers["x-chain-step1"]; !slices.Equal(got, []string{"ok"}) {
			t.Errorf("upstream x-chain-step1 = %v, want [ok]", got)
		}
		if got := echo.Headers["x-chain-step2"]; !slices.Equal(got, []string{"ok"}) {
			t.Errorf("upstream x-chain-step2 = %v, want [ok]", got)
		}
		if got := echo.Headers["x-raw-hex"]; !slices.Equal(got, []string{hex.EncodeToString(rawBytes)}) {
			t.Errorf("upstream x-raw-hex = %v, want [%s]", got, hex.EncodeToString(rawBytes))
		}
		if echo.Body != "after-second" {
			t.Errorf("upstream body = %q, want after-second", echo.Body)
		}
		extprocNames := strings.Join(resp.Header.Values("X-Extproc-Names"), ",")
		if !strings.Contains(extprocNames, "conformance") || !strings.Contains(extprocNames, "conformance-second") {
			t.Errorf("X-Extproc-Names = %v, want both conformance and conformance-second", resp.Header.Values("X-Extproc-Names"))
		}
	})

	t.Run("observability_mode", func(t *testing.T) {
		caseID := "observability-strict"
		emptyBefore := mustCounter(t, env.extprocSrv, "extproc_empty_responses")

		var sawReqHdrs, sawReqBody, sawRespHdrs, sawRespBody atomic.Bool
		var obsReqHdrs, obsReqBody, obsRespHdrs, obsRespBody atomic.Bool
		obsDone := make(chan struct{})
		var doneOnce sync.Once

		env.extprocSrv.Processor.Register(caseID, CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				sawReqHdrs.Store(true)
				obsReqHdrs.Store(ctx.ObservabilityMode)
				_ = ctx.AddHeader("x-obs-mutated", extproc.HeaderValue{RawValue: []byte("req-hdrs")})
				return ctx.ContinueRequest()
			},
			RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
				sawReqBody.Store(true)
				obsReqBody.Store(ctx.ObservabilityMode)
				_ = ctx.AddHeader("x-obs-mutated", extproc.HeaderValue{RawValue: []byte("req-body")})
				return ctx.ContinueRequest()
			},
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				sawRespHdrs.Store(true)
				obsRespHdrs.Store(ctx.ObservabilityMode)
				_ = ctx.AddHeader("x-obs-mutated", extproc.HeaderValue{RawValue: []byte("resp-hdrs")})
				return ctx.ContinueRequest()
			},
			ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
				sawRespBody.Store(true)
				obsRespBody.Store(ctx.ObservabilityMode)
				_ = ctx.AddHeader("x-obs-mutated", extproc.HeaderValue{RawValue: []byte("resp-body")})
				if ctx.EndOfStream {
					doneOnce.Do(func() { close(obsDone) })
				}
				return ctx.ContinueRequest()
			},
		})
		t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

		req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/observability/ping", strings.NewReader("observed-body"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(CaseHeader, caseID)

		resp, body := doRequest(t, env.http1Client, req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
		}
		echo := decodeEcho(t, body)
		if echo.Body != "observed-body" {
			t.Errorf("upstream echo.Body = %q, want %q", echo.Body, "observed-body")
		}
		if got, exists := echo.Headers["x-obs-mutated"]; exists {
			t.Errorf("upstream x-obs-mutated = %v, want absent in observability_mode", got)
		}
		if got := resp.Header.Get("x-obs-mutated"); got != "" {
			t.Errorf("client x-obs-mutated = %q, want empty in observability_mode", got)
		}
		if got := resp.Header.Get("x-extproc-names"); got != "" {
			t.Errorf("client x-extproc-names = %q, want empty in observability_mode", got)
		}

		select {
		case <-obsDone:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for async observability_mode callbacks to finish")
		}

		if !sawReqHdrs.Load() || !sawReqBody.Load() || !sawRespHdrs.Load() || !sawRespBody.Load() {
			t.Errorf("observability phases called: reqHdrs=%v reqBody=%v respHdrs=%v respBody=%v, want all true",
				sawReqHdrs.Load(), sawReqBody.Load(), sawRespHdrs.Load(), sawRespBody.Load())
		}
		if !obsReqHdrs.Load() || !obsReqBody.Load() || !obsRespHdrs.Load() || !obsRespBody.Load() {
			t.Errorf("ctx.ObservabilityMode flags: reqHdrs=%v reqBody=%v respHdrs=%v respBody=%v, want all true",
				obsReqHdrs.Load(), obsReqBody.Load(), obsRespHdrs.Load(), obsRespBody.Load())
		}
		if emptyAfter := mustCounter(t, env.extprocSrv, "extproc_empty_responses"); emptyAfter != emptyBefore {
			t.Errorf("extproc_empty_responses = %v, want %v (unchanged in observability_mode)", emptyAfter, emptyBefore)
		}
		env.runner.AssertNoProtocolErrors(t)
	})

	t.Run("mode_override", func(t *testing.T) {
		t.Run("opt_in_buffered_bodies_and_trailers", func(t *testing.T) {
			reqCase := "mode-override-full-req"
			var reqBodyCalled, reqTrCalled, respHdrsCalled, respTrCalled atomic.Bool
			env.extprocSrv.Processor.Register(reqCase, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.OverrideMode(extproc.ModeOverride{
						RequestBodyMode:     extproc.BodyModeBuffered,
						ResponseBodyMode:    extproc.BodyModeBuffered,
						RequestTrailerMode:  extproc.HeaderModeSend,
						ResponseTrailerMode: extproc.HeaderModeSend,
					})
				},
				RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
					reqBodyCalled.Store(true)
					if string(body) != "raw-req" {
						t.Errorf("RequestBody = %q, want raw-req", string(body))
					}
					return ctx.ReplaceBody([]byte("mutated-req"))
				},
				RequestTrailers: func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
					reqTrCalled.Store(true)
					if got, ok := trailers.Get("x-req-tr"); !ok || got != "orig-req" {
						t.Errorf("RequestTrailers x-req-tr = (%q, %v), want (orig-req, true)", got, ok)
					}
					return ctx.OverwriteHeader("x-req-tr", extproc.HeaderValue{RawValue: []byte("mutated-req-tr")})
				},
				ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					respHdrsCalled.Store(true)
					return ctx.ContinueRequest()
				},
				ResponseTrailers: func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
					respTrCalled.Store(true)
					if got, ok := trailers.Get("x-resp-tr"); !ok || got != "orig-resp" {
						t.Errorf("ResponseTrailers x-resp-tr = (%q, %v), want (orig-resp, true)", got, ok)
					}
					_ = ctx.OverwriteHeader("x-resp-tr", extproc.HeaderValue{RawValue: []byte("mutated-resp-tr")})
					return ctx.AddHeader("x-resp-added-tr", extproc.HeaderValue{RawValue: []byte("added")})
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(reqCase) })

			req1, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/mode-override/full", struct{ io.Reader }{strings.NewReader("raw-req")})
			if err != nil {
				t.Fatal(err)
			}
			req1.ContentLength = -1
			req1.Header.Set(CaseHeader, reqCase)
			req1.Header.Set("x-upstream-trailer-x-resp-tr", "orig-resp")
			req1.Trailer = http.Header{
				"X-Req-Tr": []string{"orig-req"},
			}

			resp1, body1 := doRequest(t, env.h2cClient, req1)
			if resp1.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp1.StatusCode, string(body1))
			}
			if !reqBodyCalled.Load() || !reqTrCalled.Load() || !respHdrsCalled.Load() || !respTrCalled.Load() {
				t.Errorf("callbacks called: reqBody=%v reqTr=%v respHdrs=%v respTr=%v, want all true",
					reqBodyCalled.Load(), reqTrCalled.Load(), respHdrsCalled.Load(), respTrCalled.Load())
			}
			echo := decodeEcho(t, body1)
			if echo.Body != "mutated-req" {
				t.Errorf("upstream echo.Body = %q, want mutated-req", echo.Body)
			}
			if got := echo.Trailers["x-req-tr"]; !slices.Equal(got, []string{"mutated-req-tr"}) {
				t.Errorf("upstream x-req-tr = %v, want [mutated-req-tr]", got)
			}
			if got := resp1.Trailer.Get("X-Resp-Tr"); got != "mutated-resp-tr" {
				t.Errorf("resp trailer X-Resp-Tr = %q, want mutated-resp-tr", got)
			}
			if got := resp1.Trailer.Get("X-Resp-Added-Tr"); got != "added" {
				t.Errorf("resp trailer X-Resp-Added-Tr = %q, want added", got)
			}

			respCase := "mode-override-full-resp"
			var respBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(respCase, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.OverrideMode(extproc.ModeOverride{
						RequestBodyMode:     extproc.BodyModeBuffered,
						ResponseBodyMode:    extproc.BodyModeBuffered,
						RequestTrailerMode:  extproc.HeaderModeSend,
						ResponseTrailerMode: extproc.HeaderModeSend,
					})
				},
				ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
					respBodyCalled.Store(true)
					if string(body) != "raw-resp" {
						t.Errorf("ResponseBody = %q, want raw-resp", string(body))
					}
					return ctx.ReplaceBody([]byte("mutated-resp"))
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(respCase) })

			req2, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/mode-override/full", strings.NewReader("raw-req"))
			if err != nil {
				t.Fatal(err)
			}
			req2.Header.Set(CaseHeader, respCase)
			req2.Header.Set("x-upstream-raw-body", "raw-resp")

			resp2, body2 := doRequest(t, env.h2cClient, req2)
			if resp2.StatusCode != http.StatusOK {
				t.Fatalf("resp replace status = %d, want 200 (body=%q)", resp2.StatusCode, string(body2))
			}
			if !respBodyCalled.Load() {
				t.Error("expected ProcessResponseBody to be invoked via mode_override")
			}
			if string(body2) != "mutated-resp" {
				t.Errorf("client response body = %q, want mutated-resp", string(body2))
			}
		})

		t.Run("skip_remaining_phases", func(t *testing.T) {
			caseID := "mode-override-skip"
			var reqBodyCalled, respHdrsCalled, respBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					if err := ctx.AddHeader("x-fast-path", extproc.HeaderValue{RawValue: []byte("yes")}); err != nil {
						return err
					}
					return ctx.SkipRemainingPhases()
				},
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					reqBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
				ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					respHdrsCalled.Store(true)
					return ctx.ContinueRequest()
				},
				ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
					respBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/mode-override/skip", strings.NewReader("untouched"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			echo := decodeEcho(t, body)
			if got := echo.Headers["x-fast-path"]; !slices.Equal(got, []string{"yes"}) {
				t.Errorf("upstream x-fast-path = %v, want [yes]", got)
			}
			if echo.Body != "untouched" {
				t.Errorf("upstream body = %q, want untouched", echo.Body)
			}
			if reqBodyCalled.Load() || respHdrsCalled.Load() || respBodyCalled.Load() {
				t.Errorf("unexpected callbacks after SkipRemainingPhases: reqBody=%v respHdrs=%v respBody=%v",
					reqBodyCalled.Load(), respHdrsCalled.Load(), respBodyCalled.Load())
			}
			if got := resp.Header.Get("x-extproc-names"); got != "" {
				t.Errorf("x-extproc-names = %q, want empty when ResponseHeaders skipped", got)
			}
		})

		t.Run("response_headers_streamed_body", func(t *testing.T) {
			caseID := "mode-override-resp-stream"
			var reqBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					reqBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
				ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.OverrideMode(extproc.ModeOverride{
						ResponseBodyMode: extproc.BodyModeStreamed,
					})
				},
				ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
					if len(body) > 0 {
						return ctx.ReplaceBodyChunk(bytes.ToUpper(body))
					}
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/profile/mode-override/resp-stream", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)
			req.Header.Set("x-upstream-raw-body", "part1-part2-part3")
			req.Header.Set("x-upstream-chunks", "3")

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			if reqBodyCalled.Load() {
				t.Error("RequestBody should not run under NONE baseline")
			}
			if string(body) != "PART1-PART2-PART3" {
				t.Errorf("streamed mutated body = %q, want PART1-PART2-PART3", string(body))
			}
		})

		t.Run("ignored_when_disabled_on_filter", func(t *testing.T) {
			caseID := "mode-override-disabled"
			var reqBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.OverrideMode(extproc.ModeOverride{
						RequestBodyMode: extproc.BodyModeBuffered,
					})
				},
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					reqBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/mode/none/override-disabled", strings.NewReader("no-override"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			if reqBodyCalled.Load() {
				t.Error("RequestBody should not run when allow_mode_override is false on filter")
			}
		})

		t.Run("allowed_override_modes_allowlist", func(t *testing.T) {
			allowedCase := "mode-override-allowlist-ok"
			var allowedBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(allowedCase, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.OverrideMode(extproc.ModeOverride{
						RequestBodyMode: extproc.BodyModeStreamed,
					})
				},
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					allowedBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(allowedCase) })

			req1, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/mode-override-allowlist/check", strings.NewReader("probe"))
			if err != nil {
				t.Fatal(err)
			}
			req1.Header.Set(CaseHeader, allowedCase)
			resp1, body1 := doRequest(t, env.http1Client, req1)
			if resp1.StatusCode != http.StatusOK {
				t.Fatalf("allowed status = %d, want 200 (body=%q)", resp1.StatusCode, string(body1))
			}
			if !allowedBodyCalled.Load() {
				t.Error("expected RequestBody to run when OverrideMode matches allowed_override_modes")
			}

			disallowedCase := "mode-override-allowlist-denied"
			var disallowedBodyCalled atomic.Bool
			env.extprocSrv.Processor.Register(disallowedCase, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					return ctx.OverrideMode(extproc.ModeOverride{
						RequestBodyMode: extproc.BodyModeBuffered,
					})
				},
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					disallowedBodyCalled.Store(true)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(disallowedCase) })

			req2, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/mode-override-allowlist/check", strings.NewReader("probe"))
			if err != nil {
				t.Fatal(err)
			}
			req2.Header.Set(CaseHeader, disallowedCase)
			resp2, body2 := doRequest(t, env.http1Client, req2)
			if resp2.StatusCode != http.StatusOK {
				t.Fatalf("disallowed status = %d, want 200 (body=%q)", resp2.StatusCode, string(body2))
			}
			if disallowedBodyCalled.Load() {
				t.Error("RequestBody should not run when OverrideMode is outside allowed_override_modes")
			}
		})
	})

	t.Run("protocol_config", func(t *testing.T) {
		t.Run("body_modes_and_cross_phase_persistence", func(t *testing.T) {
			cases := []struct {
				name         string
				path         string
				wantReqBody  extproc.BodySendMode
				wantRespBody extproc.BodySendMode
				expectBodies bool
			}{
				{
					name:         "none",
					path:         "/mode/none/protocol-config",
					wantReqBody:  extproc.BodyModeNone,
					wantRespBody: extproc.BodyModeNone,
					expectBodies: false,
				},
				{
					name:         "buffered",
					path:         "/mode/buffered/protocol-config",
					wantReqBody:  extproc.BodyModeBuffered,
					wantRespBody: extproc.BodyModeBuffered,
					expectBodies: true,
				},
				{
					name:         "streamed",
					path:         "/mode/streamed/protocol-config",
					wantReqBody:  extproc.BodyModeStreamed,
					wantRespBody: extproc.BodyModeStreamed,
					expectBodies: true,
				},
				{
					name:         "buffered_partial",
					path:         "/mode/buffered-partial/protocol-config",
					wantReqBody:  extproc.BodyModeBufferedPartial,
					wantRespBody: extproc.BodyModeBufferedPartial,
					expectBodies: true,
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					caseID := "proto-cfg-" + tc.name
					var mu sync.Mutex
					observed := map[string]extproc.ProtocolConfig{}
					var nilPhases []string
					record := func(phaseName string, ctx *extproc.RequestContext) {
						mu.Lock()
						defer mu.Unlock()
						if ctx.ProtocolConfig == nil {
							nilPhases = append(nilPhases, phaseName)
							return
						}
						observed[phaseName] = *ctx.ProtocolConfig
					}

					env.extprocSrv.Processor.Register(caseID, CaseHandler{
						RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
							record("req_headers", ctx)
							return ctx.ContinueRequest()
						},
						RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
							record("req_body", ctx)
							return ctx.ContinueRequest()
						},
						ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
							record("resp_headers", ctx)
							return ctx.ContinueRequest()
						},
						ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
							record("resp_body", ctx)
							return ctx.ContinueRequest()
						},
					})
					t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

					req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+tc.path, strings.NewReader("probe-body"))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set(CaseHeader, caseID)

					resp, body := doRequest(t, env.http1Client, req)
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
					}

					mu.Lock()
					defer mu.Unlock()
					if len(nilPhases) > 0 {
						t.Fatalf("phases observed nil ProtocolConfig: %v", nilPhases)
					}
					expectedPhases := []string{"req_headers", "resp_headers"}
					if tc.expectBodies {
						expectedPhases = append(expectedPhases, "req_body", "resp_body")
					}
					wantCfg := extproc.ProtocolConfig{
						RequestBodyMode:                         tc.wantReqBody,
						ResponseBodyMode:                        tc.wantRespBody,
						SendBodyWithoutWaitingForHeaderResponse: false,
					}
					for _, p := range expectedPhases {
						got, ok := observed[p]
						if !ok {
							t.Errorf("phase %s did not run or record ProtocolConfig", p)
							continue
						}
						if got != wantCfg {
							t.Errorf("phase %s ProtocolConfig = %+v, want %+v", p, got, wantCfg)
						}
					}
				})
			}
		})

		t.Run("skip_request_headers_first_message", func(t *testing.T) {
			caseID := "proto-cfg-skip-req-hdrs"
			var mu sync.Mutex
			var reqHdrsCalled atomic.Bool
			observed := map[string]extproc.ProtocolConfig{}
			var nilPhases []string
			record := func(phaseName string, ctx *extproc.RequestContext) {
				mu.Lock()
				defer mu.Unlock()
				if ctx.ProtocolConfig == nil {
					nilPhases = append(nilPhases, phaseName)
					return
				}
				observed[phaseName] = *ctx.ProtocolConfig
			}

			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					reqHdrsCalled.Store(true)
					return ctx.ContinueRequest()
				},
				ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					record("resp_headers", ctx)
					return ctx.ContinueRequest()
				},
				ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
					record("resp_body", ctx)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodGet, env.runner.ProxyURL()+"/mode/skip-req-headers/protocol-config", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			if reqHdrsCalled.Load() {
				t.Error("RequestHeaders should not run under request_header_mode: SKIP")
			}

			mu.Lock()
			defer mu.Unlock()
			if len(nilPhases) > 0 {
				t.Fatalf("phases observed nil ProtocolConfig: %v", nilPhases)
			}
			wantCfg := extproc.ProtocolConfig{
				RequestBodyMode:                         extproc.BodyModeNone,
				ResponseBodyMode:                        extproc.BodyModeBuffered,
				SendBodyWithoutWaitingForHeaderResponse: false,
			}
			for _, p := range []string{"resp_headers", "resp_body"} {
				got, ok := observed[p]
				if !ok {
					t.Errorf("phase %s did not run or record ProtocolConfig", p)
					continue
				}
				if got != wantCfg {
					t.Errorf("phase %s ProtocolConfig = %+v, want %+v", p, got, wantCfg)
				}
			}
		})

		t.Run("send_body_without_waiting_for_header_response", func(t *testing.T) {
			caseID := "proto-cfg-no-wait"
			var mu sync.Mutex
			observed := map[string]extproc.ProtocolConfig{}
			var nilPhases []string
			record := func(phaseName string, ctx *extproc.RequestContext) {
				mu.Lock()
				defer mu.Unlock()
				if ctx.ProtocolConfig == nil {
					nilPhases = append(nilPhases, phaseName)
					return
				}
				observed[phaseName] = *ctx.ProtocolConfig
			}

			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					record("req_headers", ctx)
					return ctx.ContinueRequest()
				},
				RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
					record("req_body", ctx)
					return ctx.ContinueRequest()
				},
				ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					record("resp_headers", ctx)
					return ctx.ContinueRequest()
				},
				ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
					record("resp_body", ctx)
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/send-body-without-waiting/check", strings.NewReader("fast-stream-body"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			echo := decodeEcho(t, body)
			if echo.Body != "fast-stream-body" {
				t.Errorf("upstream body = %q, want fast-stream-body", echo.Body)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(nilPhases) > 0 {
				t.Fatalf("phases observed nil ProtocolConfig: %v", nilPhases)
			}
			wantCfg := extproc.ProtocolConfig{
				RequestBodyMode:                         extproc.BodyModeStreamed,
				ResponseBodyMode:                        extproc.BodyModeStreamed,
				SendBodyWithoutWaitingForHeaderResponse: true,
			}
			for _, p := range []string{"req_headers", "req_body", "resp_headers", "resp_body"} {
				got, ok := observed[p]
				if !ok {
					t.Errorf("phase %s did not run or record ProtocolConfig", p)
					continue
				}
				if got != wantCfg {
					t.Errorf("phase %s ProtocolConfig = %+v, want %+v", p, got, wantCfg)
				}
			}
			env.runner.AssertNoProtocolErrors(t)
		})

		t.Run("default_mode_override_preserves_response_body_mode", func(t *testing.T) {
			caseID := "proto-cfg-default-override"
			var respBodyCalled atomic.Bool
			var hdrErr string

			env.extprocSrv.Processor.Register(caseID, CaseHandler{
				RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
					want := extproc.ProtocolConfig{
						RequestBodyMode:  extproc.BodyModeNone,
						ResponseBodyMode: extproc.BodyModeBuffered,
					}
					if ctx.ProtocolConfig == nil || *ctx.ProtocolConfig != want {
						hdrErr = "unexpected ProtocolConfig in RequestHeaders"
						return ctx.ContinueRequest()
					}
					mode := ctx.DefaultModeOverride()
					mode.RequestBodyMode = extproc.BodyModeBuffered
					return ctx.OverrideMode(mode)
				},
				RequestBody: func(ctx *extproc.RequestContext, body []byte) error {
					if string(body) != "raw-req" {
						t.Errorf("RequestBody = %q, want raw-req", string(body))
					}
					return ctx.ReplaceBody([]byte("mutated-req"))
				},
				ResponseBody: func(ctx *extproc.RequestContext, body []byte) error {
					respBodyCalled.Store(true)
					if len(body) == 0 {
						t.Error("expected non-empty buffered ResponseBody")
					}
					return ctx.ContinueRequest()
				},
			})
			t.Cleanup(func() { env.extprocSrv.Processor.Unregister(caseID) })

			req, err := http.NewRequest(http.MethodPost, env.runner.ProxyURL()+"/profile/mode-override-resp-buffered/check", strings.NewReader("raw-req"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(CaseHeader, caseID)

			resp, body := doRequest(t, env.http1Client, req)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", resp.StatusCode, string(body))
			}
			if hdrErr != "" {
				t.Fatal(hdrErr)
			}
			if !respBodyCalled.Load() {
				t.Error("ResponseBody should remain BUFFERED when using DefaultModeOverride")
			}
			echo := decodeEcho(t, body)
			if echo.Body != "mutated-req" {
				t.Errorf("upstream echo.Body = %q, want mutated-req", echo.Body)
			}
			env.runner.AssertNoProtocolErrors(t)
		})
	})
}
