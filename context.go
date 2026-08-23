package extproc

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

const (
	REQUEST_PHASE_UNDETERMINED      = 0
	REQUEST_PHASE_REQUEST_HEADERS   = 1
	REQUEST_PHASE_REQUEST_BODY      = 2
	REQUEST_PHASE_REQUEST_TRAILERS  = 3
	REQUEST_PHASE_RESPONSE_HEADERS  = 4
	REQUEST_PHASE_RESPONSE_BODY     = 5
	REQUEST_PHASE_RESPONSE_TRAILERS = 6
)

func RequestPhaseToString(phase int) string {
	switch phase {
	case REQUEST_PHASE_UNDETERMINED:
		return "UNDETERMINED"
	case REQUEST_PHASE_REQUEST_HEADERS:
		return "REQUEST_HEADERS"
	case REQUEST_PHASE_REQUEST_BODY:
		return "REQUEST_BODY"
	case REQUEST_PHASE_REQUEST_TRAILERS:
		return "REQUEST_TRAILERS"
	case REQUEST_PHASE_RESPONSE_HEADERS:
		return "RESPONSE_HEADERS"
	case REQUEST_PHASE_RESPONSE_BODY:
		return "RESPONSE_BODY"
	case REQUEST_PHASE_RESPONSE_TRAILERS:
		return "RESPONSE_TRAILERS"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", phase)
	}
}

const kContentLength = "Content-Length"

type PhaseResponse struct {
	headerMutation    *extprocv3.HeaderMutation    // any response
	bodyMutation      *extprocv3.BodyMutation      // body responses
	continueRequest   *extprocv3.CommonResponse    // headers/body responses
	immediateResponse *extprocv3.ImmediateResponse // headers/body responses
}

type HeaderValue struct {
	Value    string
	RawValue []byte
}

func (hv HeaderValue) IsValid() bool {
	if len(hv.Value) > 0 && hv.RawValue != nil {
		return false
	}
	return true
}

func (hv HeaderValue) ToEnvoyHeaderValue(name string) *corev3.HeaderValue {
	if len(hv.Value) > 0 && hv.RawValue == nil {
		return &corev3.HeaderValue{
			Key:      name,
			RawValue: []byte(hv.Value),
		}
	}
	return &corev3.HeaderValue{
		Key:      name,
		RawValue: hv.RawValue,
	}
}

func BuildHeaderValuesFromMap(headers map[string]string) map[string]HeaderValue {
	h := make(map[string]HeaderValue)
	for n := range headers {
		h[n] = HeaderValue{RawValue: []byte(headers[n])}
	}
	return h
}

type RequestContext struct {
	// parsed from header
	Scheme    string
	Authority string
	Method    string
	Path      string
	FullPath  string
	RequestID string

	AllHeaders AllHeaders

	Started     time.Time
	Duration    time.Duration
	EndOfStream bool
	data        map[string]any
	response    PhaseResponse
}

// data must be allocated here, not in initReqCtx: that only runs in the
// request headers phase, which request_header_mode: SKIP never reaches.
func newRequestContext() *RequestContext {
	return &RequestContext{
		// for custom data between phases
		data: make(map[string]any),
	}
}

func initReqCtx(rc *RequestContext, headers *corev3.HeaderMap) error {
	rc.Started = time.Now()
	rc.Duration = 0

	eitherValue := func(h *corev3.HeaderValue) string {
		if h == nil {
			return ""
		}

		val := h.Value
		if len(h.RawValue) > 0 {
			val = string(h.RawValue)
		}
		return val
	}

	// for stream phase responses (convenience)
	rc.ResetPhase()

	// string and byte header processing

	var err error
	rc.AllHeaders, err = genHeaders(headers)
	if err != nil {
		return fmt.Errorf("parsing headers failed: %w", err)
	}

	for _, h := range headers.Headers {
		switch h.Key {
		case ":scheme":
			rc.Scheme = eitherValue(h)

		case ":authority":
			rc.Authority = eitherValue(h)

		case ":method":
			rc.Method = eitherValue(h)

		case ":path":
			rc.FullPath = eitherValue(h)
			rc.Path = strings.Split(rc.FullPath, "?")[0]

		case "x-request-id":
			rc.RequestID = eitherValue(h)

		default:
		}
	}

	return nil
}

func (rc *RequestContext) GetValue(name string) (any, error) {
	val, exists := rc.data[name]
	if exists {
		return val, nil
	}
	return nil, errors.New(name + " does not exist")
}

func (rc *RequestContext) SetValue(name string, val any) error {
	rc.data[name] = val
	return nil
}

func (rc *RequestContext) ResetPhase() error {
	rc.EndOfStream = false
	rc.response.headerMutation = &extprocv3.HeaderMutation{}
	rc.response.bodyMutation = nil
	rc.response.continueRequest = nil
	rc.response.immediateResponse = nil
	return nil
}

func (rc *RequestContext) ContinueRequest() error {
	if rc.response.immediateResponse != nil {
		rc.response.immediateResponse = nil
	}

	rc.response.continueRequest = &extprocv3.CommonResponse{
		// status? (ie response phase status)
		HeaderMutation: rc.response.headerMutation,
		BodyMutation:   rc.response.bodyMutation,
		// trailers?
	}

	return nil
}

