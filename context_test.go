package extproc

import "testing"

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
