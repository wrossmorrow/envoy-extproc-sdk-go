package extproc

import (
	"slices"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

func TestAllHeadersParsesHeaderMap(t *testing.T) {
	rc := newRequestContext()
	hdrMap := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: ":method", RawValue: []byte("POST")},
			{Key: ":scheme", RawValue: []byte("https")},
			{Key: ":authority", RawValue: []byte("api.example.com")},
			{Key: ":path", RawValue: []byte("/foo?bar=baz")},
			{Key: "x-request-id", RawValue: []byte("req-123")},
			{Key: "x-multi", RawValue: []byte("first")},
			{Key: "x-multi", RawValue: []byte("second")},
			{Key: "x-list", RawValue: []byte("a, b ,c")},
			{Key: "x-empty", RawValue: []byte("")},
		},
	}
	if err := initReqCtx(rc, hdrMap); err != nil {
		t.Fatal(err)
	}

	if rc.Method != "POST" {
		t.Errorf("rc.Method = %q, want %q", rc.Method, "POST")
	}
	if rc.Scheme != "https" {
		t.Errorf("rc.Scheme = %q, want %q", rc.Scheme, "https")
	}
	if rc.Authority != "api.example.com" {
		t.Errorf("rc.Authority = %q, want %q", rc.Authority, "api.example.com")
	}
	if rc.FullPath != "/foo?bar=baz" {
		t.Errorf("rc.FullPath = %q, want %q", rc.FullPath, "/foo?bar=baz")
	}
	if rc.Path != "/foo" {
		t.Errorf("rc.Path = %q, want %q", rc.Path, "/foo")
	}
	if rc.RequestID != "req-123" {
		t.Errorf("rc.RequestID = %q, want %q", rc.RequestID, "req-123")
	}

	// both maps must be populated, one entry per occurrence, in wire order
	if got := rc.AllHeaders.Values("X-Multi"); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("Values(X-Multi) = %v, want [first second]", got)
	}
	if got := rc.AllHeaders.Values("x-multi"); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("Values(x-multi) = %v, want [first second]", got)
	}
	if v, ok := rc.AllHeaders.Get("x-multi"); !ok || v != "first" {
		t.Errorf("Get(x-multi) = %q, %v; want %q, true", v, ok, "first")
	}
	if raw := rc.AllHeaders.RawValues("x-multi"); len(raw) != 2 || string(raw[0]) != "first" || string(raw[1]) != "second" {
		t.Errorf("RawValues(x-multi) = %v, want two entries", raw)
	}
	if !rc.AllHeaders.Has("X-Empty") {
		t.Error("Has should be true for an empty value")
	}
	if rc.AllHeaders.Has("missing") {
		t.Error("Has should be false for an absent header")
	}

	// splitting is opt-in and only splits comma delimited values
	if got := rc.AllHeaders.SplitList("x-list"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("SplitList(x-list) = %v, want [a b c]", got)
	}
	if got := rc.AllHeaders.Values("x-list"); !slices.Equal(got, []string{"a, b ,c"}) {
		t.Errorf("Values(x-list) = %v, want the raw value unsplit", got)
	}

	// keys are lower cased on ingest
	if _, ok := rc.AllHeaders.Headers["x-multi"]; !ok {
		t.Error("expected lower cased key in Headers map")
	}
}

func TestInitReqCtxRejectsHeaderWithBothValueAndRawValue(t *testing.T) {
	rc := newRequestContext()
	hdrMap := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: "x-bad", Value: "a", RawValue: []byte("b")},
		},
	}
	err := initReqCtx(rc, hdrMap)
	if err == nil {
		t.Fatal("expected an error for a header setting both value and raw_value")
	}
	if rc.AllHeaders.Headers == nil {
		t.Error("expected AllHeaders to be initialized even when parsing fails")
	}
}

