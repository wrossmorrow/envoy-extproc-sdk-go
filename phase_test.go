package extproc

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproccfgv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestDefaultPhaseIfInvalid(t *testing.T) {
	valid := []int{
		REQUEST_PHASE_UNDETERMINED,
		REQUEST_PHASE_REQUEST_HEADERS,
		REQUEST_PHASE_REQUEST_BODY,
		REQUEST_PHASE_REQUEST_TRAILERS,
		REQUEST_PHASE_RESPONSE_HEADERS,
		REQUEST_PHASE_RESPONSE_BODY,
		REQUEST_PHASE_RESPONSE_TRAILERS,
	}
	for _, p := range valid {
		if got := defaultPhaseIfInvalid(p); got != p {
			t.Errorf("defaultPhaseIfInvalid(%d) = %d, want %d", p, got, p)
		}
	}

	for _, p := range []int{-1, 42, 99} {
		if got := defaultPhaseIfInvalid(p); got != REQUEST_PHASE_UNDETERMINED {
			t.Errorf("defaultPhaseIfInvalid(%d) = %d, want %d", p, got, REQUEST_PHASE_UNDETERMINED)
		}
	}
}

func TestRequestPhaseToString(t *testing.T) {
	cases := []struct {
		phase int
		want  string
	}{
		{REQUEST_PHASE_UNDETERMINED, "UNDETERMINED"},
		{REQUEST_PHASE_REQUEST_HEADERS, "REQUEST_HEADERS"},
		{REQUEST_PHASE_REQUEST_BODY, "REQUEST_BODY"},
		{REQUEST_PHASE_REQUEST_TRAILERS, "REQUEST_TRAILERS"},
		{REQUEST_PHASE_RESPONSE_HEADERS, "RESPONSE_HEADERS"},
		{REQUEST_PHASE_RESPONSE_BODY, "RESPONSE_BODY"},
		{REQUEST_PHASE_RESPONSE_TRAILERS, "RESPONSE_TRAILERS"},
		{42, "UNKNOWN(42)"},
	}
	for _, c := range cases {
		if got := RequestPhaseToString(c.phase); got != c.want {
			t.Errorf("RequestPhaseToString(%d) = %q, want %q", c.phase, got, c.want)
		}
	}
}

func TestRequestContextPhaseLifecycle(t *testing.T) {
	rc := newRequestContext()
	if rc.GetPhase() != REQUEST_PHASE_UNDETERMINED {
		t.Errorf("fresh context phase = %d, want %d", rc.GetPhase(), REQUEST_PHASE_UNDETERMINED)
	}
	if rc.GetPhaseName() != "UNDETERMINED" {
		t.Errorf("fresh context phase name = %q, want %q", rc.GetPhaseName(), "UNDETERMINED")
	}
	if rc.EndOfStream {
		t.Error("fresh context should not have EndOfStream set")
	}

	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}
	if rc.GetPhase() != REQUEST_PHASE_UNDETERMINED {
		t.Errorf("phase after ResetPhase = %d, want %d", rc.GetPhase(), REQUEST_PHASE_UNDETERMINED)
	}
	if rc.EndOfStream {
		t.Error("EndOfStream should be cleared by ResetPhase")
	}

	rc.setPhase(REQUEST_PHASE_RESPONSE_BODY)
	if rc.GetPhase() != REQUEST_PHASE_RESPONSE_BODY {
		t.Errorf("phase after setPhase = %d, want %d", rc.GetPhase(), REQUEST_PHASE_RESPONSE_BODY)
	}
	if rc.GetPhaseName() != "RESPONSE_BODY" {
		t.Errorf("phase name after setPhase = %q, want %q", rc.GetPhaseName(), "RESPONSE_BODY")
	}

	// invalid phases fall back to the default
	rc.setPhase(99)
	if rc.GetPhase() != REQUEST_PHASE_UNDETERMINED {
		t.Errorf("phase after setPhase(99) = %d, want %d", rc.GetPhase(), REQUEST_PHASE_UNDETERMINED)
	}

	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	// ResetPhase must clear the whole response state, not just the phase
	if rc.response.continueRequest != nil {
		t.Error("ResetPhase should clear continueRequest")
	}
	if rc.response.immediateResponse != nil {
		t.Error("ResetPhase should clear immediateResponse")
	}
	if rc.response.headerMutation == nil {
		t.Fatal("ResetPhase should (re)create headerMutation")
	}
	if len(rc.response.headerMutation.SetHeaders) != 0 {
		t.Errorf("ResetPhase should clear SetHeaders, got %v", rc.response.headerMutation.SetHeaders)
	}
	if len(rc.response.headerMutation.RemoveHeaders) != 0 {
		t.Errorf("ResetPhase should clear RemoveHeaders, got %v", rc.response.headerMutation.RemoveHeaders)
	}
	if rc.response.bodyMutation != nil {
		t.Error("ResetPhase should clear bodyMutation")
	}
	if rc.response.clearRouteCache {
		t.Error("ResetPhase should clear clearRouteCache")
	}
}

