package extproc

import (
	"fmt"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

// AllHeaders holds the headers (or trailers) of a single processing phase.
//
// Envoy's ext_proc filter sends header values in HeaderValue.raw_value rather
// than HeaderValue.value, so both maps here are populated for every header
// regardless of which proto field was set. A lookup never has to guess which
// map a header landed in.
//
// Values are stored one entry per occurrence on the wire: a header sent twice
// (Set-Cookie, for instance) yields two entries rather than the second
// overwriting the first. Values are never split on commas; use SplitList for
// headers that are genuinely comma delimited lists.
//
// Keys are lower cased, matching what Envoy sends. The accessors are case
// insensitive; prefer them to indexing the maps directly.
type AllHeaders struct {
	Headers    map[string][]string
	RawHeaders map[string][][]byte
}

func newAllHeaders() AllHeaders {
	return AllHeaders{
		Headers:    map[string][]string{},
		RawHeaders: map[string][][]byte{},
	}
}

func (h AllHeaders) add(name string, raw []byte) {
	name = strings.ToLower(name)
	h.Headers[name] = append(h.Headers[name], string(raw))
	h.RawHeaders[name] = append(h.RawHeaders[name], raw)
}

// Values returns every value sent for name, in wire order, or nil.
func (h AllHeaders) Values(name string) []string {
	return h.Headers[strings.ToLower(name)]
}

// Get returns the first value sent for name.
func (h AllHeaders) Get(name string) (string, bool) {
	vs := h.Headers[strings.ToLower(name)]
	if len(vs) == 0 {
		return "", false
	}
	return vs[0], true
}

// RawValues returns every value sent for name as raw bytes, in wire order, or
// nil. Use this for headers whose values are not valid UTF-8.
func (h AllHeaders) RawValues(name string) [][]byte {
	return h.RawHeaders[strings.ToLower(name)]
}

// Has reports whether name was sent at all, including with an empty value.
func (h AllHeaders) Has(name string) bool {
	_, ok := h.Headers[strings.ToLower(name)]
	return ok
}

// SplitList returns the comma separated elements of every value sent for name,
// with surrounding whitespace trimmed and empty elements dropped.
//
// This is only correct for headers defined as a comma delimited list (#list in
// RFC 9110): Accept, Accept-Encoding, Cache-Control, Vary and similar. Do NOT
// use it on Date, User-Agent, Cookie, or any header whose value may contain a
// comma that is not a separator.
func (h AllHeaders) SplitList(name string) []string {
	var out []string
	for _, v := range h.Values(name) {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func genHeaders(headerMap *corev3.HeaderMap) (AllHeaders, error) {
	headers := newAllHeaders()

	for _, h := range headerMap.GetHeaders() {
		if len(h.GetValue()) > 0 && len(h.GetRawValue()) > 0 {
			return headers, fmt.Errorf("header %q sets both 'value' and 'raw_value'", h.GetKey())
		}

		// Envoy populates raw_value; fall back to value for any producer that
		// does not, so callers see one consistent representation either way.
		raw := h.GetRawValue()
		if len(raw) == 0 && len(h.GetValue()) > 0 {
			raw = []byte(h.GetValue())
		}

		headers.add(h.GetKey(), raw)
	}

	return headers, nil
}