func TestInitReqCtxFallsBackToValue(t *testing.T) {
	rc := newRequestContext()
	hdrMap := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: "x-ok", Value: "hello"},
		},
	}
	err := initReqCtx(rc, hdrMap)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := rc.AllHeaders.Get("x-ok"); !ok || v != "hello" {
		t.Errorf("Get(x-ok) = %q, %v; want %q, true", v, ok, "hello")
	}
}

func TestHeaderValueHelpers(t *testing.T) {
	// a HeaderValue may set either Value or RawValue, but not both
	hv := HeaderValue{Value: "v"}
	if !hv.IsValid() {
		t.Errorf("IsValid() = %v, want true for a value only HeaderValue", hv.IsValid())
	}
	hv = HeaderValue{RawValue: []byte("v")}
	if !hv.IsValid() {
		t.Errorf("IsValid() = %v, want true for a raw value HeaderValue", hv.IsValid())
	}
	hv = HeaderValue{Value: "v", RawValue: []byte("v")}
	if hv.IsValid() {
		t.Error("IsValid() = true, want false when both value and raw_value are set")
	}

	// Value is converted to raw bytes for the envoy header value
	envoy := HeaderValue{Value: "v"}.ToEnvoyHeaderValue("x-a")
	if envoy.GetKey() != "x-a" {
		t.Errorf("envoy header key = %q, want %q", envoy.GetKey(), "x-a")
	}
	if string(envoy.GetRawValue()) != "v" {
		t.Errorf("envoy header raw value = %q, want %q", string(envoy.GetRawValue()), "v")
	}

	envoy = HeaderValue{RawValue: []byte("raw")}.ToEnvoyHeaderValue("x-a")
	if envoy.GetKey() != "x-a" {
		t.Errorf("envoy header key = %q, want %q", envoy.GetKey(), "x-a")
	}
	if string(envoy.GetRawValue()) != "raw" {
		t.Errorf("envoy header raw value = %q, want %q", string(envoy.GetRawValue()), "raw")
	}

	// options are validated, and accept any action the proto defines
	ho, err := HeaderValue{RawValue: []byte("1")}.ToEnvoyHeaderValueOption("x-a", "ADD_IF_ABSENT")
	if err != nil {
		t.Fatalf("expected no error for a valid action, got %v", err)
	}
	if ho.GetHeader().GetKey() != "x-a" {
		t.Errorf("option header key = %q, want %q", ho.GetHeader().GetKey(), "x-a")
	}
	if string(ho.GetHeader().GetRawValue()) != "1" {
		t.Errorf("option header raw value = %q, want %q", string(ho.GetHeader().GetRawValue()), "1")
	}
	if ho.GetAppendAction() != corev3.HeaderValueOption_ADD_IF_ABSENT {
		t.Errorf("option append action = %v, want %v", ho.GetAppendAction(), corev3.HeaderValueOption_ADD_IF_ABSENT)
	}

	_, err = HeaderValue{RawValue: []byte("1")}.ToEnvoyHeaderValueOption("x-a", "NOT_A_REAL_ACTION")
	if err == nil {
		t.Error("expected an error for an unknown append action")
	}
	_, err = HeaderValue{Value: "a", RawValue: []byte("b")}.ToEnvoyHeaderValueOption("x-a", "ADD_IF_ABSENT")
	if err == nil {
		t.Error("expected an error for an invalid header value")
	}
}

func TestBuildHeaderValuesFromMap(t *testing.T) {
	m := BuildHeaderValuesFromMap(map[string]string{"x-a": "1", "x-b": "hello world"})
	if len(m) != 2 {
		t.Fatalf("BuildHeaderValuesFromMap returned %d entries, want 2", len(m))
	}
	if hv, ok := m["x-a"]; !ok || hv.RawValue == nil || string(hv.RawValue) != "1" {
		t.Errorf("BuildHeaderValuesFromMap[\"x-a\"] = %+v, want raw value %q", hv, "1")
	}
	if hv, ok := m["x-b"]; !ok || hv.RawValue == nil || string(hv.RawValue) != "hello world" {
		t.Errorf("BuildHeaderValuesFromMap[\"x-b\"] = %+v, want raw value %q", hv, "hello world")
	}

	if m := BuildHeaderValuesFromMap(map[string]string{}); len(m) != 0 {
		t.Errorf("BuildHeaderValuesFromMap(empty) = %v, want empty", m)
	}
}

