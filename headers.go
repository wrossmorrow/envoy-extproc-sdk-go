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

type AllHeaders struct {
	Headers    map[string][]string
	RawHeaders map[string][]byte
}

type HeaderNameFilter func(string) bool

func genHeaders(headerMap *corev3.HeaderMap) (headers AllHeaders, err error) {
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

func NewAllHeadersFromEnvoyHeaderMap(headerMap *corev3.HeaderMap) (headers AllHeaders, err error) {
	return genHeaders(headerMap)
}

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

func (h *AllHeaders) GetHeaderValue(name string) ([]string, []byte, bool) {
	if value, exists := h.Headers[name]; exists {
		return value, nil, true
	}
	if value, exists := h.RawHeaders[name]; exists {
		return nil, value, true
	}
	return nil, nil, false
}

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

func (h *AllHeaders) DropHeadersNamedStartingWith(prefix string) {
	h.FilterHeaders(func(name string) bool {
		return strings.HasPrefix(name, prefix)
	})
}

func (h *AllHeaders) DropHeadersNamedEndingWith(suffix string) {
	h.FilterHeaders(func(name string) bool {
		return strings.HasSuffix(name, suffix)
	})
}

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
