package extproc

import (
	"reflect"
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

func TestRequestContextSetValueWithoutHeaders(t *testing.T) {
	rc := newRequestContext() // as when request_header_mode: SKIP
	if err := rc.SetValue("k", "v"); err != nil {
		t.Fatal(err)
	}
}

func TestRequestContextGetValue(t *testing.T) {
	rc := newRequestContext()
	if err := rc.SetValue("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := rc.GetValue("k"); err != nil || v != "v" {
		t.Errorf("GetValue(%q) = %v, %v; want %q, nil", "k", v, err, "v")
	}
	if _, err := rc.GetValue("missing"); err == nil {
		t.Error("expected an error for a value that was never set")
	}
}

func TestRequestContextSetValueOverwrites(t *testing.T) {
	rc := newRequestContext()
	if err := rc.SetValue("k", "first"); err != nil {
		t.Fatal(err)
	}
	if err := rc.SetValue("k", "second"); err != nil {
		t.Fatal(err)
	}
	if v, err := rc.GetValue("k"); err != nil || v != "second" {
		t.Errorf("GetValue(%q) = %v, %v; want %q, nil", "k", v, err, "second")
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

func TestMetricsCollectorsCoverEveryField(t *testing.T) {
	m := NewEmptyMetrics()

	// Collectors() reflects over the struct; a field added to Metrics but left
	// nil, or one that stops being a Collector, silently drops off /metrics
	want := reflect.ValueOf(m).Elem().NumField()
	if got := len(m.Collectors()); got != want {
		t.Errorf("Collectors() = %d, want %d (one per Metrics field)", got, want)
	}

	registry := prometheus.NewRegistry()
	m.Register(registry)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 0 {
		// counters with no observations are not gathered; this just proves
		// Register did not panic on a duplicate or nil collector
		t.Logf("gathered %d metric families", len(families))
	}
}

func TestToEnvoyHeaderValueOptionAppendFlag(t *testing.T) {
	cases := []struct {
		action     string
		wantEnum   corev3.HeaderValueOption_HeaderAppendAction
		wantAppend bool
	}{
		{
			action:     "APPEND_IF_EXISTS_OR_ADD",
			wantEnum:   corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
			wantAppend: true,
		},
		{
			action:     "ADD_IF_ABSENT",
			wantEnum:   corev3.HeaderValueOption_ADD_IF_ABSENT,
			wantAppend: false,
		},
		{
			action:     "OVERWRITE_IF_EXISTS_OR_ADD",
			wantEnum:   corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			wantAppend: false,
		},
		{
			action:     "OVERWRITE_IF_EXISTS",
			wantEnum:   corev3.HeaderValueOption_OVERWRITE_IF_EXISTS,
			wantAppend: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			ho, err := (HeaderValue{RawValue: []byte("v")}).ToEnvoyHeaderValueOption("x-h", tc.action)
			if err != nil {
				t.Fatalf("ToEnvoyHeaderValueOption returned error: %v", err)
			}
			if verr := ho.ValidateAll(); verr != nil {
				t.Errorf("ho.ValidateAll() = %v, want nil", verr)
			}
			if ho.GetAppendAction() != tc.wantEnum {
				t.Errorf("AppendAction = %v, want %v", ho.GetAppendAction(), tc.wantEnum)
			}
			if tc.wantAppend {
				if ho.GetAppend() == nil || !ho.GetAppend().GetValue() {
					t.Errorf("Append = %v, want BoolValue(true)", ho.GetAppend())
				}
			} else if ho.GetAppend() != nil {
				t.Errorf("Append = %v, want nil", ho.GetAppend())
			}
		})
	}
}

func TestResolveHeaderMutationEnforcesAddIfAbsentAndOverwriteIfExists(t *testing.T) {
	rc := newRequestContext()
	in := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: ":authority", RawValue: []byte("example.com")},
			{Key: "x-existing", RawValue: []byte("keep")},
			{Key: "x-remove-then-add", RawValue: []byte("old")},
		},
	}
	if err := initReqCtx(rc, in); err != nil {
		t.Fatalf("initReqCtx failed: %v", err)
	}
	rc.setPhase(REQUEST_PHASE_REQUEST_HEADERS)

	_ = rc.AddHeader("x-existing", HeaderValue{RawValue: []byte("ignored")})
	_ = rc.AddHeader("x-absent", HeaderValue{RawValue: []byte("first")})
	_ = rc.AddHeader("x-absent", HeaderValue{RawValue: []byte("second")})
	_ = rc.UpdateHeader("x-missing", HeaderValue{RawValue: []byte("nope")}, "OVERWRITE_IF_EXISTS")
	_ = rc.UpdateHeader("x-existing", HeaderValue{RawValue: []byte("replaced")}, "OVERWRITE_IF_EXISTS")
	_ = rc.RemoveHeader("x-remove-then-add")
	_ = rc.AddHeader("x-remove-then-add", HeaderValue{RawValue: []byte("fresh")})
	_ = rc.RemoveHeader(":authority")
	_ = rc.AddHeader(":authority", HeaderValue{RawValue: []byte("spoof")})

	resp1, err := rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
	if err != nil {
		t.Fatalf("GetResponse(1) failed: %v", err)
	}
	resp2, err := rc.GetResponse(REQUEST_PHASE_REQUEST_HEADERS)
	if err != nil {
		t.Fatalf("GetResponse(2) failed: %v", err)
	}

	extract := func(r []*corev3.HeaderValueOption) []string {
		out := make([]string, 0, len(r))
		for _, h := range r {
			out = append(out, h.GetHeader().GetKey()+"="+string(h.GetHeader().GetRawValue())+":"+h.GetAppendAction().String())
		}
		return out
	}

	got1 := extract(resp1.GetRequestHeaders().GetResponse().GetHeaderMutation().GetSetHeaders())
	got2 := extract(resp2.GetRequestHeaders().GetResponse().GetHeaderMutation().GetSetHeaders())
	want := []string{
		"x-absent=first:ADD_IF_ABSENT",
		"x-existing=replaced:OVERWRITE_IF_EXISTS",
		"x-remove-then-add=fresh:ADD_IF_ABSENT",
	}
	if !slices.Equal(got1, want) {
		t.Errorf("SetHeaders(1) = %v, want %v", got1, want)
	}
	if !slices.Equal(got2, want) {
		t.Errorf("SetHeaders(2) = %v, want %v (idempotent)", got2, want)
	}
}
