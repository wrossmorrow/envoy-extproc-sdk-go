package main

import (
	"errors"
	"strconv"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

type requestContext struct {
	scheme    string
	authority string
	method    string
	path      string
	requestId string
	started   int64
	duration  int64
	data      map[string]interface{}
}

func NewReqCtx(headers *corev3.HeaderMap) (*requestContext, error) {

	rc := requestContext{}

	rc.started = time.Now().UnixNano()
	rc.duration = 0
	rc.data = make(map[string]interface{})

	for _, h := range headers.Headers {
		switch h.Key {
		case ":scheme":
			rc.scheme = h.Value
			break
		case ":authority":
			rc.authority = h.Value
			break
		case ":method":
			rc.method = h.Value
			break
		case ":path":
			rc.path = h.Value
			break
		case "x-request-id":
			rc.requestId = h.Value
			break
		default:
			break
		}
	}

	return &rc, nil
}

func (rc *requestContext) GetValue(name string) (interface{}, error) {
	val, exists := rc.data[name]
	if exists {
		return val, nil
	}
	return nil, errors.New(name + " does not exist")
}

func (rc *requestContext) SetValue(name string, val interface{}) error {
	rc.data[name] = val
	return nil
}

func (rc *requestContext) StartedHeader() (*corev3.HeaderValueOption, error) {
	return &corev3.HeaderValueOption{
		AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		Header: &corev3.HeaderValue{
			Key:   "x-extproc-started",
			Value: string(strconv.FormatInt(rc.started, 10)),
		},
	}, nil
}

func (rc *requestContext) DurationHeader() (*corev3.HeaderValueOption, error) {
	return &corev3.HeaderValueOption{
		AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		Header: &corev3.HeaderValue{
			Key:   "x-extproc-duration",
			Value: string(strconv.FormatInt(rc.duration, 10)),
		},
	}, nil
}

func (rc *requestContext) FormCommonResponse() (*pb.CommonResponse, error) {
	return &pb.CommonResponse{HeaderMutation: &pb.HeaderMutation{}}, nil
}

func (rc *requestContext) FormImmediateResponse(status int32, body string) (*pb.ImmediateResponse, error) {
	return &pb.ImmediateResponse{
		Status: &typev3.HttpStatus{
			Code: typev3.StatusCode(status),
		},
		Headers: &pb.HeaderMutation{},
		Body:    body,
	}, nil
}

func (rc *requestContext) AddHeader(hm *pb.HeaderMutation, name string, value string, action string) error {
	h := &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: name, Value: value},
		AppendAction: corev3.HeaderValueOption_HeaderAppendAction(
			corev3.HeaderValueOption_HeaderAppendAction_value[action],
		),
	}
	hm.SetHeaders = append(hm.SetHeaders, h)
	return nil
}

func (rc *requestContext) AddHeaders(hm *pb.HeaderMutation, headers map[string]string, action string) error {
	a := corev3.HeaderValueOption_HeaderAppendAction(
		corev3.HeaderValueOption_HeaderAppendAction_value[action],
	)
	for k, v := range headers {
		h := &corev3.HeaderValueOption{
			Header:       &corev3.HeaderValue{Key: k, Value: v},
			AppendAction: a,
		}
		hm.SetHeaders = append(hm.SetHeaders, h)
	}
	return nil
}

func (rc *requestContext) RemoveHeader(hm *pb.HeaderMutation, name string) error {
	if !StrInSlice(hm.RemoveHeaders, name) {
		hm.RemoveHeaders = append(hm.RemoveHeaders, name)
	}
	return nil
}

func (rc *requestContext) RemoveHeaders(hm *pb.HeaderMutation, headers ...string) error {
	for _, h := range headers {
		if !StrInSlice(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}
