package main

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/nqd/flat"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var masked = map[string][]string{
	"request": {
		"maskme",
		"mask.me",
	},
	"response": {},
}

type maskerRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func isMaybeJSON(headers ep.AllHeaders) bool {
	for _, t := range headers.Values("content-type") {
		// NOTE: media type may carry parameters, eg "application/json; charset=utf-8"
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(t)), "application/json") {
			return true
		}
	}
	return false
}

func maskJSONData(jsonPaths []string, body []byte) ([]byte, error) {

	var (
		data map[string]any
		err  error
	)

	err = json.Unmarshal(body, &data) // get JSON data
	if err != nil {
		return body, err
	}

	flattened, errf := flat.Flatten(data, nil) // flatten it
	if errf != nil {
		return body, errf
	}

	for _, key := range jsonPaths {
		_, exists := flattened[key]
		if exists {
			flattened[key] = "****"
		}
	}

	unfl, erru := flat.Unflatten(flattened, nil)
	if erru != nil {
		return body, erru
	}

	masked, errj := json.Marshal(unfl)
	if errj != nil {
		return body, errj
	}

	return masked, nil

}

func (s *maskerRequestProcessor) GetName() string {
	return "masker"
}

func (s *maskerRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *maskerRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *maskerRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	// unmarshal JSON body (if content-type: application/json)
	// examine for matching paths
	// "mask" data at all matching paths
	// replace body, unmarshalled to []byte
	if len(masked["request"]) > 0 {
		log.Print("examining request body")
		if isMaybeJSON(ctx.AllHeaders) {
			log.Print("request body may be JSON")
			masked, err := maskJSONData(masked["request"], body)
			if err != nil {
				log.Printf("Error: %v", err)
			} else {
				ctx.ReplaceBodyChunk(masked)
			}
		}
	}
	return ctx.ContinueRequest()
}

func (s *maskerRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *maskerRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	ctx.SetValue("responseHeaders", headers)
	return ctx.ContinueRequest()
}

func (s *maskerRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	// unmarshal JSON body (if content-type: application/json)
	// examine for matching paths
	// "mask" data at all matching paths
	// replace body, unmarshalled to []byte
	if len(masked["response"]) > 0 {
		rh, _ := ctx.GetValue("responseHeaders")
		// stored as ep.AllHeaders in ProcessResponseHeaders; the previous
		// assertion to map[string][]string would have panicked here
		headers, ok := rh.(ep.AllHeaders)
		if ok && isMaybeJSON(headers) {
			masked, err := maskJSONData(masked["response"], body)
			if err != nil {
				log.Printf("Error: %v", err)
			} else {
				ctx.ReplaceBodyChunk(masked)
			}
		}
	}
	return ctx.ContinueRequest()
}

func (s *maskerRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *maskerRequestProcessor) ErrorHandler(ctx *ep.RequestContext, phase int, err error) {
	log.Printf("Error in phase %s: %v", ep.RequestPhaseToString(phase), err)
}

func (s *maskerRequestProcessor) Close(gracePeriodSeconds int32) error {
	log.Printf("Closing %s", s.GetName())
	return nil
}

func (s *maskerRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *maskerRequestProcessor) Finish() {}