func TestContinueRequestClearsImmediateResponse(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if err := rc.CancelRequest(503, map[string]HeaderValue{"x-a": {RawValue: []byte("b")}}, "nope"); err != nil {
		t.Fatal(err)
	}
	if rc.response.immediateResponse == nil {
		t.Fatal("expected an immediate response after CancelRequest")
	}
	if rc.response.continueRequest != nil {
		t.Fatal("CancelRequest should not leave a continue response behind")
	}

	if err := rc.ContinueRequest(); err != nil {
		t.Fatal(err)
	}
	if rc.response.immediateResponse != nil {
		t.Error("ContinueRequest should clear the immediate response")
	}
	if rc.response.continueRequest == nil {
		t.Fatal("ContinueRequest should set a continue response")
	}
	if rc.response.continueRequest.GetStatus() != extprocv3.CommonResponse_CONTINUE {
		t.Errorf("continue status = %v, want %v", rc.response.continueRequest.GetStatus(), extprocv3.CommonResponse_CONTINUE)
	}
	if rc.response.continueRequest.GetClearRouteCache() {
		t.Error("clearRouteCache should default to false")
	}
	if rc.response.continueRequest.GetHeaderMutation() == nil {
		t.Error("continue response should carry the header mutation")
	}
	if rc.response.continueRequest.GetBodyMutation() != nil {
		t.Error("continue response should not carry a body mutation when none was set")
	}
}

func TestContinueRequestHonorsClearRouteCache(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	rc.ClearRouteCache()
	if err := rc.ContinueRequest(); err != nil {
		t.Fatal(err)
	}
	if !rc.response.continueRequest.GetClearRouteCache() {
		t.Error("ClearRouteCache was not honored in the continue response")
	}

	rc.UnClearRouteCache()
	if err := rc.ContinueRequest(); err != nil {
		t.Fatal(err)
	}
	if rc.response.continueRequest.GetClearRouteCache() {
		t.Error("UnClearRouteCache did not clear clearRouteCache")
	}
}

