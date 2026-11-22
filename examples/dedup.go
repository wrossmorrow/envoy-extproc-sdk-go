package main

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"log"
	"sync"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

const dedupAcquiredDigestKey = "dedup_acquired_digest"

type dedupRequestProcessor struct {
	opts     *ep.ProcessingOptions
	mu       sync.Mutex
	inFlight map[string]struct{}
}

func dedupable(ctx *ep.RequestContext) bool {
	switch ctx.Method {
	case "PUT", "POST", "PATCH":
		return true
	default:
		return false
	}
}

func (s *dedupRequestProcessor) tryAcquire(ctx *ep.RequestContext, digest string) bool {
	s.mu.Lock()
	if s.inFlight == nil {
		s.inFlight = make(map[string]struct{})
	}
	if _, exists := s.inFlight[digest]; exists {
		s.mu.Unlock()
		return false
	}
	s.inFlight[digest] = struct{}{}
	s.mu.Unlock()

	if ctx != nil {
		_ = ctx.SetValue(dedupAcquiredDigestKey, digest)
	}
	return true
}

func (s *dedupRequestProcessor) releaseOwned(ctx *ep.RequestContext) {
	if ctx == nil {
		return
	}
	val, err := ctx.GetValue(dedupAcquiredDigestKey)
	if err != nil {
		return
	}
	digest, ok := val.(string)
	if !ok || digest == "" {
		return
	}
	// Clear stream ownership before deleting so a later phase or duplicate
	// release cannot evict a slot acquired by a subsequent request.
	_ = ctx.SetValue(dedupAcquiredDigestKey, "")

	s.mu.Lock()
	delete(s.inFlight, digest)
	s.mu.Unlock()
}

func (s *dedupRequestProcessor) finishRequestDigest(ctx *ep.RequestContext, hasher hash.Hash) error {
	digest := hex.EncodeToString(hasher.Sum(nil))
	_ = ctx.SetValue("digest", digest)
	_ = ctx.AddHeader("x-extproc-request-digest", ep.HeaderValue{RawValue: []byte(digest)})
	if !ctx.ObservabilityMode && dedupable(ctx) {
		if !s.tryAcquire(ctx, digest) {
			return ctx.CancelRequest(409, map[string]ep.HeaderValue{}, "")
		}
	}
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) handleResponsePhase(ctx *ep.RequestContext) error {
	s.releaseOwned(ctx)
	if ctx.EndOfStream {
		if digest, err := getDigest(ctx); err == nil && digest != "" {
			_ = ctx.AddHeader("x-extproc-request-digest", ep.HeaderValue{RawValue: []byte(digest)})
		}
	}
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) GetName() string {
	return "dedup"
}

func (s *dedupRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *dedupRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	hasher := sha256.New()
	_ = ctx.SetValue("hasher", hasher)

	hasher.Write([]byte(ctx.Method + ":" + ctx.Path)) // method:path

	if ctx.EndOfStream {
		return s.finishRequestDigest(ctx, hasher)
	}

	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	hasher, err := getHasher(ctx)
	if err != nil || hasher == nil {
		hasher = sha256.New()
		_ = ctx.SetValue("hasher", hasher)
	}
	hasher.Write([]byte(":"))
	hasher.Write(body)
	if ctx.EndOfStream {
		return s.finishRequestDigest(ctx, hasher)
	}
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	return s.handleResponsePhase(ctx)
}

func (s *dedupRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	return s.handleResponsePhase(ctx)
}

func (s *dedupRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	s.releaseOwned(ctx)
	return ctx.ContinueRequest()
}

func (s *dedupRequestProcessor) ErrorHandler(ctx *ep.RequestContext, phase int, err error) {
	s.releaseOwned(ctx)
	log.Printf("Error in phase %s: %v", ep.RequestPhaseToString(phase), err)
}

func (s *dedupRequestProcessor) Close(gracePeriodSeconds int32) error {
	log.Printf("Closing %s", s.GetName())
	return nil
}

func (s *dedupRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.mu.Lock()
	s.opts = opts
	if s.inFlight == nil {
		s.inFlight = make(map[string]struct{})
	}
	s.mu.Unlock()
	return nil
}
