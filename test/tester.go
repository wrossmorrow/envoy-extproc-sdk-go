package main

import (
	"encoding/json"
	"log"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type testingRequestProcessor struct {
	opts *ep.ProcessingOptions
}

type TesterRequestBody struct {
	// request processing options
	AppendRequestHeaders    map[string]string `json:"append_request_headers" yaml:"append_request_headers"`
	AddRequestHeaders       map[string]string `json:"add_request_headers" yaml:"add_request_headers"`
	OverwriteRequestHeaders map[string]string `json:"overwrite_request_headers" yaml:"overwrite_request_headers"`
	RemoveRequestHeaders    []string          `json:"remove_request_headers" yaml:"remove_request_headers"`
	ClearRequestBody        bool              `json:"clear_request_body" yaml:"clear_request_body"`
	ReplaceRequestBody      string            `json:"replace_request_body" yaml:"replace_request_body"`

	// response processing options
	AppendResponseHeaders    map[string]string `json:"append_response_headers" yaml:"append_response_headers"`
	AddResponseHeaders       map[string]string `json:"add_response_headers" yaml:"add_response_headers"`
	OverwriteResponseHeaders map[string]string `json:"overwrite_response_headers" yaml:"overwrite_response_headers"`
	RemoveResponseHeaders    []string          `json:"remove_response_headers" yaml:"remove_response_headers"`
	ClearResponseBody        bool              `json:"clear_response_body" yaml:"clear_response_body"`
	ReplaceResponseBody      string            `json:"replace_response_body" yaml:"replace_response_body"`

	// cancellation
	CancelRequest        bool              `json:"cancel_request" yaml:"cancel_request"`
	CancelRequestStatus  int32             `json:"cancel_request_status" yaml:"cancel_request_status"`
	CancelRequestHeaders map[string]string `json:"cancel_request_headers" yaml:"cancel_request_headers"`
	CancelRequestBody    string            `json:"cancel_request_body" yaml:"cancel_request_body"`
}

func (s *testingRequestProcessor) GetName() string {
	return "testing"
}

func (s *testingRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *testingRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *testingRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {

	// NOTE: assuming buffering, not streaming; if buffering we have to
	// accumulate the request body chunks sent until the end of stream

	actions := TesterRequestBody{}
	err := json.Unmarshal(body, &actions)
	if err != nil {
		log.Printf("Error parsing request body: %v", err)
		return ctx.ContinueRequest()
	}
	ctx.SetValue("actions", actions)

	if actions.CancelRequest {
		headers := ep.BuildHeaderValuesFromMap(actions.CancelRequestHeaders)
		return ctx.CancelRequest(actions.CancelRequestStatus, headers, actions.CancelRequestBody)
	}

	for k, v := range actions.AppendRequestHeaders {
		ctx.AppendHeader(k, ep.HeaderValue{Value: v})
	}

	for k, v := range actions.AddRequestHeaders {
		ctx.AddHeader(k, ep.HeaderValue{Value: v})
	}

	for k, v := range actions.OverwriteRequestHeaders {
		ctx.OverwriteHeader(k, ep.HeaderValue{Value: v})
	}

	for _, k := range actions.RemoveRequestHeaders {
		ctx.RemoveHeader(k)
	}

	if actions.ClearRequestBody {
		ctx.ClearBodyChunk()
	} else if actions.ReplaceRequestBody != "" {
		ctx.ReplaceBodyChunk([]byte(actions.ReplaceRequestBody))
	}

	return ctx.ContinueRequest()
}

func (s *testingRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *testingRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {

	val, err := ctx.GetValue("actions")
	if err == nil {
		actions := val.(TesterRequestBody)
		for k, v := range actions.AppendResponseHeaders {
			ctx.AppendHeader(k, ep.HeaderValue{Value: v})
		}

		for k, v := range actions.AddResponseHeaders {
			ctx.AddHeader(k, ep.HeaderValue{Value: v})
		}

		for k, v := range actions.OverwriteResponseHeaders {
			ctx.OverwriteHeader(k, ep.HeaderValue{Value: v})
		}

		for _, k := range actions.RemoveResponseHeaders {
			ctx.RemoveHeader(k)
		}
	}

	return ctx.ContinueRequest()
}

func (s *testingRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {

	// NOTE: assuming buffering, not streaming; if buffering we have to
	// accumulate the request body chunks sent until the end of stream

	val, err := ctx.GetValue("actions")
	if err == nil {
		actions := val.(TesterRequestBody)
		if actions.ClearResponseBody {
			ctx.ClearBodyChunk()
		} else if actions.ReplaceResponseBody != "" {
			ctx.ReplaceBodyChunk([]byte(actions.ReplaceResponseBody))
		}
	}

	return ctx.ContinueRequest()
}

func (s *testingRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *testingRequestProcessor) ErrorHandler(ctx *ep.RequestContext, phase int, err error) {
	log.Printf("Error in phase %s: %v", ep.RequestPhaseToString(phase), err)
}

func (s *testingRequestProcessor) Close(gracePeriodSeconds int32) error {
	log.Printf("Closing tester")
	return nil
}