// An external processor that clears the route cache (i.e. is changing the
// upstream, typically by rewriting a routed header such as `:authority`) must
// surface that decision in the envoy-facing response, or envoy will keep serving
// from its cached route.
func TestGetResponsePropagatesClearRouteCache(t *testing.T) {
	t.Run(RequestPhaseToString(REQUEST_PHASE_REQUEST_HEADERS), func(t *testing.T) {
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		rc.ClearRouteCache()
		resp, err := rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
		if err != nil {
			t.Fatalf("GetResponse(%d) returned error: %v", REQUEST_PHASE_REQUEST_HEADERS, err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if !resp.GetRequestHeaders().GetResponse().GetClearRouteCache() {
			t.Error("expected ClearRouteCache to be set in the envoy-facing response when the processor clears it")
		}

		rc.UnClearRouteCache()
		resp, err = rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
		if err != nil {
			t.Fatalf("GetResponse(%d) returned error: %v", REQUEST_PHASE_REQUEST_HEADERS, err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if resp.GetRequestHeaders().GetResponse().GetClearRouteCache() {
			t.Error("expected ClearRouteCache to be cleared after UnClearRouteCache")
		}
	})

	t.Run(RequestPhaseToString(REQUEST_PHASE_REQUEST_BODY), func(t *testing.T) {
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		rc.ClearRouteCache()
		resp, err := rc.GetResponse(REQUEST_PHASE_REQUEST_BODY)
		if err != nil {
			t.Fatalf("GetResponse(%d) returned error: %v", REQUEST_PHASE_REQUEST_BODY, err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if !resp.GetRequestBody().GetResponse().GetClearRouteCache() {
			t.Error("expected ClearRouteCache to be set in the envoy-facing response for the request body phase")
		}
	})

	t.Run("default is false", func(t *testing.T) {
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}

		resp, err := rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
		if err != nil {
			t.Fatalf("GetResponse(%d) returned error: %v", REQUEST_PHASE_REQUEST_HEADERS, err)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("resp.ValidateAll() = %v, want nil", verr)
		}
		if resp.GetRequestHeaders().GetResponse().GetClearRouteCache() {
			t.Error("expected ClearRouteCache to default to false")
		}
	})
}

func TestGetResponseReturnsTypedResponsePerPhase(t *testing.T) {
	cases := []struct {
		phase int
		check func(t *testing.T, resp *extprocv3.ProcessingResponse)
	}{
		{
			phase: REQUEST_PHASE_REQUEST_HEADERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetRequestHeaders() == nil {
					t.Fatal("expected a ProcessingResponse_RequestHeaders")
				}
				if cr := resp.GetRequestHeaders().GetResponse(); cr == nil {
					t.Fatal("expected the request headers response to carry a CommonResponse")
				} else if cr.GetStatus() != extprocv3.CommonResponse_CONTINUE {
					t.Errorf("continue status = %v, want %v", cr.GetStatus(), extprocv3.CommonResponse_CONTINUE)
				}
			},
		},
		{
			phase: REQUEST_PHASE_REQUEST_BODY,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetRequestBody() == nil {
					t.Fatal("expected a ProcessingResponse_RequestBody")
				}
				if cr := resp.GetRequestBody().GetResponse(); cr == nil {
					t.Fatal("expected the request body response to carry a CommonResponse")
				} else if cr.GetStatus() != extprocv3.CommonResponse_CONTINUE {
					t.Errorf("continue status = %v, want %v", cr.GetStatus(), extprocv3.CommonResponse_CONTINUE)
				}
			},
		},
		{
			phase: REQUEST_PHASE_REQUEST_TRAILERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetRequestTrailers() == nil {
					t.Fatal("expected a ProcessingResponse_RequestTrailers")
				}
				if hm := resp.GetRequestTrailers().GetHeaderMutation(); hm == nil {
					t.Fatal("expected the trailers response to carry a HeaderMutation")
				}
			},
		},
		{
			phase: REQUEST_PHASE_RESPONSE_HEADERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetResponseHeaders() == nil {
					t.Fatal("expected a ProcessingResponse_ResponseHeaders")
				}
				if cr := resp.GetResponseHeaders().GetResponse(); cr == nil {
					t.Fatal("expected the response headers response to carry a CommonResponse")
				} else if cr.GetStatus() != extprocv3.CommonResponse_CONTINUE {
					t.Errorf("continue status = %v, want %v", cr.GetStatus(), extprocv3.CommonResponse_CONTINUE)
				}
			},
		},
		{
			phase: REQUEST_PHASE_RESPONSE_BODY,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetResponseBody() == nil {
					t.Fatal("expected a ProcessingResponse_ResponseBody")
				}
				if cr := resp.GetResponseBody().GetResponse(); cr == nil {
					t.Fatal("expected the response body response to carry a CommonResponse")
				} else if cr.GetStatus() != extprocv3.CommonResponse_CONTINUE {
					t.Errorf("continue status = %v, want %v", cr.GetStatus(), extprocv3.CommonResponse_CONTINUE)
				}
			},
		},
		{
			phase: REQUEST_PHASE_RESPONSE_TRAILERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetResponseTrailers() == nil {
					t.Fatal("expected a ProcessingResponse_ResponseTrailers")
				}
				if hm := resp.GetResponseTrailers().GetHeaderMutation(); hm == nil {
					t.Fatal("expected the trailers response to carry a HeaderMutation")
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(RequestPhaseToString(c.phase), func(t *testing.T) {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			resp, err := rc.GetResponse(c.phase)
			if err != nil {
				t.Fatalf("GetResponse(%d) returned error: %v", c.phase, err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("resp.ValidateAll() = %v, want nil", verr)
			}
			c.check(t, resp)
		})
	}
}

func TestGetResponseRejectsUnknownPhase(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}
	resp, err := rc.GetResponse(42)
	if err == nil {
		t.Fatal("expected an error for an unknown phase")
	}
	if resp != nil {
		t.Error("expected no response for an unknown phase")
	}
}

func TestEmptyContinueResponsePerPhase(t *testing.T) {
	cases := []struct {
		phase int
		check func(t *testing.T, resp *extprocv3.ProcessingResponse)
	}{
		{
			phase: REQUEST_PHASE_REQUEST_HEADERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetRequestHeaders() == nil {
					t.Fatal("expected a ProcessingResponse_RequestHeaders")
				}
				if cr := resp.GetRequestHeaders().GetResponse(); cr == nil {
					t.Fatal("expected an empty continue response")
				}
			},
		},
		{
			phase: REQUEST_PHASE_REQUEST_BODY,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetRequestBody() == nil {
					t.Fatal("expected a ProcessingResponse_RequestBody")
				}
				if cr := resp.GetRequestBody().GetResponse(); cr == nil {
					t.Fatal("expected an empty continue response")
				}
			},
		},
		{
			phase: REQUEST_PHASE_REQUEST_TRAILERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetRequestTrailers() == nil {
					t.Fatal("expected a ProcessingResponse_RequestTrailers")
				}
				if hm := resp.GetRequestTrailers().GetHeaderMutation(); hm == nil {
					t.Fatal("expected an empty header mutation for trailers")
				}
			},
		},
		{
			phase: REQUEST_PHASE_RESPONSE_HEADERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetResponseHeaders() == nil {
					t.Fatal("expected a ProcessingResponse_ResponseHeaders")
				}
				if cr := resp.GetResponseHeaders().GetResponse(); cr == nil {
					t.Fatal("expected an empty continue response")
				}
			},
		},
		{
			phase: REQUEST_PHASE_RESPONSE_BODY,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetResponseBody() == nil {
					t.Fatal("expected a ProcessingResponse_ResponseBody")
				}
				if cr := resp.GetResponseBody().GetResponse(); cr == nil {
					t.Fatal("expected an empty continue response")
				}
			},
		},
		{
			phase: REQUEST_PHASE_RESPONSE_TRAILERS,
			check: func(t *testing.T, resp *extprocv3.ProcessingResponse) {
				if resp.GetResponseTrailers() == nil {
					t.Fatal("expected a ProcessingResponse_ResponseTrailers")
				}
				if hm := resp.GetResponseTrailers().GetHeaderMutation(); hm == nil {
					t.Fatal("expected an empty header mutation for trailers")
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(RequestPhaseToString(c.phase), func(t *testing.T) {
			resp, err := newRequestContext().EmptyContinueResponse(c.phase)
			if err != nil {
				t.Fatalf("EmptyContinueResponse(%d) returned error: %v", c.phase, err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("resp.ValidateAll() = %v, want nil", verr)
			}
			c.check(t, resp)
		})
	}
}

func TestEmptyContinueResponseRejectsUnknownPhase(t *testing.T) {
	resp, err := newRequestContext().EmptyContinueResponse(42)
	if err == nil {
		t.Fatal("expected an error for an unknown phase")
	}
	if resp != nil {
		t.Error("expected no response for an unknown phase")
	}
}

func TestGetResponseImmediateResponse(t *testing.T) {
	// phases that can carry an ImmediateResponse: request/response headers and
	// request/response bodies. Trailers phases cannot (only header mutations are
	// permitted there).
	immediateCapable := []int{
		REQUEST_PHASE_REQUEST_HEADERS,
		REQUEST_PHASE_REQUEST_BODY,
		REQUEST_PHASE_RESPONSE_HEADERS,
		REQUEST_PHASE_RESPONSE_BODY,
	}

	for _, phase := range immediateCapable {
		t.Run(RequestPhaseToString(phase), func(t *testing.T) {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			if err := rc.CancelRequest(503, map[string]HeaderValue{"x-a": {RawValue: []byte("b")}}, "nope"); err != nil {
				t.Fatal(err)
			}

			resp, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatalf("GetResponse(%d) returned error: %v", phase, err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("resp.ValidateAll() = %v, want nil", verr)
			}

			ir := resp.GetImmediateResponse()
			if ir == nil {
				t.Fatalf("expected an ImmediateResponse for phase %d", phase)
			}
			if ir.GetStatus() == nil {
				t.Fatalf("expected a status on the ImmediateResponse for phase %d", phase)
			}
			if code := ir.GetStatus().GetCode(); code != 503 {
				t.Errorf("immediate status code = %d, want 503", code)
			}
			if ir.GetHeaders() == nil {
				t.Errorf("expected header mutations on the ImmediateResponse for phase %d", phase)
			}
			if string(ir.GetBody()) != "nope" {
				t.Errorf("immediate body = %q, want %q", string(ir.GetBody()), "nope")
			}
		})
	}
}

func TestGetResponseIgnoresImmediateResponseForTrailers(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}
	if err := rc.CancelRequest(503, map[string]HeaderValue{"x-a": {RawValue: []byte("b")}}, "nope"); err != nil {
		t.Fatal(err)
	}

	rc.setPhase(REQUEST_PHASE_RESPONSE_TRAILERS)
	resp, err := rc.GetResponse(REQUEST_PHASE_RESPONSE_TRAILERS)
	if err != nil {
		t.Fatalf("GetResponse(%d) returned error: %v", REQUEST_PHASE_RESPONSE_TRAILERS, err)
	}
	if verr := resp.ValidateAll(); verr != nil {
		t.Errorf("resp.ValidateAll() = %v, want nil", verr)
	}
	if rc.GetPhase() != REQUEST_PHASE_RESPONSE_TRAILERS {
		t.Errorf("context phase = %d, want %d", rc.GetPhase(), REQUEST_PHASE_RESPONSE_TRAILERS)
	}
	// trailers phases cannot answer with an ImmediateResponse; the SDK must fall
	// back to the (header-only) common response
	if resp.GetImmediateResponse() != nil {
		t.Error("expected no ImmediateResponse for a trailers phase")
	}
	if resp.GetResponseTrailers() == nil {
		t.Fatal("expected a ProcessingResponse_ResponseTrailers")
	}
	if resp.GetResponseTrailers().GetHeaderMutation() == nil {
		t.Fatal("expected the trailers response to carry a HeaderMutation")
	}
}

func TestGetResponseUnknownPhase(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}
	resp, err := rc.GetResponse(42)
	if err == nil {
		t.Fatal("expected an error for an unknown phase")
	}
	if resp != nil {
		t.Error("expected no response for an unknown phase")
	}
}

