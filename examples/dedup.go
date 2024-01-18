package main

import (
	"crypto/sha256"
	"encoding/hex"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var cache map[string]bool

type dedupRequestProcessor struct {
	opts *ep.ProcessingOptions
}

func dedupable(ctx *ep.RequestContext) bool {
	switch ctx.Method {
	case "PUT", "POST", "PATCH":
		return true
	default:
		return false
	}
}

func cacheRequest(ctx *ep.RequestContext, digest string) {
	if cache == nil {
		cache = make(map[string]bool)
	}
	cache[digest] = true
}

func uncacheRequest(digest string) {
	if isRequestCached(digest) {
		delete(cache, digest)
	}
}

func isRequestCached(digest string) bool {
	if cache == nil {
		cache = make(map[string]bool)
		return false
	}
	_, cached := cache[digest]
	return cached
}

func (s *dedupRequestProcessor) GetName() string {
	return "dedup"
}

func (s *dedupRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *dedupRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string, headerRawValues map[string][]byte) error {
	hasher := sha256.New()
	ctx.SetValue("hasher", hasher)

	hasher.Write([]byte(ctx.Method + ":" + ctx.Path)) // method:path

	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		ctx.SetValue("digest", digest)
		ctx.AddHeader("x-extproc-request-digest", digest)
		if dedupable(ctx) {
			if isRequestCached(digest) {
				return ctx.CancelRequest(409, make(map[string]string), "")

			} else {
				cacheRequest(ctx, digest)
			}
		}
	}

	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {

	hasher, _ := getHasher(ctx)
	hasher.Write([]byte(":"))
	hasher.Write(body)
	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		ctx.SetValue("digest", digest)
		ctx.AddHeader("x-extproc-request-digest", digest)
		if dedupable(ctx) {
			if isRequestCached(digest) {
				return ctx.CancelRequest(409, make(map[string]string), "")

			} else {
				cacheRequest(ctx, digest)
			}
		}
	}
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string, rawValues map[string][]byte) error {
	digest, _ := getDigest(ctx)
	uncacheRequest(digest)
	if ctx.EndOfStream {
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	digest, _ := getDigest(ctx)
	uncacheRequest(digest)
	if ctx.EndOfStream {
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string, rawValues map[string][]byte) error {
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) Init(opts *ep.ProcessingOptions, extnonFlagArgsraArgs []string) error {
	s.opts = opts
	return nil
}

func (s *dedupRequestProcessor) Finish() {}
