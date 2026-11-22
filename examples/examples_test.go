package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeRecvItem struct {
	req *extprocv3.ProcessingRequest
	err error
}

type fakeProcessStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	reqCh  chan fakeRecvItem
	respCh chan *extprocv3.ProcessingResponse
	doneCh chan error
}

func newFakeProcessStream(srv *ep.GenericExtProcServer) *fakeProcessStream {
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeProcessStream{
		ctx:    ctx,
		cancel: cancel,
		reqCh:  make(chan fakeRecvItem, 1),
		respCh: make(chan *extprocv3.ProcessingResponse, 1),
		doneCh: make(chan error, 1),
	}
	go func() {
		s.doneCh <- srv.Process(s)
	}()
	return s
}

func (s *fakeProcessStream) Context() context.Context {
	return s.ctx
}

func (s *fakeProcessStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeProcessStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeProcessStream) SetTrailer(metadata.MD)       {}
func (s *fakeProcessStream) SendMsg(any) error            { return nil }
func (s *fakeProcessStream) RecvMsg(any) error            { return nil }

func (s *fakeProcessStream) Recv() (*extprocv3.ProcessingRequest, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case item, ok := <-s.reqCh:
		if !ok {
			return nil, io.EOF
		}
		return item.req, item.err
	}
}

func (s *fakeProcessStream) Send(resp *extprocv3.ProcessingResponse) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.respCh <- resp:
		return nil
	}
}

func (s *fakeProcessStream) step(t *testing.T, req *extprocv3.ProcessingRequest) *extprocv3.ProcessingResponse {
	t.Helper()
	select {
	case s.reqCh <- fakeRecvItem{req: req}:
	case procErr := <-s.doneCh:
		t.Fatalf("stream ended unexpectedly before sending request: %v", procErr)
	}
	select {
	case resp := <-s.respCh:
		return resp
	case procErr := <-s.doneCh:
		t.Fatalf("stream ended unexpectedly while awaiting response: %v", procErr)
		return nil
	}
}

func (s *fakeProcessStream) finish(t *testing.T) {
	t.Helper()
	close(s.reqCh)
	if doneErr := <-s.doneCh; doneErr != nil {
		t.Fatalf("stream exited with error: %v", doneErr)
	}
}

func (s *fakeProcessStream) cancelWithErr(t *testing.T, err error) {
	t.Helper()
	s.reqCh <- fakeRecvItem{err: err}
	if doneErr := <-s.doneCh; doneErr != nil {
		t.Fatalf("cancelled stream returned non-nil error: %v", doneErr)
	}
}

func newExampleTestServer(t *testing.T, proc processor) *ep.GenericExtProcServer {
	t.Helper()
	if initErr := proc.Init(ep.NewDefaultProcessingOptions(), nil); initErr != nil {
		t.Fatalf("Init(%s) error: %v", proc.GetName(), initErr)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, srvErr := ep.NewGenericExtProcServer(proc, nil, logger)
	if srvErr != nil {
		t.Fatalf("NewGenericExtProcServer(%s) error: %v", proc.GetName(), srvErr)
	}
	return srv
}

func makeHeadersReq(method, path, contentType string, endOfStream bool) *extprocv3.ProcessingRequest {
	hdrs := []*corev3.HeaderValue{
		{Key: ":method", RawValue: []byte(method)},
		{Key: ":path", RawValue: []byte(path)},
	}
	if contentType != "" {
		hdrs = append(hdrs, &corev3.HeaderValue{Key: "content-type", RawValue: []byte(contentType)})
	}
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers:     &corev3.HeaderMap{Headers: hdrs},
				EndOfStream: endOfStream,
			},
		},
	}
}

func makeBodyReq(body []byte, endOfStream bool) *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestBody{
			RequestBody: &extprocv3.HttpBody{
				Body:        body,
				EndOfStream: endOfStream,
			},
		},
	}
}

func makeRespHeadersReq(contentType string, endOfStream bool) *extprocv3.ProcessingRequest {
	hdrs := []*corev3.HeaderValue{
		{Key: ":status", RawValue: []byte("200")},
	}
	if contentType != "" {
		hdrs = append(hdrs, &corev3.HeaderValue{Key: "content-type", RawValue: []byte(contentType)})
	}
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extprocv3.HttpHeaders{
				Headers:     &corev3.HeaderMap{Headers: hdrs},
				EndOfStream: endOfStream,
			},
		},
	}
}

func makeRespBodyReq(body []byte, endOfStream bool) *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_ResponseBody{
			ResponseBody: &extprocv3.HttpBody{
				Body:        body,
				EndOfStream: endOfStream,
			},
		},
	}
}

