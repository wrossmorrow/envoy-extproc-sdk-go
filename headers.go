package extproc

import (
	b64 "encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

type HeaderValue struct {
	Value    string
	RawValue []byte
}

// Internal structure for storing headers received from envoy, as either
// multi-string-valued lists or raw bytes.
type AllHeaders struct {
	Headers    map[string][]string
	RawHeaders map[string][]byte
}

// The type required of a method to filter headers in-place
type HeaderNameFilter func(string) bool

// Create an `AllHeaders` struct from envoy-formatted headers.
func NewAllHeadersFromEnvoyHeaderMap(headerMap *corev3.HeaderMap) (headers AllHeaders, err error) {
	headers = AllHeaders{map[string][]string{}, map[string][]byte{}}

	for _, h := range headerMap.Headers {
		if len(h.Value) > 0 && len(h.RawValue) > 0 {
			err = fmt.Errorf("only one of 'value' or 'raw_value' can be set")
			return
		}

		if len(h.Value) > 0 {
			headers.Headers[h.Key] = strings.Split(h.Value, ",")
		} else {
			headers.RawHeaders[h.Key] = h.RawValue
		}
	}
	return
}

// "Stringify" all headers, meaning convert the headers to a simplified
// map[string]string joining (in CSV-style) multi-string-valued headers
// if they exist
func (h *AllHeaders) Stringify() map[string]string {
	headers := make(map[string]string)
	for name, val := range h.Headers {
		headers[name] = strings.Join(val, ", ")
	}
	for name, val := range h.RawHeaders {
		if utf8.Valid(val) {
			headers[name] = string(val)
		} else {
			headers[name] = b64.StdEncoding.EncodeToString(val)
		}
	}
	return headers
}

// Get header values by name as either list of strings or raw bytes
func (h *AllHeaders) GetHeaderValue(name string) ([]string, []byte, bool) {
	if value, exists := h.Headers[name]; exists {
		return value, nil, true
	}
	if value, exists := h.RawHeaders[name]; exists {
		return nil, value, true
	}
	return nil, nil, false
}

// Get header values by name, if it exists, as a single string joining multivalues
// if they exist for the name
func (h *AllHeaders) GetHeaderValueAsString(name string) (string, error) {
	sv, bv, exists := h.GetHeaderValue(name)
	if !exists {
		return "", errors.New("header does not exist")
	}
	if sv != nil {
		s := strings.Join(sv, ", ")
		return s, nil
	}
	if bv != nil {
		if utf8.Valid(bv) {
			return string(bv), nil
		}
		// Note, we return the bytes base64 encoded, not an empty string
		return b64.StdEncoding.EncodeToString(bv), errors.New("bytes-valued header is not valid utf8")
	}
	return "", errors.New("unexpected state encountered retrieving header value")
}

// Get header values by name, if it exists, as a list of string
func (h *AllHeaders) GetHeaderValueAsStringList(name string) ([]string, error) {
	sv, bv, exists := h.GetHeaderValue(name)
	if !exists {
		return nil, errors.New("header does not exist")
	}
	if sv != nil {
		return sv, nil
	}
	if bv != nil {
		if utf8.Valid(bv) {
			csv := string(bv)
			values := strings.Split(csv, ",")
			for v := range values {
				values[v] = strings.TrimSpace(values[v])
			}
			return values, nil
		}
		// Note, we return the bytes base64 encoded, not an empty string
		return nil, errors.New("bytes-valued header is not valid utf8")
	}
	return nil, errors.New("unexpected state encountered retrieving header value")
}

// Drop, in-place, the header with a given name if it exists
func (h *AllHeaders) DropHeaderNamed(name string) bool {
	if _, exists := h.Headers[name]; exists {
		delete(h.Headers, name)
		return true
	}
	if _, exists := h.RawHeaders[name]; exists {
		delete(h.RawHeaders, name)
		return true
	}
	return false
}

// Drop, in-place, the headers with given names if they exists
func (h *AllHeaders) DropHeadersNamed(names []string) {
	for _, name := range names {
		if _, exists := h.Headers[name]; exists {
			delete(h.Headers, name)
		}
		if _, exists := h.RawHeaders[name]; exists {
			delete(h.RawHeaders, name)
		}
	}
}

// Filter headers, meaning drop them in place, using a generic filter
// strategy specified by a `HeaderNameFilter` method. That is, iterate
// over all headers and remove them if the method evaluates `true`.
func (h *AllHeaders) FilterHeaders(exclude HeaderNameFilter) {
	// values are reached in (any) iterative order chosen, so in-loop removal ok?
	for name := range h.Headers {
		if exclude(name) {
			delete(h.Headers, name)
		}
	}
	for name := range h.RawHeaders {
		if exclude(name) {
			delete(h.RawHeaders, name)
		}
	}
}

// Convenience method for dropping headers with names matching a prefix.
func (h *AllHeaders) DropHeadersNamedStartingWith(prefix string) {
	h.FilterHeaders(func(name string) bool {
		return strings.HasPrefix(name, prefix)
	})
}

// Convenience method for dropping headers with names matching a suffix.
func (h *AllHeaders) DropHeadersNamedEndingWith(suffix string) {
	h.FilterHeaders(func(name string) bool {
		return strings.HasSuffix(name, suffix)
	})
}

// Clone a set of headers, convenience for copying in case in-place
// methods above are too destructive for use in a given implementation.
func (h *AllHeaders) Clone() *AllHeaders {
	copy := AllHeaders{map[string][]string{}, map[string][]byte{}}
	for name, val := range h.Headers {
		copy.Headers[name] = val
	}
	for name, val := range h.RawHeaders {
		copy.RawHeaders[name] = val
	}
	return &copy
}
