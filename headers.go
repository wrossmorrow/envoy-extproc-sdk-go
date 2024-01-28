package extproc

import (
	"fmt"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

type AllHeaders struct {
	Headers     map[string][]string
	ByteHeaders map[string][]byte
}

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
			headers.ByteHeaders[h.Key] = h.RawValue
		}
	}
	return
}
