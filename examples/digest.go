package main

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type digestRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func getHasher(ctx *ep.RequestContext) (hash.Hash, error) {
	val, err := ctx.GetValue("hasher")
	if err != nil {
		return nil, err
	}
	return val.(hash.Hash), nil
}

func getDigest(ctx *ep.RequestContext) (string, error) {
	val, err := ctx.GetValue("digest")
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

func (s *digestRequestProcessor) GetName() string {
	return "digest"
}

func (s *digestRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *digestRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string, headerRawValues map[string][]byte) error {
	hasher := sha256.New()
	ctx.SetValue("hasher", hasher)

	hasher.Write([]byte(ctx.Method + ":" + ctx.Path)) // method:path

	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		ctx.SetValue("digest", digest)
		ctx.AddHeader("x-extproc-request-digest", digest)
	}

	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	hasher, _ := getHasher(ctx)
	hasher.Write([]byte(":"))
	hasher.Write(body)

	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		ctx.SetValue("digest", digest)
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string, rawValues map[string][]byte) error {
	if ctx.EndOfStream {
		digest, _ := getDigest(ctx)
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	if ctx.EndOfStream {
		digest, _ := getDigest(ctx)
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}

func (s *digestRequestProcessor) Finish() {}
