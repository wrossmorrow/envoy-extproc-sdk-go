package extproc

import "testing"

func TestRequestContextSetValueWithoutHeaders(t *testing.T) {
	rc := newRequestContext() // as when request_header_mode: SKIP
	if err := rc.SetValue("k", "v"); err != nil {
		t.Fatal(err)
	}
}
