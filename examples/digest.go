package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"log"

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
	h, ok := val.(hash.Hash)
	if !ok || h == nil {
		return nil, errors.New("hasher value is invalid")
	}
	return h, nil
}

func getDigest(ctx *ep.RequestContext) (string, error) {
	val, err := ctx.GetValue("digest")
	if err != nil {
		return "", err
	}
	d, ok := val.(string)
	if !ok {
		return "", errors.New("digest value is invalid")
	}
	return d, nil
}

func maybeAddDigestHeader(ctx *ep.RequestContext) {
	if !ctx.EndOfStream {
		return
	}
	if digest, err := getDigest(ctx); err == nil && digest != "" {
		_ = ctx.AddHeader("x-extproc-request-digest", ep.HeaderValue{RawValue: []byte(digest)})
	}
}

func (s *digestRequestProcessor) GetName() string {
	return "digest"
}

func (s *digestRequestProcessor) GetOptions() *ep.ProcessingOptions {
	return s.opts
}

func (s *digestRequestProcessor) ProcessRequestHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	hasher := sha256.New()
	_ = ctx.SetValue("hasher", hasher)

	hasher.Write([]byte(ctx.Method + ":" + ctx.Path)) // method:path

	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		_ = ctx.SetValue("digest", digest)
		_ = ctx.AddHeader("x-extproc-request-digest", ep.HeaderValue{RawValue: []byte(digest)})
	}

	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessRequestBody(ctx *ep.RequestContext, body []byte) error {
	hasher, err := getHasher(ctx)
	if err != nil || hasher == nil {
		hasher = sha256.New()
		_ = ctx.SetValue("hasher", hasher)
	}
	hasher.Write([]byte(":"))
	hasher.Write(body)

	if ctx.EndOfStream {
		digest := hex.EncodeToString(hasher.Sum(nil))
		_ = ctx.SetValue("digest", digest)
		_ = ctx.AddHeader("x-extproc-request-digest", ep.HeaderValue{RawValue: []byte(digest)})
	}
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessRequestTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessResponseHeaders(ctx *ep.RequestContext, headers ep.AllHeaders) error {
	maybeAddDigestHeader(ctx)
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessResponseBody(ctx *ep.RequestContext, body []byte) error {
	maybeAddDigestHeader(ctx)
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ProcessResponseTrailers(ctx *ep.RequestContext, trailers ep.AllHeaders) error {
	return ctx.ContinueRequest()
}

func (s *digestRequestProcessor) ErrorHandler(ctx *ep.RequestContext, phase int, err error) {
	log.Printf("Error in phase %s: %v", ep.RequestPhaseToString(phase), err)
}

func (s *digestRequestProcessor) Close(gracePeriodSeconds int32) error {
	log.Printf("Closing %s", s.GetName())
	return nil
}

func (s *digestRequestProcessor) Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error {
	s.opts = opts
	return nil
}
