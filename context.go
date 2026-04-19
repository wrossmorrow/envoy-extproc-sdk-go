package extproc

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/google/uuid"
)

// Internal structure to manage construction of responses to streaming
// external processing requests from envoy
type PhaseResponse struct {
	headerMutation    *extprocv3.HeaderMutation    // any response
	bodyMutation      *extprocv3.BodyMutation      // body responses
	continueRequest   *extprocv3.CommonResponse    // headers/body responses
	immediateResponse *extprocv3.ImmediateResponse // headers/body responses
}

// RequestContext helps manage and pass data related to a given request
// being processed. During request header processing basic data is initialized
// and populated, thus skipping request headers is not feasible. There should
// be one context per request, and it should not be shared across requests.
type RequestContext struct {
	StreamId uuid.UUID // a generated ID for this stream (not requestID)

	Scheme    string              // from envoy's `:scheme` header
	Authority string              // from envoy's `:authority` header
	Method    string              // from envoy's `:method` header
	FullPath  string              // from envoy's ':path' header
	Path      string              // from envoy's `:path` header, parsed without query
	Query     map[string][]string // from envoy's `:path` header, parsed without path
	RequestID string              // from `x-request-id` header, if present

	AllHeaders *AllHeaders // all request/response headers

	Status   uint16        // response status code, when available, from envoy's `:status` header
	Started  time.Time     // stores when processing a specific request started
	Duration time.Duration // appoximate, cumulative duration of external processing (not request)

	EndOfStream bool // flag declaring when request/response processing is complete

	extProcOptions *ProcessingOptions // external processing options
	data           map[string]any     // named data store for clients passing values
	response       PhaseResponse      // internal response helper object
	bodybuffer     *EncodedBody       // reset on request headers, response headers
}

// Initialize a request context with parsed headers
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

	rc.RequestID, err = rc.AllHeaders.GetHeaderValueAsString(rc.extProcOptions.RequestIdHeaderName)
	if err != nil {
		fmt.Printf("request id header \"%s\" not found, using fallback \"%s\"\n", rc.extProcOptions.RequestIdHeaderName, rc.extProcOptions.RequestIdFallback)
		rc.RequestID = rc.extProcOptions.RequestIdFallback
	}

	// remove "envoy" headers from (copied) headers, so clients don't need to parse
	rc.AllHeaders.DropHeadersNamedStartingWith(":")

	return nil
}

// Internal method to append body chunks when buffering streaming bodies
func (rc *RequestContext) appendBodyChunk(chunk []byte) error {
	return rc.bodybuffer.AppendChunk(chunk)
}

// Internal handler for each "chunk" (complete or not) for a request or response
// body. This is repeated in request and response body handling.
func (rc *RequestContext) handleBodyChunk(handler BodyHandler, opts *ProcessingOptions, chunk []byte) (err error) {
	if opts.BufferStreamedBodies {
		err = rc.appendBodyChunk(chunk)
		if err == nil && rc.EndOfStream {
			rc.bodybuffer.Complete = true // EndOfStream, no (size) error
			if opts.DecompressBodies {
				err = rc.bodybuffer.DecompressBody()
				if err != nil {
					log.Printf("Failed to decompress body bytes: %v \n", err)
				}
			}
		}
		// TODO: "call only on completion" option; but if we decompress bodies, we
		// _can't_ call on every body chunk; or rather a call on each chunk is not
		// meaningful. Alternatively, it could be up to the SDK user to check
		// "ctx.HasCompleteBody()" before acting on the data.
		return handler(rc, rc.CurrentBodyBytes())
	}
	return handler(rc, chunk)
}

// @deprecate: migrate to clearer name "HasStoredValue"
func (rc *RequestContext) HasValue(name string) bool {
	_, exists := rc.data[name]
	return exists
}

// Check whether a context has a stored value of the given name
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

// Retreive a stored value having the given name from a context. Returns
// a non-nil error in the case the value does not exist (a deviation from
// standard map behavior, which may be changed).
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

// Set a stored value having under the given name in a context. Returns
// a non-nil error in the case of an error, but currently always nil.
func (rc *RequestContext) SetStoredValue(name string, val any) error {
	rc.data[name] = val
	return nil
}

