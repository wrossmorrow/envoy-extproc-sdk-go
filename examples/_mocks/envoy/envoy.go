package main

import (
	"github.com/google/uuid"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

type HttpRequest struct {
	Method   string            `yaml:"method"`
	Path     string            `yaml:"path"`
	Headers  map[string]string `yaml:"headers"`
	Body     string            `yaml:"body"`
	Trailers map[string]string `yaml:"trailers"`
}

type HttpResponse struct {
	Status   int               `yaml:"status"`
	Headers  map[string]string `yaml:"headers"`
	Body     string            `yaml:"body"`
	Trailers map[string]string `yaml:"trailers"`
}

type envoyStream struct {
	phases []extprocv3.ProcessingRequest
}

func newEnvoyStream(req HttpRequest, resp HttpResponse) *envoyStream {
	es := &envoyStream{}
	es.phases = append(es.phases, req.getEnvoyPhases()...)
	es.phases = append(es.phases, resp.getEnvoyPhases()...)
	return es
}

func (r *HttpRequest) getEnvoyPhases() []extprocv3.ProcessingRequest {
	switch r.Method {
	case "HEAD", "OPTIONS", "GET", "DELETE": // ignore bodies in these methods
		return []extprocv3.ProcessingRequest{
			newRequestHeadersPhase(r.Method, r.Path, r.Headers),
			newRequestTrailersPhase(r.Trailers),
		}

	default:
		return []extprocv3.ProcessingRequest{
			newRequestHeadersPhase(r.Method, r.Path, r.Headers),
			newRequestBodyPhase(r.Body),
			newRequestTrailersPhase(r.Trailers),
		}
	}
}

func (r *HttpResponse) getEnvoyPhases() []extprocv3.ProcessingRequest {
	return []extprocv3.ProcessingRequest{
		newResponseHeadersPhase(r.Status, r.Headers),
		newResponseBodyPhase(r.Body),
		newResponseTrailersPhase(r.Trailers),
	}
}

func newRequestHeadersPhase(method string, path string, headers map[string]string) extprocv3.ProcessingRequest {
	hm := &corev3.HeaderMap{}
	hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: ":scheme", Value: "http"})
	hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: ":authority", Value: *serverAddr})
	hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: ":method", Value: method})
	hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: ":path", Value: path})
	hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: "x-request-id", Value: uuid.New().String()})
	for k, v := range headers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, Value: v})
	}

	rh := &extprocv3.HttpHeaders{Headers: hm}
	switch method {
	case "HEAD", "OPTIONS", "GET", "DELETE": // ignore bodies in these methods
		rh.EndOfStream = true

	default:
	}

	return extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: rh},
	}
}

func newRequestBodyPhase(body string) extprocv3.ProcessingRequest {
	return extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestBody{
			RequestBody: &extprocv3.HttpBody{
				Body:        []byte(body),
				EndOfStream: true,
			},
		},
	}
}

func newRequestTrailersPhase(trailers map[string]string) extprocv3.ProcessingRequest {
	hm := &corev3.HeaderMap{}
	for k, v := range trailers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, Value: v})
	}

	rt := &extprocv3.HttpTrailers{Trailers: hm}
	return extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestTrailers{RequestTrailers: rt},
	}
}

func newResponseHeadersPhase(status int, headers map[string]string) extprocv3.ProcessingRequest {
	hm := &corev3.HeaderMap{}
	for k, v := range headers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, Value: v})
	}

	rh := &extprocv3.HttpHeaders{Headers: hm}

	return extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: rh},
	}
}

func newResponseBodyPhase(body string) extprocv3.ProcessingRequest {
	return extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{
				Body:        []byte(body),
				EndOfStream: true,
			},
		},
	}
}

func newResponseTrailersPhase(trailers map[string]string) extprocv3.ProcessingRequest {
	hm := &corev3.HeaderMap{}
	for k, v := range trailers {
		hm.Headers = append(hm.Headers, &corev3.HeaderValue{Key: k, Value: v})
	}

	rt := &extprocv3.HttpTrailers{Trailers: hm}
	return extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseTrailers{ResponseTrailers: rt},
	}
}
