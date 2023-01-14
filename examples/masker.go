package main

import (
	"log"
	"encoding/json"

	"github.com/nqd/flat"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var masked = map[string][]string{
	"request": []string{
		"maskme",
		"mask.me",
	},
	"response": []string{},
}

type maskerRequestProcessor struct{}

func isMaybeJSON(headers map[string][]string) bool {
	types, exists := headers["content-type"]
	if !exists {
		return false
	}

	for _, t := range types {
		if t == "application/json" {
			return true
		}
	}
	return false
}

func maskJSONData(jsonPaths []string, body []byte) ([]byte, error) {

	var (
		data map[string]interface{}
		err error
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

func (s maskerRequestProcessor) GetName() string {
	return "masker"
}

func (s maskerRequestProcessor) GetOptions() *ep.ProcessingOptions {
	opts := ep.NewOptions()
	opts.LogStream = true
	opts.LogPhases = true
	opts.UpdateExtProcHeader = true
	opts.UpdateDurationHeader = true
	return opts
}

func (s maskerRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s maskerRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	// unmarshal JSON body (if content-type: application/json)
	// examine for matching paths
	// "mask" data at all matching paths
	// replace body, unmarshalled to []byte
	if len(masked["request"]) > 0 {
		log.Print("examining request body")
		if isMaybeJSON(ctx.Headers) {
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

func (s maskerRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s maskerRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	ctx.SetValue("responseHeaders", headers)
	return ctx.ContinueRequest()
}

func (s maskerRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	// unmarshal JSON body (if content-type: application/json)
	// examine for matching paths
	// "mask" data at all matching paths
	// replace body, unmarshalled to []byte
	if len(masked["response"]) > 0 {
		rh, _ := ctx.GetValue("responseHeaders")
		if isMaybeJSON(rh.(map[string][]string)) {
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

func (s maskerRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}