func TestResponsesValidateAgainstPGVSchema(t *testing.T) {
	applyHeaderMutations := func(t *testing.T, rc *RequestContext) {
		t.Helper()
		if err := rc.AddHeader("x-add", HeaderValue{RawValue: []byte("v1")}); err != nil {
			t.Fatal(err)
		}
		if err := rc.AppendHeader("x-append", HeaderValue{Value: "v2"}); err != nil {
			t.Fatal(err)
		}
		if err := rc.OverwriteHeader("x-overwrite", HeaderValue{RawValue: []byte("v3")}); err != nil {
			t.Fatal(err)
		}
		if err := rc.UpdateHeader("x-update", HeaderValue{RawValue: []byte("v4")}, "OVERWRITE_IF_EXISTS"); err != nil {
			t.Fatal(err)
		}
		if err := rc.RemoveHeader("x-remove"); err != nil {
			t.Fatal(err)
		}
	}

	headerPhases := []int{
		REQUEST_PHASE_REQUEST_HEADERS,
		REQUEST_PHASE_RESPONSE_HEADERS,
		REQUEST_PHASE_REQUEST_TRAILERS,
		REQUEST_PHASE_RESPONSE_TRAILERS,
	}
	for _, phase := range headerPhases {
		t.Run("headers_"+RequestPhaseToString(phase), func(t *testing.T) {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			applyHeaderMutations(t, rc)
			resp, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatalf("GetResponse(%d) returned error: %v", phase, err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("resp.ValidateAll() = %v, want nil", verr)
			}
		})
	}

	bodyPhases := []int{
		REQUEST_PHASE_REQUEST_BODY,
		REQUEST_PHASE_RESPONSE_BODY,
	}
	for _, phase := range bodyPhases {
		for _, action := range []struct {
			name string
			fn   func(rc *RequestContext) error
		}{
			{"replace_body", func(rc *RequestContext) error { return rc.ReplaceBody([]byte("payload")) }},
			{"clear_body", func(rc *RequestContext) error { return rc.ClearBody() }},
			{"replace_body_chunk", func(rc *RequestContext) error { return rc.ReplaceBodyChunk([]byte("chunk")) }},
			{"clear_body_chunk", func(rc *RequestContext) error { return rc.ClearBodyChunk() }},
		} {
			t.Run(RequestPhaseToString(phase)+"_"+action.name, func(t *testing.T) {
				rc := newRequestContext()
				if err := rc.ResetPhase(); err != nil {
					t.Fatal(err)
				}
				if err := action.fn(rc); err != nil {
					t.Fatal(err)
				}
				resp, err := rc.GetResponse(phase)
				if err != nil {
					t.Fatalf("GetResponse(%d) returned error: %v", phase, err)
				}
				if verr := resp.ValidateAll(); verr != nil {
					t.Errorf("resp.ValidateAll() = %v, want nil", verr)
				}
			})
		}
	}

	immediatePhases := []int{
		REQUEST_PHASE_REQUEST_HEADERS,
		REQUEST_PHASE_REQUEST_BODY,
		REQUEST_PHASE_RESPONSE_HEADERS,
		REQUEST_PHASE_RESPONSE_BODY,
	}
	for _, phase := range immediatePhases {
		t.Run("immediate_"+RequestPhaseToString(phase), func(t *testing.T) {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			if err := rc.CancelRequest(429, map[string]HeaderValue{"x-local": {RawValue: []byte("yes")}}, "rate-limited"); err != nil {
				t.Fatal(err)
			}
			resp, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatalf("GetResponse(%d) returned error: %v", phase, err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("resp.ValidateAll() = %v, want nil", verr)
			}
		})
	}

	for _, phase := range []int{REQUEST_PHASE_REQUEST_HEADERS, REQUEST_PHASE_RESPONSE_HEADERS} {
		for _, tc := range []struct {
			name string
			fn   func(rc *RequestContext) error
		}{
			{
				name: "override_mode",
				fn: func(rc *RequestContext) error {
					return rc.OverrideMode(ModeOverride{
						ResponseHeaderMode:  HeaderModeDefault,
						RequestBodyMode:     BodyModeBuffered,
						ResponseBodyMode:    BodyModeStreamed,
						RequestTrailerMode:  HeaderModeSend,
						ResponseTrailerMode: HeaderModeSkip,
					})
				},
			},
			{
				name: "skip_remaining_phases",
				fn:   func(rc *RequestContext) error { return rc.SkipRemainingPhases() },
			},
		} {
			t.Run(tc.name+"_"+RequestPhaseToString(phase), func(t *testing.T) {
				rc := newRequestContext()
				if err := rc.ResetPhase(); err != nil {
					t.Fatal(err)
				}
				rc.setPhase(phase)
				if err := tc.fn(rc); err != nil {
					t.Fatal(err)
				}
				resp, err := rc.GetResponse(phase)
				if err != nil {
					t.Fatalf("GetResponse(%d) returned error: %v", phase, err)
				}
				if verr := resp.ValidateAll(); verr != nil {
					t.Errorf("resp.ValidateAll() = %v, want nil", verr)
				}
			})
		}
	}
}