func TestDedupConcurrentInFlightRequests(t *testing.T) {
	proc := &dedupRequestProcessor{}
	srv := newExampleTestServer(t, proc)

	const workers = 32
	startBarrier := make(chan struct{})
	var readyWG sync.WaitGroup
	var bodyWG sync.WaitGroup
	readyWG.Add(workers)
	bodyWG.Add(workers)

	var winners atomic.Int32
	var conflicts atomic.Int32
	winnerCh := make(chan *fakeProcessStream, workers)

	for range workers {
		stream := newFakeProcessStream(srv)
		respHdr := stream.step(t, makeHeadersReq("POST", "/items", "application/json", false))
		if respHdr.GetRequestHeaders() == nil {
			t.Fatalf("expected RequestHeaders response, got %v", respHdr)
		}

		go func(st *fakeProcessStream) {
			defer bodyWG.Done()
			readyWG.Done()
			<-startBarrier

			respBody := st.step(t, makeBodyReq([]byte(`{"id":1}`), true))
			if imm := respBody.GetImmediateResponse(); imm != nil {
				if imm.GetStatus().GetCode() != 409 {
					t.Errorf("duplicate status = %d, want 409", imm.GetStatus().GetCode())
				}
				conflicts.Add(1)
				st.finish(t)
				return
			}
			if respBody.GetRequestBody() == nil {
				t.Errorf("expected RequestBody continue or ImmediateResponse, got %v", respBody)
				st.finish(t)
				return
			}
			winners.Add(1)
			winnerCh <- st
		}(stream)
	}

	readyWG.Wait()
	close(startBarrier)

	// Wait for all concurrent body steps to resolve before inspecting winner/conflict counts.
	bodyWG.Wait()
	close(winnerCh)

	if got := winners.Load(); got != 1 {
		t.Fatalf("concurrent winners = %d, want 1 (conflicts=%d)", got, conflicts.Load())
	}
	if got := conflicts.Load(); got != workers-1 {
		t.Fatalf("concurrent conflicts = %d, want %d", got, workers-1)
	}

	winner := <-winnerCh

	// Winner enters ResponseHeaders (with response body still to follow), which
	// releases winner's in-flight digest slot.
	resp1 := winner.step(t, makeRespHeadersReq("application/json", false))
	if resp1.GetResponseHeaders() == nil {
		t.Fatalf("expected ResponseHeaders response, got %v", resp1)
	}

	// Second identical request now acquires the digest slot cleanly while
	// winner #1 has not yet processed ResponseBody.
	winner2 := newFakeProcessStream(srv)
	if hdrResp := winner2.step(t, makeHeadersReq("POST", "/items", "application/json", false)); hdrResp.GetRequestHeaders() == nil {
		t.Fatalf("expected RequestHeaders response, got %v", hdrResp)
	}
	bodyResp2 := winner2.step(t, makeBodyReq([]byte(`{"id":1}`), true))
	if bodyResp2.GetImmediateResponse() != nil || bodyResp2.GetRequestBody() == nil {
		t.Fatalf("expected second request to acquire released digest, got %v", bodyResp2)
	}

	// Winner #1 now finishes ResponseBody; it must NOT release winner2's slot.
	resp2 := winner.step(t, makeRespBodyReq([]byte(`{"saved":true}`), true))
	if resp2.GetResponseBody() == nil {
		t.Fatalf("expected ResponseBody response, got %v", resp2)
	}
	winner.finish(t)

	// A concurrent request while winner2 is still in flight must still get 409.
	conflictStream := newFakeProcessStream(srv)
	_ = conflictStream.step(t, makeHeadersReq("POST", "/items", "application/json", false))
	conflictResp := conflictStream.step(t, makeBodyReq([]byte(`{"id":1}`), true))
	if imm := conflictResp.GetImmediateResponse(); imm == nil || imm.GetStatus().GetCode() != 409 {
		t.Fatalf("expected 409 while winner2 still holds slot, got %v", conflictResp)
	}
	conflictStream.finish(t)

	// Finish winner2 and verify a subsequent identical request succeeds again.
	_ = winner2.step(t, makeRespHeadersReq("application/json", true))
	winner2.finish(t)

	after := newFakeProcessStream(srv)
	_ = after.step(t, makeHeadersReq("POST", "/items", "application/json", false))
	afterBody := after.step(t, makeBodyReq([]byte(`{"id":1}`), true))
	if afterBody.GetImmediateResponse() != nil || afterBody.GetRequestBody() == nil {
		t.Fatalf("expected post-release request to succeed, got %v", afterBody)
	}
	_ = after.step(t, makeRespHeadersReq("application/json", true))
	after.finish(t)
}