func TestHeaderMutationConvenienceActions(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	if err := rc.AddHeader("x-a", HeaderValue{RawValue: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	if err := rc.AppendHeader("x-a", HeaderValue{RawValue: []byte("2")}); err != nil {
		t.Fatal(err)
	}
	if err := rc.OverwriteHeader("x-a", HeaderValue{RawValue: []byte("3")}); err != nil {
		t.Fatal(err)
	}

	hs := rc.response.headerMutation.SetHeaders
	if len(hs) != 3 {
		t.Fatalf("SetHeaders = %d entries, want 3 (one per mutation)", len(hs))
	}
	if hs[0].GetAppendAction() != corev3.HeaderValueOption_ADD_IF_ABSENT {
		t.Errorf("AddHeader action = %v, want %v", hs[0].GetAppendAction(), corev3.HeaderValueOption_ADD_IF_ABSENT)
	}
	if hs[1].GetAppendAction() != corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD {
		t.Errorf("AppendHeader action = %v, want %v", hs[1].GetAppendAction(), corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD)
	}
	if hs[2].GetAppendAction() != corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD {
		t.Errorf("OverwriteHeader action = %v, want %v", hs[2].GetAppendAction(), corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD)
	}
	if string(hs[0].GetHeader().GetRawValue()) != "1" || string(hs[1].GetHeader().GetRawValue()) != "2" || string(hs[2].GetHeader().GetRawValue()) != "3" {
		t.Errorf("mutation values not propagated: %v", hs)
	}
}

func TestRemoveHeaders(t *testing.T) {
	rc := newRequestContext()
	if err := rc.ResetPhase(); err != nil {
		t.Fatal(err)
	}

	rc.OverwriteHeader("x-a", HeaderValue{RawValue: []byte("1")})
	rc.OverwriteHeader("x-b", HeaderValue{RawValue: []byte("2")})

	// variadic removal takes multiple names at once
	rc.RemoveHeadersVariadic("x-a", "x-b")
	if !slices.Contains(rc.response.headerMutation.RemoveHeaders, "x-a") {
		t.Errorf("RemoveHeadersVariadic did not remove %q: %v", "x-a", rc.response.headerMutation.RemoveHeaders)
	}
	if !slices.Contains(rc.response.headerMutation.RemoveHeaders, "x-b") {
		t.Errorf("RemoveHeadersVariadic did not remove %q: %v", "x-b", rc.response.headerMutation.RemoveHeaders)
	}
	if len(rc.response.headerMutation.RemoveHeaders) != 2 {
		t.Errorf("RemoveHeadersVariadic removed %d headers, want 2 (no duplicates)", len(rc.response.headerMutation.RemoveHeaders))
	}

	// re-removing an already-removed header must not duplicate the entry
	rc.RemoveHeaders([]string{"x-a"})
	if len(rc.response.headerMutation.RemoveHeaders) != 2 {
		t.Errorf("RemoveHeaders duplicated an entry: %v", rc.response.headerMutation.RemoveHeaders)
	}

	// removing a header that was never set is not an error; the name is simply
	// queued for removal (envoy ignores removes for headers it never saw)
	if err := rc.RemoveHeader("x-never-set"); err != nil {
		t.Fatalf("RemoveHeader on an absent header returned error: %v", err)
	}
	if !slices.Contains(rc.response.headerMutation.RemoveHeaders, "x-never-set") {
		t.Errorf("RemoveHeader on an absent header did not queue the removal: %v", rc.response.headerMutation.RemoveHeaders)
	}
}