func TestOverrideModeValidatesEnumsAndPhases(t *testing.T) {
	t.Run("allowed_phases", func(t *testing.T) {
		for _, phase := range []int{
			REQUEST_PHASE_UNDETERMINED,
			REQUEST_PHASE_REQUEST_HEADERS,
			REQUEST_PHASE_RESPONSE_HEADERS,
		} {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			rc.setPhase(phase)
			if err := rc.OverrideMode(ModeOverride{RequestBodyMode: BodyModeBuffered}); err != nil {
				t.Errorf("OverrideMode in %s returned error: %v", RequestPhaseToString(phase), err)
			}
			if err := rc.SkipRemainingPhases(); err != nil {
				t.Errorf("SkipRemainingPhases in %s returned error: %v", RequestPhaseToString(phase), err)
			}
		}
	})

	t.Run("disallowed_phases_fail_without_mutating", func(t *testing.T) {
		for _, phase := range []int{
			REQUEST_PHASE_REQUEST_BODY,
			REQUEST_PHASE_RESPONSE_BODY,
			REQUEST_PHASE_REQUEST_TRAILERS,
			REQUEST_PHASE_RESPONSE_TRAILERS,
		} {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			rc.setPhase(phase)
			if err := rc.OverrideMode(ModeOverride{ResponseBodyMode: BodyModeBuffered}); err == nil {
				t.Errorf("expected OverrideMode error in phase %s", RequestPhaseToString(phase))
			}
			if err := rc.SkipRemainingPhases(); err == nil {
				t.Errorf("expected SkipRemainingPhases error in phase %s", RequestPhaseToString(phase))
			}
			if rc.response.modeOverride != nil {
				t.Errorf("phase %s mutated response.modeOverride on error", RequestPhaseToString(phase))
			}
		}
	})

	t.Run("out_of_range_enums_rejected", func(t *testing.T) {
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}
		rc.setPhase(REQUEST_PHASE_REQUEST_HEADERS)
		initial := ModeOverride{RequestBodyMode: BodyModeStreamed}
		if err := rc.OverrideMode(initial); err != nil {
			t.Fatal(err)
		}

		if err := rc.OverrideMode(ModeOverride{ResponseHeaderMode: HeaderSendMode(99)}); err == nil {
			t.Error("expected error for invalid HeaderSendMode")
		}
		if err := rc.OverrideMode(ModeOverride{RequestBodyMode: BodySendMode(99)}); err == nil {
			t.Error("expected error for invalid BodySendMode")
		}
		if got := rc.response.modeOverride.GetRequestBodyMode(); got != extproccfgv3.ProcessingMode_STREAMED {
			t.Errorf("existing modeOverride overwritten on error: got %v, want STREAMED", got)
		}
	})
}