func TestDedupReleaseOnStreamCancellationAndDuplicateCancelSafety(t *testing.T) {
	srv := newExampleTestServer(t, &dedupRequestProcessor{})

	// Owner stream acquires a bodyless POST digest on RequestHeaders(EndOfStream=true).
	owner := newFakeProcessStream(srv)
	ownerResp := owner.step(t, makeHeadersReq("POST", "/orders", "", true))
	if ownerResp.GetImmediateResponse() != nil || ownerResp.GetRequestHeaders() == nil {
		t.Fatalf("expected owner to acquire digest, got %v", ownerResp)
	}

	// Duplicate stream gets 409 and then cancels; this must NOT unlock owner's slot.
	dup := newFakeProcessStream(srv)
	dupResp := dup.step(t, makeHeadersReq("POST", "/orders", "", true))
	if imm := dupResp.GetImmediateResponse(); imm == nil || imm.GetStatus().GetCode() != 409 {
		t.Fatalf("expected 409 duplicate rejection, got %v", dupResp)
	}
	dup.cancelWithErr(t, status.Error(codes.Canceled, "client disconnected"))

	// Still locked while owner is alive.
	probe := newFakeProcessStream(srv)
	probeResp := probe.step(t, makeHeadersReq("POST", "/orders", "", true))
	if imm := probeResp.GetImmediateResponse(); imm == nil || imm.GetStatus().GetCode() != 409 {
		t.Fatalf("expected 409 after duplicate cancellation, got %v", probeResp)
	}
	probe.finish(t)

	// Now owner cancels before response headers; ErrorHandler must release its slot.
	owner.cancelWithErr(t, status.Error(codes.Canceled, "downstream went away"))

	// A new identical request should now acquire the released digest.
	next := newFakeProcessStream(srv)
	nextResp := next.step(t, makeHeadersReq("POST", "/orders", "", true))
	if nextResp.GetImmediateResponse() != nil || nextResp.GetRequestHeaders() == nil {
		t.Fatalf("expected digest to be unlocked after owner cancel, got %v", nextResp)
	}
	_ = next.step(t, makeRespHeadersReq("", true))
	next.finish(t)
}

func TestDedupAllowsConcurrentReadOnlyMethods(t *testing.T) {
	srv := newExampleTestServer(t, &dedupRequestProcessor{})

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			st := newFakeProcessStream(srv)
			hdrResp := st.step(t, makeHeadersReq("GET", "/items", "", true))
			if hdrResp.GetImmediateResponse() != nil || hdrResp.GetRequestHeaders() == nil {
				t.Errorf("GET request unexpectedly rejected: %v", hdrResp)
				st.finish(t)
				return
			}
			_ = st.step(t, makeRespHeadersReq("application/json", true))
			st.finish(t)
		}()
	}
	wg.Wait()
}

func TestAllExampleProcessorsConcurrentRace(t *testing.T) {
	runRoundTrip := func(t *testing.T, srv *ep.GenericExtProcServer, path string) {
		t.Helper()
		st := newFakeProcessStream(srv)

		reqHdrResp := st.step(t, makeHeadersReq("POST", path, "application/json", false))
		if reqHdrResp.GetImmediateResponse() != nil {
			st.finish(t)
			return
		}

		reqBodyResp := st.step(t, makeBodyReq([]byte(`{"maskme":"secret","upstream":"upstream-copy"}`), true))
		if reqBodyResp.GetImmediateResponse() != nil {
			st.finish(t)
			return
		}

		respHdrResp := st.step(t, makeRespHeadersReq("application/json", false))
		if respHdrResp.GetImmediateResponse() != nil {
			st.finish(t)
			return
		}

		_ = st.step(t, makeRespBodyReq([]byte(`{"ok":true}`), true))
		st.finish(t)
	}

	cases := []struct {
		name string
		new  func() processor
		path string
	}{
		{name: "noop", new: func() processor { return &noopRequestProcessor{} }, path: "/"},
		{name: "trivial", new: func() processor { return &trivialRequestProcessor{} }, path: "/"},
		{name: "timer", new: func() processor { return &timerRequestProcessor{} }, path: "/"},
		{name: "data", new: func() processor { return &dataRequestProcessor{} }, path: "/"},
		{name: "digest", new: func() processor { return &digestRequestProcessor{} }, path: "/"},
		{name: "dedup", new: func() processor { return &dedupRequestProcessor{} }, path: "/"},
		{name: "masker", new: func() processor { return &maskerRequestProcessor{} }, path: "/"},
		{name: "echo", new: func() processor { return &echoRequestProcessor{} }, path: "/echo/test"},
		{name: "router", new: func() processor { return &routingRequestProcessor{} }, path: "/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newExampleTestServer(t, tc.new())
			const workers = 8
			var wg sync.WaitGroup
			wg.Add(workers)
			for range workers {
				go func() {
					defer wg.Done()
					runRoundTrip(t, srv, tc.path)
				}()
			}
			wg.Wait()
		})
	}
}
