package extproc

import (
	"errors"
	"fmt"
	"log"
	"slices"
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

type RequestContext struct {
	Scheme    string
	Authority string
	Method    string
	Path      string
	FullPath  string
	RequestID string

	Headers    map[string][]string
	RawHeaders map[string][]byte

	Started     time.Time
	Duration    time.Duration
	EndOfStream bool
	data        map[string]any
	response    PhaseResponse
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

	// for custom data between phases
	rc.data = make(map[string]any)

	// for stream phase responses (convenience)
	rc.ResetPhase()

	// string and byte header processing
	//rc.RawHeaders = allHeaders.RawHeaders
	rc.RawHeaders = map[string][]byte{}
	rc.Headers = map[string][]string{}
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
			if len(h.Value) != 0 {
				rc.Headers[h.Key] = strings.Split(h.Value, ",")
			} else {
				rc.RawHeaders[h.Key] = h.RawValue
			}

		}
	}

	return nil
}

func (rc *RequestContext) AllHeaders() AllHeaders {
	return AllHeaders{rc.Headers, rc.RawHeaders}
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
	rc.response.bodyMutation = &extprocv3.BodyMutation{}
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
	log.Printf("Cancelling request: %d, %v, %s", status, headers, body)
	rc.AppendHeaders(headers)
	rc.response.continueRequest = nil
	rc.response.immediateResponse = &extprocv3.ImmediateResponse{
		Status: &typev3.HttpStatus{
			Code: typev3.StatusCode(status),
		},
		Headers: rc.response.headerMutation,
		Body:    body,
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

func (rc *RequestContext) UpdateHeader(name string, value string, rawValue []byte, action string) error {
	if len(value) != 0 && rawValue != nil {
		return fmt.Errorf("only one of 'value' or 'raw_value' can be set")
	}
	hm := rc.response.headerMutation
	aa := corev3.HeaderValueOption_HeaderAppendAction(
		corev3.HeaderValueOption_HeaderAppendAction_value[action],
	)
	h := &corev3.HeaderValueOption{
		Header:       &corev3.HeaderValue{Key: name, Value: value, RawValue: rawValue},
		AppendAction: aa,
	}
	hm.SetHeaders = append(hm.SetHeaders, h)
	return nil
}

func (rc *RequestContext) AppendHeader(name string, value string, rawValue []byte) error {
	return rc.UpdateHeader(name, value, rawValue, "APPEND_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) AddHeader(name string, value string, rawValue []byte) error {
	return rc.UpdateHeader(name, value, rawValue, "ADD_IF_ABSENT")
}

func (rc *RequestContext) OverwriteHeader(name string, value string, rawValue []byte) error {
	return rc.UpdateHeader(name, value, rawValue, "OVERWRITE_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) UpdateHeaders(headers map[string]HeaderValue, action string) error {
	hm := rc.response.headerMutation
	aa := corev3.HeaderValueOption_HeaderAppendAction(
		corev3.HeaderValueOption_HeaderAppendAction_value[action],
	)
	for k, v := range headers {
		if len(v.Value) != 0 && v.RawValue != nil {
			return fmt.Errorf("only one of 'value' or 'raw_value' can be set")
		}
		h := &corev3.HeaderValueOption{
			Header:       &corev3.HeaderValue{Key: k, Value: v.Value, RawValue: v.RawValue},
			AppendAction: aa,
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
	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: body,
		},
	}
	return nil
}

func (rc *RequestContext) ClearBodyChunk() error {
	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_ClearBody{
			ClearBody: true,
		},
	}
	return nil
}
