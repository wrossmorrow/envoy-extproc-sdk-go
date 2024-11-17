package extproc

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

type PhaseResponse struct {
	headerMutation    *extprocv3.HeaderMutation    // any response
	bodyMutation      *extprocv3.BodyMutation      // body responses
	continueRequest   *extprocv3.CommonResponse    // headers/body responses
	immediateResponse *extprocv3.ImmediateResponse // headers/body responses
}

type RequestContext struct {
	Scheme    string              // from :scheme header
	Authority string              // from :authority header
	Method    string              // from :method header
	FullPath  string              // from :path header
	Path      string              // from :path header, parsed
	Query     map[string][]string // from :path header, parsed
	RequestID string              // from x-request-id header, if present

	AllHeaders *AllHeaders // all request/response headers

	Status   uint16        // response status code, when available
	Started  time.Time     // when processing started
	Duration time.Duration // appoximate, cumulative duration of external processing

	EndOfStream bool

	data       map[string]any // named data store for clients passing values
	response   PhaseResponse
	bodybuffer *EncodedBody // reset on request headers, response headers
}

func eitherValue(h *corev3.HeaderValue) string {
	if h == nil {
		return ""
	}

	val := h.Value
	if len(h.RawValue) > 0 {
		if utf8.Valid(h.RawValue) {
			val = string(h.RawValue)
		}
	}
	return val
}

// func initReqCtx(rc *RequestContext, headers *corev3.HeaderMap) error {
func initReqCtx(rc *RequestContext, headers *AllHeaders) error {
	rc.Started = time.Now()
	rc.Duration = 0

	// for custom data between phases
	rc.data = make(map[string]any)

	// for stream phase responses (convenience)
	rc.ResetPhase()

	// string and byte header processing for "standard" data

	var err error

	// rc.AllHeaders, err = NewAllHeadersFromEnvoyHeaderMap(headers)
	// if err != nil {
	// 	return fmt.Errorf("parse header is failed: %w", err)
	// }
	rc.AllHeaders = headers

	// parse internal data -- an alternative would be to receive all headers,
	// extract all these values, and then drop the envoy headers. That shouldn't
	// iterate over all headers as well.
	rc.Scheme, _ = rc.AllHeaders.GetHeaderValueAsString(":scheme")
	rc.Authority, _ = rc.AllHeaders.GetHeaderValueAsString(":authority")
	rc.Method, _ = rc.AllHeaders.GetHeaderValueAsString(":method")
	rc.FullPath, _ = rc.AllHeaders.GetHeaderValueAsString(":path")

	pathParts := strings.Split(rc.FullPath, "?")
	rc.Path = pathParts[0]
	if len(pathParts) > 1 {
		rc.Query, err = url.ParseQuery(pathParts[1])
		if err != nil {
			fmt.Printf("failed to parse query string: %v\n", err)
			rc.Query = nil
		}
	} else {
		rc.Query = nil
	}

	rc.RequestID, err = rc.AllHeaders.GetHeaderValueAsString("x-request-id")
	if err != nil {
		rc.RequestID = ""
	}

	// for _, h := range headers.Headers {
	// 	switch h.Key {
	// 	case ":scheme":
	// 		rc.Scheme = eitherValue(h)

	// 	case ":authority":
	// 		rc.Authority = eitherValue(h)

	// 	case ":method":
	// 		rc.Method = eitherValue(h)

	// 	case ":path":
	// 		rc.FullPath = eitherValue(h)
	// 		pathParts := strings.Split(rc.FullPath, "?")
	// 		rc.Path = pathParts[0]
	// 		if len(pathParts) > 1 {
	// 			rc.Query, err = url.ParseQuery(pathParts[1])
	// 			if err != nil {
	// 				fmt.Printf("failed to parse query string: %v\n", err)
	// 				rc.Query = nil
	// 			}
	// 		} else {
	// 			rc.Query = nil
	// 		}

	// 	case "x-request-id":
	// 		rc.RequestID = eitherValue(h)

	// 	default:
	// 	}
	// }

	// remove "envoy" headers from (copied) headers, so clients don't need to parse
	rc.AllHeaders.DropHeadersNamedStartingWith(":")

	return nil
}

// @deprecate: migrate to clearer name "HasStoredValue"
func (rc *RequestContext) HasValue(name string) bool {
	_, exists := rc.data[name]
	return exists
}

func (rc *RequestContext) HasStoredValue(name string) bool {
	_, exists := rc.data[name]
	return exists
}

// @deprecate: migrate to clearer name "GetStoredValue"
func (rc *RequestContext) GetValue(name string) (any, error) {
	val, exists := rc.data[name]
	if exists {
		return val, nil
	}
	return nil, errors.New(name + " does not exist")
}

func (rc *RequestContext) GetStoredValue(name string) (any, error) {
	val, exists := rc.data[name]
	if exists {
		return val, nil
	}
	return nil, errors.New(name + " does not exist")
}

// @deprecate: migrate to clearer name "SetStoredValue"
func (rc *RequestContext) SetValue(name string, val any) error {
	rc.data[name] = val
	return nil
}

func (rc *RequestContext) SetStoredValue(name string, val any) error {
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

func (rc *RequestContext) UpdateHeader(name string, hv HeaderValue, action string) error {
	if len(hv.Value) != 0 && hv.RawValue != nil {
		return fmt.Errorf("only one of 'value' or 'raw_value' can be set")
	}
	hm := rc.response.headerMutation
	aa := corev3.HeaderValueOption_HeaderAppendAction(
		corev3.HeaderValueOption_HeaderAppendAction_value[action],
	)
	h := &corev3.HeaderValueOption{
		Header:       &corev3.HeaderValue{Key: name, Value: hv.Value, RawValue: hv.RawValue},
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
	return nil
}

func (rc *RequestContext) parseStatusFromResponseHeaders(headers AllHeaders) error {

	rc.Status = uint16(0)

	statusStrVals, statusBytes, exists := headers.GetHeaderValue(":status")
	if !exists {
		return errors.New("no :status header exists in AllHeaders passed")
	}

	var err error
	var statusInt int64

	if statusBytes != nil && len(statusBytes) > 0 {
		statusStr := string(statusBytes)
		statusInt, err = strconv.ParseInt(statusStr, 0, 16)
		if err != nil {
			return err
		}
		rc.Status = uint16(statusInt)
		return nil
	}

	if statusStrVals != nil && len(statusStrVals) > 0 {
		statusStr := statusStrVals[0] // take first, only first
		statusInt, err = strconv.ParseInt(statusStr, 0, 16)
		if err != nil {
			return err
		}
		rc.Status = uint16(statusInt)
		return nil
	}

	return errors.New("Could not parse existing :status header as a status")
}