func TestGetResponsePropagatesModeOverrideAndResetClearsIt(t *testing.T) {
	t.Run("header_phases_attach_and_clear", func(t *testing.T) {
		for _, phase := range []int{REQUEST_PHASE_REQUEST_HEADERS, REQUEST_PHASE_RESPONSE_HEADERS} {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			rc.setPhase(phase)
			if err := rc.OverrideMode(ModeOverride{
				ResponseHeaderMode:  HeaderModeSkip,
				RequestBodyMode:     BodyModeBuffered,
				ResponseBodyMode:    BodyModeStreamed,
				RequestTrailerMode:  HeaderModeSend,
				ResponseTrailerMode: HeaderModeSkip,
			}); err != nil {
				t.Fatal(err)
			}

			resp, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatalf("GetResponse(%d) returned error: %v", phase, err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("resp.ValidateAll() = %v, want nil", verr)
			}
			mo := resp.GetModeOverride()
			if mo == nil {
				t.Fatalf("expected ModeOverride on %s response", RequestPhaseToString(phase))
			}
			if mo.GetRequestHeaderMode() != extproccfgv3.ProcessingMode_DEFAULT ||
				mo.GetResponseHeaderMode() != extproccfgv3.ProcessingMode_SKIP ||
				mo.GetRequestBodyMode() != extproccfgv3.ProcessingMode_BUFFERED ||
				mo.GetResponseBodyMode() != extproccfgv3.ProcessingMode_STREAMED ||
				mo.GetRequestTrailerMode() != extproccfgv3.ProcessingMode_SEND ||
				mo.GetResponseTrailerMode() != extproccfgv3.ProcessingMode_SKIP {
				t.Errorf("unexpected ModeOverride fields: %+v", mo)
			}

			rc.ClearModeOverride()
			respAfterClear, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatal(err)
			}
			if respAfterClear.GetModeOverride() != nil {
				t.Errorf("expected nil ModeOverride after ClearModeOverride, got %v", respAfterClear.GetModeOverride())
			}

			if skipErr := rc.SkipRemainingPhases(); skipErr != nil {
				t.Fatal(skipErr)
			}
			if resetErr := rc.ResetPhase(); resetErr != nil {
				t.Fatal(resetErr)
			}
			respAfterReset, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatal(err)
			}
			if respAfterReset.GetModeOverride() != nil {
				t.Errorf("expected nil ModeOverride after ResetPhase, got %v", respAfterReset.GetModeOverride())
			}
		}
	})

	t.Run("immediate_response_omits_mode_override", func(t *testing.T) {
		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}
		rc.setPhase(REQUEST_PHASE_REQUEST_HEADERS)
		if err := rc.OverrideMode(ModeOverride{RequestBodyMode: BodyModeBuffered}); err != nil {
			t.Fatal(err)
		}
		if err := rc.CancelRequest(429, nil, "rate-limited"); err != nil {
			t.Fatal(err)
		}
		resp, err := rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetImmediateResponse() == nil {
			t.Fatal("expected ImmediateResponse")
		}
		if resp.GetModeOverride() != nil {
			t.Errorf("expected nil ModeOverride on ImmediateResponse, got %v", resp.GetModeOverride())
		}
	})
}

