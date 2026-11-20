package conformance

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	extproc "github.com/wrossmorrow/envoy-extproc-sdk-go"
	"google.golang.org/grpc"
	hpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	// CaseHeader routes a conformance request to its registered CaseHandler.
	CaseHeader     = "x-conformance-case"
	caseContextKey = "conformance.case_id"
)

// CaseHandler defines optional per-phase callbacks for a single conformance
// test case. Nil callbacks default to ctx.ContinueRequest().
type CaseHandler struct {
	RequestHeaders   func(ctx *extproc.RequestContext, headers extproc.AllHeaders) error
	RequestBody      func(ctx *extproc.RequestContext, body []byte) error
	RequestTrailers  func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error
	ResponseHeaders  func(ctx *extproc.RequestContext, headers extproc.AllHeaders) error
	ResponseBody     func(ctx *extproc.RequestContext, body []byte) error
	ResponseTrailers func(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error
	OnError          func(ctx *extproc.RequestContext, phase int, err error)
}

// ConformanceProcessor dispatches phase callbacks by the CaseHeader value so
// individual tests control exact ext_proc behavior without coupling to request
// bodies.
type ConformanceProcessor struct {
	opts  *extproc.ProcessingOptions
	mu    sync.RWMutex
	cases map[string]CaseHandler
}

// NewConformanceProcessor creates a ConformanceProcessor with the supplied options.
func NewConformanceProcessor(opts *extproc.ProcessingOptions) *ConformanceProcessor {
	if opts == nil {
		opts = extproc.NewDefaultProcessingOptions()
	}
	return &ConformanceProcessor{
		opts:  opts,
		cases: make(map[string]CaseHandler),
	}
}

// Register binds a CaseHandler to id.
func (p *ConformanceProcessor) Register(id string, handler CaseHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cases[id] = handler
}

// Unregister removes any CaseHandler registered for id.
func (p *ConformanceProcessor) Unregister(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cases, id)
}

func (p *ConformanceProcessor) lookupByID(id string) (CaseHandler, bool) {
	if id == "" {
		return CaseHandler{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	h, ok := p.cases[id]
	return h, ok
}

func (p *ConformanceProcessor) lookup(ctx *extproc.RequestContext) (CaseHandler, bool) {
	if ctx == nil {
		return CaseHandler{}, false
	}
	v, err := ctx.GetValue(caseContextKey)
	if err != nil {
		return CaseHandler{}, false
	}
	id, ok := v.(string)
	if !ok {
		return CaseHandler{}, false
	}
	return p.lookupByID(id)
}

func (p *ConformanceProcessor) GetName() string {
	return "conformance"
}

func (p *ConformanceProcessor) GetOptions() *extproc.ProcessingOptions {
	return p.opts
}

func (p *ConformanceProcessor) ProcessRequestHeaders(ctx *extproc.RequestContext, headers extproc.AllHeaders) error {
	if id, ok := headers.Get(CaseHeader); ok && id != "" {
		_ = ctx.SetValue(caseContextKey, id)
		if h, found := p.lookupByID(id); found && h.RequestHeaders != nil {
			return h.RequestHeaders(ctx, headers)
		}
	}
	return ctx.ContinueRequest()
}

func (p *ConformanceProcessor) ProcessRequestBody(ctx *extproc.RequestContext, body []byte) error {
	if h, ok := p.lookup(ctx); ok && h.RequestBody != nil {
		return h.RequestBody(ctx, body)
	}
	return ctx.ContinueRequest()
}

func (p *ConformanceProcessor) ProcessRequestTrailers(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
	if h, ok := p.lookup(ctx); ok && h.RequestTrailers != nil {
		return h.RequestTrailers(ctx, trailers)
	}
	return ctx.ContinueRequest()
}

func (p *ConformanceProcessor) ProcessResponseHeaders(ctx *extproc.RequestContext, headers extproc.AllHeaders) error {
	// Under request_header_mode: SKIP, ProcessRequestHeaders never ran; resolve
	// the case ID echoed back by the upstream in the response headers.
	if _, err := ctx.GetValue(caseContextKey); err != nil {
		if id, ok := headers.Get(CaseHeader); ok && id != "" {
			_ = ctx.SetValue(caseContextKey, id)
		}
	}
	if h, ok := p.lookup(ctx); ok && h.ResponseHeaders != nil {
		return h.ResponseHeaders(ctx, headers)
	}
	return ctx.ContinueRequest()
}

func (p *ConformanceProcessor) ProcessResponseBody(ctx *extproc.RequestContext, body []byte) error {
	if h, ok := p.lookup(ctx); ok && h.ResponseBody != nil {
		return h.ResponseBody(ctx, body)
	}
	return ctx.ContinueRequest()
}

func (p *ConformanceProcessor) ProcessResponseTrailers(ctx *extproc.RequestContext, trailers extproc.AllHeaders) error {
	if h, ok := p.lookup(ctx); ok && h.ResponseTrailers != nil {
		return h.ResponseTrailers(ctx, trailers)
	}
	return ctx.ContinueRequest()
}

func (p *ConformanceProcessor) ErrorHandler(ctx *extproc.RequestContext, phase int, err error) {
	if h, ok := p.lookup(ctx); ok && h.OnError != nil {
		h.OnError(ctx, phase, err)
	}
}

func (p *ConformanceProcessor) Close(gracePeriodSeconds int32) error {
	return nil
}

// ConformanceExtProcServer hosts the SDK's GenericExtProcServer in-process on
// an ephemeral TCP port for loopback and containerized Envoy testing.
type ConformanceExtProcServer struct {
	Processor *ConformanceProcessor
	Metrics   *extproc.Metrics
	Server    *extproc.GenericExtProcServer
	grpcSrv   *grpc.Server
	listener  net.Listener
	port      int
}

// StartConformanceExtProcServer launches an in-process ext_proc gRPC server on
// 0.0.0.0:0.
func StartConformanceExtProcServer(opts *extproc.ProcessingOptions, logger *slog.Logger) (*ConformanceExtProcServer, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	lis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("listen extproc server: %w", err)
	}

	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		_ = lis.Close()
		return nil, fmt.Errorf("unexpected listener address %T", lis.Addr())
	}

	proc := NewConformanceProcessor(opts)
	metrics := extproc.NewEmptyMetrics()
	sdkSrv, err := extproc.NewGenericExtProcServer(proc, metrics, logger)
	if err != nil {
		_ = lis.Close()
		return nil, err
	}

	grpcSrv := grpc.NewServer()
	epb.RegisterExternalProcessorServer(grpcSrv, sdkSrv)
	hpb.RegisterHealthServer(grpcSrv, extproc.NewReadyHealthServer())

	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	return &ConformanceExtProcServer{
		Processor: proc,
		Metrics:   metrics,
		Server:    sdkSrv,
		grpcSrv:   grpcSrv,
		listener:  lis,
		port:      tcpAddr.Port,
	}, nil
}

// Port returns the ephemeral TCP port the gRPC server is listening on.
func (s *ConformanceExtProcServer) Port() int {
	return s.port
}

// Addr returns the loopback host:port address for direct gRPC connections.
func (s *ConformanceExtProcServer) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.port)
}

// Close stops the gRPC server.
func (s *ConformanceExtProcServer) Close() {
	s.grpcSrv.Stop()
}