func (rc *RequestContext) CancelRequest(status int32, headers map[string]HeaderValue, body string) error {
	rc.AppendHeaders(headers)
	rc.response.continueRequest = nil
	rc.response.immediateResponse = &extprocv3.ImmediateResponse{
		Status: &typev3.HttpStatus{
			Code: typev3.StatusCode(status),
		},
		Headers: rc.response.headerMutation,
		Body:    []byte(body),
	}
	return nil
}

func (rc *RequestContext) GetResponse(phase int) (*extprocv3.ProcessingResponse, error) {

	// handle immediate responses
	if rc.response.immediateResponse != nil {
		switch phase {
		case REQUEST_PHASE_REQUEST_HEADERS, REQUEST_PHASE_REQUEST_BODY, REQUEST_PHASE_RESPONSE_HEADERS, REQUEST_PHASE_RESPONSE_BODY:
			// TODO: post-process modifications?
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ImmediateResponse{
					ImmediateResponse: rc.response.immediateResponse,
				},
			}, nil

		// trailers phases don't have an ImmediateResponse option
		// (only changes to headers permitted)
		default:
		}
	}

	// handle "common" responses (immediateResponse == nil and/or phase ignored)

	// presume no-op if common response wrapper is not defined
	// if rc.response.headerMutation == nil {
	// 	rc.response.headerMutation = &extprocv3.HeaderMutation{}
	// }
	if rc.response.continueRequest == nil {
		rc.response.continueRequest = &extprocv3.CommonResponse{}
	}

	// HACK: (?) this means any post-process modifications are added
	rc.ContinueRequest()

	switch phase {
	case REQUEST_PHASE_REQUEST_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: rc.response.continueRequest,
				},
			},
		}, nil

	case REQUEST_PHASE_REQUEST_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{
					Response: rc.response.continueRequest,
				},
			},
		}, nil

	case REQUEST_PHASE_REQUEST_TRAILERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestTrailers{
				RequestTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: rc.response.headerMutation,
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{
					Response: rc.response.continueRequest,
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: rc.response.continueRequest,
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_TRAILERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseTrailers{
				ResponseTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: rc.response.headerMutation,
				},
			},
		}, nil

	default:
		return nil, errors.New("unknown request phase")
	}
}

func (rc *RequestContext) EmptyContinueResponse(phase int) (*extprocv3.ProcessingResponse, error) {
	switch phase {
	case REQUEST_PHASE_REQUEST_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_REQUEST_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_REQUEST_TRAILERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestTrailers{
				RequestTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: &extprocv3.HeaderMutation{},
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_TRAILERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseTrailers{
				ResponseTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: &extprocv3.HeaderMutation{},
				},
			},
		}, nil

	default:
		return nil, errors.New("unknown request phase")
	}
}

func (rc *RequestContext) UpdateHeader(name string, hv HeaderValue, action string) error {
	if !hv.IsValid() {
		return fmt.Errorf("invalid header value")
	}
	hm := rc.response.headerMutation
	aa := corev3.HeaderValueOption_HeaderAppendAction(
		corev3.HeaderValueOption_HeaderAppendAction_value[action],
	)
	h := &corev3.HeaderValueOption{
		Header:       hv.ToEnvoyHeaderValue(name),
		AppendAction: aa,
	}
	hm.SetHeaders = append(hm.SetHeaders, h)
	return nil
}

func (rc *RequestContext) AppendHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "APPEND_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) AddHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "ADD_IF_ABSENT")
}

func (rc *RequestContext) OverwriteHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "OVERWRITE_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) UpdateHeaders(headers map[string]HeaderValue, action string) error {
	hm := rc.response.headerMutation
	for n, v := range headers {
		if !v.IsValid() {
			return fmt.Errorf("invalid header value for %s", n)
		}
		h := &corev3.HeaderValueOption{
			Header: v.ToEnvoyHeaderValue(n),
			AppendAction: corev3.HeaderValueOption_HeaderAppendAction(
				corev3.HeaderValueOption_HeaderAppendAction_value[action],
			),
		}
		hm.SetHeaders = append(hm.SetHeaders, h)
	}
	return nil
}

func (rc *RequestContext) AppendHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "APPEND_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) AddHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "ADD_IF_ABSENT")
}

func (rc *RequestContext) OverwriteHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "OVERWRITE_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) RemoveHeader(name string) error {
	hm := rc.response.headerMutation
	if !slices.Contains(hm.RemoveHeaders, name) {
		hm.RemoveHeaders = append(hm.RemoveHeaders, name)
	}
	return nil
}

func (rc *RequestContext) RemoveHeaders(headers []string) error {
	hm := rc.response.headerMutation
	for _, h := range headers {
		if !slices.Contains(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}

func (rc *RequestContext) RemoveHeadersVariadic(headers ...string) error {
	hm := rc.response.headerMutation
	for _, h := range headers {
		if !slices.Contains(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}

func (rc *RequestContext) ReplaceBodyChunk(body []byte) error {
	size := len(body)
	if size == 0 {
		return nil
	}

	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: body,
		},
	}

	rc.OverwriteHeader(kContentLength, HeaderValue{RawValue: []byte(strconv.Itoa(size))})

	return nil
}

func (rc *RequestContext) ClearBodyChunk() error {
	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_ClearBody{
			ClearBody: true,
		},
	}
	rc.OverwriteHeader(kContentLength, HeaderValue{RawValue: []byte(strconv.Itoa(0))})
	return nil
}