func TestProtocolConfigAndDefaultModeOverride(t *testing.T) {
	t.Run("protocol_config_from_proto", func(t *testing.T) {
		if got := protocolConfigFromProto(nil); got != nil {
			t.Fatalf("protocolConfigFromProto(nil) = %+v, want nil", got)
		}

		pc := protocolConfigFromProto(&extprocv3.ProtocolConfiguration{
			RequestBodyMode:                         extproccfgv3.ProcessingMode_STREAMED,
			ResponseBodyMode:                        extproccfgv3.ProcessingMode_BUFFERED_PARTIAL,
			SendBodyWithoutWaitingForHeaderResponse: true,
		})
		if pc == nil {
			t.Fatal("protocolConfigFromProto returned nil")
		}
		if pc.RequestBodyMode != BodyModeStreamed ||
			pc.ResponseBodyMode != BodyModeBufferedPartial ||
			!pc.SendBodyWithoutWaitingForHeaderResponse {
			t.Errorf("unexpected ProtocolConfig: %+v", pc)
		}
	})

	t.Run("default_mode_override_handles_nil_and_populated", func(t *testing.T) {
		var nilRC *RequestContext
		if got := nilRC.DefaultModeOverride(); got != (ModeOverride{}) {
			t.Errorf("nilRC.DefaultModeOverride() = %+v, want zero ModeOverride", got)
		}

		rc := newRequestContext()
		emptyMode := rc.DefaultModeOverride()
		if emptyMode != (ModeOverride{}) {
			t.Errorf("empty DefaultModeOverride() = %+v, want zero ModeOverride", emptyMode)
		}
		if _, err := emptyMode.ToEnvoyProcessingMode(); err != nil {
			t.Errorf("emptyMode.ToEnvoyProcessingMode() = %v, want nil", err)
		}

		rc.ProtocolConfig = &ProtocolConfig{
			RequestBodyMode:                         BodyModeNone,
			ResponseBodyMode:                        BodyModeBuffered,
			SendBodyWithoutWaitingForHeaderResponse: true,
		}
		mode := rc.DefaultModeOverride()
		if mode.ResponseHeaderMode != HeaderModeDefault ||
			mode.RequestBodyMode != BodyModeNone ||
			mode.ResponseBodyMode != BodyModeBuffered ||
			mode.RequestTrailerMode != HeaderModeDefault ||
			mode.ResponseTrailerMode != HeaderModeDefault {
			t.Errorf("unexpected DefaultModeOverride() = %+v", mode)
		}
		mode.RequestBodyMode = BodyModeStreamed
		pm, err := mode.ToEnvoyProcessingMode()
		if err != nil {
			t.Fatalf("ToEnvoyProcessingMode() = %v", err)
		}
		if pm.GetRequestBodyMode() != extproccfgv3.ProcessingMode_STREAMED ||
			pm.GetResponseBodyMode() != extproccfgv3.ProcessingMode_BUFFERED {
			t.Errorf("converted proto = %+v, want RequestBodyMode=STREAMED ResponseBodyMode=BUFFERED", pm)
		}
	})

	t.Run("reset_and_init_preserve_protocol_config", func(t *testing.T) {
		rc := newRequestContext()
		expected := &ProtocolConfig{
			RequestBodyMode:                         BodyModeBuffered,
			ResponseBodyMode:                        BodyModeStreamed,
			SendBodyWithoutWaitingForHeaderResponse: true,
		}
		rc.ProtocolConfig = expected

		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}
		if rc.ProtocolConfig != expected {
			t.Fatalf("ResetPhase cleared ProtocolConfig: got %+v, want %+v", rc.ProtocolConfig, expected)
		}

		if err := initReqCtx(rc, &corev3.HeaderMap{}); err != nil {
			t.Fatal(err)
		}
		if rc.ProtocolConfig != expected {
			t.Fatalf("initReqCtx cleared ProtocolConfig: got %+v, want %+v", rc.ProtocolConfig, expected)
		}
	})
}