// Internal method to "reset" the phase of a context, clearing internal
// data to be ready to handle the next phase.
func (rc *RequestContext) ResetPhase() error {
	rc.EndOfStream = false
	rc.response.headerMutation = &extprocv3.HeaderMutation{}
	rc.response.bodyMutation = nil
	rc.response.continueRequest = nil
	rc.response.immediateResponse = nil
	return nil
}

// Method to use to signal request processing should continue, without
// a direct response or mode changes by the external processor. This
// does not imply the request isn't _modified_ by other changes made
// during processing.
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

// Signal that request processing should be stopped with a client response
// consisting of the supplied status, headers, and body. This is useful in
// the request headers or body processing phase to send a proxied response
// directly to the client without calling the upstream. Cancelling does not
// mean "failure", the response sent back can signal a successful request.
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

// Internal method to get/form the formal envoy external processor service
// response to a streaming processing request. Returns the response for envoy,
// a flag denoting if the stream can be considered finished, and possibly an
// error. The "finished" flag in particular is so the server can cancel the
// stream with envoy, which envoy itself may not do.
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
			// }, true, nil

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

// Method to call to update a request/response header in an external processor
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

// Method to call to append a request/response header in an external processor
func (rc *RequestContext) AppendHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "APPEND_IF_EXISTS_OR_ADD")
}

// Method to call to add a request/response header in an external processor (if absent)
func (rc *RequestContext) AddHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "ADD_IF_ABSENT")
}

// Method to call to overwrite-or-add a request/response header in an external processor
func (rc *RequestContext) OverwriteHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "OVERWRITE_IF_EXISTS_OR_ADD")
}

// Method to call to batch update request/response headers in an external processor,
// with all having the same action
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

// Method to call to batch append request/response header values in an external processor
func (rc *RequestContext) AppendHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "APPEND_IF_EXISTS_OR_ADD")
}

// Method to call to batch add request/response headers in an external processor, if absent
func (rc *RequestContext) AddHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "ADD_IF_ABSENT")
}

// Method to call to batch overwrite request/response headers in an external processor
func (rc *RequestContext) OverwriteHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "OVERWRITE_IF_EXISTS_OR_ADD")
}

// Method to call to remove a request/response header in an external processor
func (rc *RequestContext) RemoveHeader(name string) error {
	hm := rc.response.headerMutation
	if !slices.Contains(hm.RemoveHeaders, name) {
		hm.RemoveHeaders = append(hm.RemoveHeaders, name)
	}
	return nil
}

// Method to call to batch remove request/response headers in an external processor
func (rc *RequestContext) RemoveHeaders(headers []string) error {
	hm := rc.response.headerMutation
	for _, h := range headers {
		if !slices.Contains(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}

// Method to call to batch remove request/response headers in an external processor,
// using variadic arguments
func (rc *RequestContext) RemoveHeadersVariadic(headers ...string) error {
	hm := rc.response.headerMutation
	for _, h := range headers {
		if !slices.Contains(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}

// Method to call to replace a request/response body chunk
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

// Method to call to clear an entire request/response body chunk
func (rc *RequestContext) ClearBodyChunk() error {
	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_ClearBody{
			ClearBody: true,
		},
	}
	return nil
}

// Return a body's declared content type, encoding, and transfer style.
func (rc *RequestContext) GetBodyType() BodyType {
	return rc.bodybuffer.Type
}

// Return whether context (thinks it) has "complete" body bytes.
func (rc *RequestContext) HasCompleteBody() bool {
	return rc.bodybuffer.Complete
}

// Return whether context (thinks it) has decompressed body bytes.
func (rc *RequestContext) HasDecompressedBody() bool {
	return rc.bodybuffer.Decompressed
}

// Return current body byte buffer, complete or incomplete, decompressed or not.
func (rc *RequestContext) CurrentBodyBytes() []byte {
	return rc.bodybuffer.Value
}

// Internal method to recover request's response status from envoy `:status` header.
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

	return errors.New("Could not parse existing `:status` header as a status")
}
