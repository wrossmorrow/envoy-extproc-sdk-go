package extproc

import (
	"slices"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

func TestRequestContextSetValueWithoutHeaders(t *testing.T) {
	rc := newRequestContext() // as when request_header_mode: SKIP
	if err := rc.SetValue("k", "v"); err != nil {
		t.Fatal(err)
	}
}

func TestCancelRequestRejectsInvalidHeaderValue(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	// only one of Value / RawValue may be set
	bad := map[string]HeaderValue{
		"x-bad": {Value: "a", RawValue: []byte("b")},
	}

	if err := rc.CancelRequest(403, bad, "nope"); err == nil {
		t.Fatal("expected an error for a HeaderValue with both fields set")
	}

	if rc.response.immediateResponse != nil {
		t.Error("immediate response was built despite the invalid header")
	}
}

func contentLengthMutations(rc *RequestContext) []string {
	var out []string
	for _, h := range rc.response.headerMutation.SetHeaders {
		if h.Header.Key == kContentLength {
			out = append(out, string(h.Header.RawValue))
		}
	}
	return out
}

func TestReplaceBodyChunkLeavesContentLength(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	// a chunk is not the message: its length is not Content-Length
	if err := rc.ReplaceBodyChunk([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if got := contentLengthMutations(rc); len(got) != 0 {
		t.Errorf("ReplaceBodyChunk set content-length to %v", got)
	}
	if rc.response.bodyMutation == nil {
		t.Error("ReplaceBodyChunk did not set a body mutation")
	}
}

func TestReplaceBodySetsContentLength(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if err := rc.ReplaceBody([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := contentLengthMutations(rc); len(got) != 1 || got[0] != "5" {
		t.Errorf("content-length mutations = %v, want [5]", got)
	}
}

func TestClearBodySetsContentLengthZero(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if err := rc.ClearBody(); err != nil {
		t.Fatal(err)
	}
	if got := contentLengthMutations(rc); len(got) != 1 || got[0] != "0" {
		t.Errorf("content-length mutations = %v, want [0]", got)
	}
}

func setHeaderNames(rc *RequestContext) []string {
	var out []string
	for _, h := range rc.response.headerMutation.SetHeaders {
		out = append(out, h.Header.Key)
	}
	return out
}

func TestUpdateHeaderRejectsUnknownAction(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	// an unknown name used to map to 0, silently becoming APPEND_IF_EXISTS_OR_ADD (note misspelled here)
	err := rc.UpdateHeader("x-a", HeaderValue{RawValue: []byte("1")}, "APPEND_IF_EXITS_OR_ADD")
	if err == nil {
		t.Fatal("expected an error for a misspelled append action")
	}
	if got := setHeaderNames(rc); len(got) != 0 {
		t.Errorf("header was set despite the bad action: %v", got)
	}
}

func TestUpdateHeaderAppliesNamedAction(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if err := rc.AddHeader("x-a", HeaderValue{RawValue: []byte("1")}); err != nil {
		t.Fatal(err)
	}

	hs := rc.response.headerMutation.SetHeaders
	if len(hs) != 1 {
		t.Fatalf("SetHeaders = %d entries, want 1", len(hs))
	}
	if got := hs[0].AppendAction; got != corev3.HeaderValueOption_ADD_IF_ABSENT {
		t.Errorf("AppendAction = %v, want ADD_IF_ABSENT", got)
	}

	if err := rc.UpdateHeader("x-b", HeaderValue{RawValue: []byte("1")}, "OVERWRITE_IF_EXISTS"); err != nil {
		t.Fatal(err)
	}

	// OVERWRITE_IF_EXISTS has no convenience wrapper as of writing; this pins that
	// UpdateHeader accepts any action name the proto defines, not just the three we wrap
	hs = rc.response.headerMutation.SetHeaders
	if len(hs) != 2 {
		t.Fatalf("SetHeaders = %d entries, want 2", len(hs))
	}
	if got := hs[1].AppendAction; got != corev3.HeaderValueOption_OVERWRITE_IF_EXISTS {
		t.Errorf("AppendAction = %v, want OVERWRITE_IF_EXISTS", got)
	}
}

func TestUpdateHeadersIsAllOrNothing(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	// map iteration order is randomized, so an implementation that appends as it
	// goes leaves a different partial mutation behind on different runs
	headers := map[string]HeaderValue{
		"x-a": {RawValue: []byte("1")},
		"x-b": {Value: "2", RawValue: []byte("2")}, // invalid: both fields set
		"x-c": {RawValue: []byte("3")},
	}

	if err := rc.AddHeaders(headers); err == nil {
		t.Fatal("expected an error for the invalid header value")
	}
	if got := setHeaderNames(rc); len(got) != 0 {
		t.Errorf("partial mutation left behind: %v", got)
	}
}

func TestUpdateHeadersAppliesAllOnSuccess(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	headers := map[string]HeaderValue{
		"x-a": {RawValue: []byte("1")},
		"x-b": {RawValue: []byte("2")},
	}

	if err := rc.OverwriteHeaders(headers); err != nil {
		t.Fatal(err)
	}

	got := setHeaderNames(rc)
	slices.Sort(got)
	if !slices.Equal(got, []string{"x-a", "x-b"}) {
		t.Errorf("SetHeaders = %v, want [x-a x-b]", got)
	}
}