func TestDynamicMetadataAndAttributes(t *testing.T) {
	t.Run("validation_all_or_nothing_and_merging", func(t *testing.T) {
		rc := newRequestContext()
		if err := rc.SetDynamicMetadata("", "k", "v"); err == nil {
			t.Fatal("expected error for empty namespace")
		}
		if err := rc.SetDynamicMetadata("com.example.ns", "", "v"); err == nil {
			t.Fatal("expected error for empty key")
		}
		if err := rc.SetDynamicMetadata("com.example.ns", "bad", struct{ X int }{1}); err == nil {
			t.Fatal("expected error for unsupported struct value")
		}
		if err := rc.SetDynamicMetadata("com.example.ns", "bad_utf8", string([]byte{0xff})); err == nil {
			t.Fatal("expected error for invalid UTF-8 string value")
		}
		if rc.response.dynamicMetadata != nil {
			t.Fatalf("failed calls mutated dynamicMetadata: %v", rc.response.dynamicMetadata)
		}

		if err := rc.SetDynamicMetadata("com.example.ns", "initial", "ok"); err != nil {
			t.Fatalf("SetDynamicMetadata failed: %v", err)
		}
		if err := rc.SetDynamicMetadataNamespace("com.example.ns", map[string]any{
			"":     "bad-key",
			"drop": "should-not-apply",
		}); err == nil {
			t.Fatal("expected error for empty key in SetDynamicMetadataNamespace")
		}
		fields := rc.response.dynamicMetadata.GetFields()["com.example.ns"].GetStructValue().GetFields()
		if _, exists := fields["drop"]; exists {
			t.Fatal("SetDynamicMetadataNamespace applied partial changes on error")
		}

		if err := rc.SetDynamicMetadataNamespace("com.example.ns", map[string]any{
			"count": 42,
			"tags":  []any{"a", "b"},
			"nested": map[string]any{
				"enabled": true,
			},
		}); err != nil {
			t.Fatalf("SetDynamicMetadataNamespace failed: %v", err)
		}
		if err := rc.SetDynamicMetadata("com.example.other", "flag", true); err != nil {
			t.Fatalf("second namespace SetDynamicMetadata failed: %v", err)
		}

		nsOne := rc.response.dynamicMetadata.GetFields()["com.example.ns"].GetStructValue().AsMap()
		if nsOne["initial"] != "ok" || nsOne["count"] != float64(42) {
			t.Errorf("unexpected com.example.ns contents: %+v", nsOne)
		}
		nsTwo := rc.response.dynamicMetadata.GetFields()["com.example.other"].GetStructValue().AsMap()
		if nsTwo["flag"] != true {
			t.Errorf("unexpected com.example.other contents: %+v", nsTwo)
		}
	})

	t.Run("attached_to_all_phases_and_immediate_response_and_reset_clears_outbound", func(t *testing.T) {
		phases := []int{
			REQUEST_PHASE_REQUEST_HEADERS,
			REQUEST_PHASE_REQUEST_BODY,
			REQUEST_PHASE_REQUEST_TRAILERS,
			REQUEST_PHASE_RESPONSE_HEADERS,
			REQUEST_PHASE_RESPONSE_BODY,
			REQUEST_PHASE_RESPONSE_TRAILERS,
		}
		for _, phase := range phases {
			rc := newRequestContext()
			if err := rc.ResetPhase(); err != nil {
				t.Fatal(err)
			}
			rc.setPhase(phase)
			if err := rc.SetDynamicMetadata("com.example.phase", "name", RequestPhaseToString(phase)); err != nil {
				t.Fatal(err)
			}
			resp, err := rc.GetResponse(phase)
			if err != nil {
				t.Fatalf("GetResponse(%s) failed: %v", RequestPhaseToString(phase), err)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("GetResponse(%s) ValidateAll() = %v", RequestPhaseToString(phase), verr)
			}
			got := resp.GetDynamicMetadata().GetFields()["com.example.phase"].GetStructValue().AsMap()
			if got["name"] != RequestPhaseToString(phase) {
				t.Errorf("phase %s dynamic_metadata = %+v", RequestPhaseToString(phase), got)
			}

			rc.ClearDynamicMetadata()
			respCleared, clearErr := rc.GetResponse(phase)
			if clearErr != nil {
				t.Fatal(clearErr)
			}
			if respCleared.GetDynamicMetadata() != nil {
				t.Errorf("expected nil DynamicMetadata after ClearDynamicMetadata on %s", RequestPhaseToString(phase))
			}
		}

		rc := newRequestContext()
		if err := rc.ResetPhase(); err != nil {
			t.Fatal(err)
		}
		rc.setPhase(REQUEST_PHASE_REQUEST_HEADERS)
		if err := rc.SetDynamicMetadata("com.example.auth", "denied", true); err != nil {
			t.Fatal(err)
		}
		if err := rc.CancelRequest(403, nil, "forbidden"); err != nil {
			t.Fatal(err)
		}
		immResp, err := rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
		if err != nil {
			t.Fatal(err)
		}
		if verr := immResp.ValidateAll(); verr != nil {
			t.Errorf("ImmediateResponse ValidateAll() = %v", verr)
		}
		if got := immResp.GetDynamicMetadata().GetFields()["com.example.auth"].GetStructValue().AsMap(); got["denied"] != true {
			t.Errorf("ImmediateResponse dynamic_metadata = %+v, want denied=true", got)
		}
		if resetErr := rc.ResetPhase(); resetErr != nil {
			t.Fatal(resetErr)
		}
		if rc.response.dynamicMetadata != nil {
			t.Errorf("ResetPhase failed to clear outbound dynamicMetadata")
		}
	})

	t.Run("inbound_merge_and_persistence_across_reset_and_init", func(t *testing.T) {
		rc := newRequestContext()
		st1, err := structpb.NewStruct(map[string]any{"sub": "alice", "role": "reader"})
		if err != nil {
			t.Fatal(err)
		}
		typedAny, err := anypb.New(wrapperspb.String("typed-v1"))
		if err != nil {
			t.Fatal(err)
		}
		rc.mergeMetadataContext(&corev3.Metadata{
			FilterMetadata:      map[string]*structpb.Struct{"com.example.auth": st1},
			TypedFilterMetadata: map[string]*anypb.Any{"com.example.typed": typedAny},
		})

		attr1, err := structpb.NewStruct(map[string]any{"request.protocol": "HTTP/2", "xds.route_name": "r1"})
		if err != nil {
			t.Fatal(err)
		}
		rc.mergeAttributes(map[string]*structpb.Struct{"envoy.filters.http.ext_proc": attr1})

		if resetErr := rc.ResetPhase(); resetErr != nil {
			t.Fatal(resetErr)
		}
		if initErr := initReqCtx(rc, &corev3.HeaderMap{}); initErr != nil {
			t.Fatal(initErr)
		}

		st2, err := structpb.NewStruct(map[string]any{"role": "admin"})
		if err != nil {
			t.Fatal(err)
		}
		rc.mergeMetadataContext(&corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{"com.example.auth": st2},
		})
		attr2, err := structpb.NewStruct(map[string]any{"response.code": 200})
		if err != nil {
			t.Fatal(err)
		}
		rc.mergeAttributes(map[string]*structpb.Struct{"envoy.filters.http.ext_proc": attr2})

		if rc.Metadata["com.example.auth"]["sub"] != "alice" || rc.Metadata["com.example.auth"]["role"] != "admin" {
			t.Errorf("merged Metadata = %+v", rc.Metadata)
		}
		var unpacked wrapperspb.StringValue
		if uerr := rc.TypedMetadata["com.example.typed"].UnmarshalTo(&unpacked); uerr != nil || unpacked.GetValue() != "typed-v1" {
			t.Errorf("TypedMetadata unpack = (%q, %v), want typed-v1", unpacked.GetValue(), uerr)
		}
		if rc.Attributes["request.protocol"] != "HTTP/2" || rc.Attributes["xds.route_name"] != "r1" || rc.Attributes["response.code"] != float64(200) {
			t.Errorf("merged Attributes = %+v", rc.Attributes)
		}
	})
}
