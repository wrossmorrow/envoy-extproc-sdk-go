package extproc

import (
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
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
