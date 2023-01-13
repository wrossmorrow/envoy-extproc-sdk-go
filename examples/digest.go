package main

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type digestRequestProcessor struct{}

func getHasher(ctx *ep.RequestContext) (hash.Hash, error) {
	val, err := ctx.GetValue("digest")
	if err != nil {
		return nil, err
	}
	return val.(hash.Hash), nil
}

func (s digestRequestProcessor) GetName() string {
	return "digest"
}

func (s digestRequestProcessor) GetOptions() *ep.ProcessingOptions {
	opts := ep.NewOptions()
	opts.UpdateExtProcHeader = true
	opts.UpdateDurationHeader = true
	return opts
}

func (s digestRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {

	hasher := sha256.New()
	ctx.SetValue("digest", hasher)

	hasher.Write([]byte(ctx.Method + ":" + ctx.Path)) // method:path

	// TODO: include any other "headers"?

	return ctx.ContinueRequest()
}

func (s digestRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	hasher, _ := getHasher(ctx)
	hasher.Write([]byte(":"))
	hasher.Write(body)
	return ctx.ContinueRequest()
}

func (s digestRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s digestRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	if ctx.EndOfStream {
		hasher, _ := getHasher(ctx)
		sha := hex.EncodeToString(hasher.Sum(nil))
		ctx.AddHeader("x-extproc-request-digest", sha)
	}
	return ctx.ContinueRequest()
}

func (s digestRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	if ctx.EndOfStream {
		hasher, _ := getHasher(ctx)
		sha := hex.EncodeToString(hasher.Sum(nil))
		ctx.AddHeader("x-extproc-request-digest", sha)
	}
	return ctx.ContinueRequest()
}

func (s digestRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}
