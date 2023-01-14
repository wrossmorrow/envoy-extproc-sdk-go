package main

import (
	"log"
	"crypto/sha256"
	"encoding/hex"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var cache map[string]bool

type dedupRequestProcessor struct{}

func dedupable(ctx *ep.RequestContext) bool {
	switch ctx.Method {
	case "PUT", "POST", "PATCH":
		return true
	default:
		return false
	}
}

func cacheRequest(ctx *ep.RequestContext, digest string) {
	log.Printf("  cache: %v", cache)
	if cache == nil {
		cache = make(map[string]bool)
	}
	cache[digest] = true
	log.Printf("  cache: %v", cache)
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

func (s dedupRequestProcessor) GetName() string {
	return "dedup"
}

func (s dedupRequestProcessor) GetOptions() *ep.ProcessingOptions {
	opts := ep.NewOptions()
	opts.LogStream = true
	opts.LogPhases = true
	opts.UpdateExtProcHeader = true
	opts.UpdateDurationHeader = true
	return opts
}

func (s dedupRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers map[string][]string) error {

	hasher := sha256.New()
	ctx.SetValue("hasher", hasher)

	hasher.Write([]byte(ctx.Method + ":" + ctx.Path)) // method:path

	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		ctx.SetValue("digest", digest)
		ctx.AddHeader("x-extproc-request-digest", digest)
		if dedupable(ctx) {
			log.Print("Request is de-dupable")
			log.Printf("  digest: %s", digest)
			if isRequestCached(digest) {
				log.Print("Request is cached")
				return ctx.CancelRequest(409, make(map[string]string), "")
			} else {
				cacheRequest(ctx, digest)
			}
		}
	}

	return ctx.ContinueRequest()
}

func (s dedupRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error { 

	hasher, _ := getHasher(ctx)
	hasher.Write([]byte(":"))
	hasher.Write(body)
	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		ctx.SetValue("digest", digest)
		ctx.AddHeader("x-extproc-request-digest", digest)
		if dedupable(ctx) {
			log.Print("Request is de-dupable")
			log.Printf("  digest: %s", digest)
			if isRequestCached(digest) {
				log.Print("Request is cached")
				return ctx.CancelRequest(409, make(map[string]string), "")
			} else {
				cacheRequest(ctx, digest)
			}
		}
	}
	return ctx.ContinueRequest()
}

func (s dedupRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}

func (s dedupRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers map[string][]string) error {
	digest, _ := getDigest(ctx)
	uncacheRequest(digest)
	if ctx.EndOfStream {
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s dedupRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	digest, _ := getDigest(ctx)
	uncacheRequest(digest)
	if ctx.EndOfStream {
		ctx.AddHeader("x-extproc-request-digest", digest)
	}
	return ctx.ContinueRequest()
}

func (s dedupRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers map[string][]string) error {
	return ctx.ContinueRequest()
}
