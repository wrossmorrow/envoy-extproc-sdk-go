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
