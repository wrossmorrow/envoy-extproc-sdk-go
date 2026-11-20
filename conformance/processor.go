package conformance

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
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
	name  string
	opts  *extproc.ProcessingOptions
	mu    sync.RWMutex
	cases map[string]CaseHandler
}

// NewConformanceProcessor creates a ConformanceProcessor named "conformance"
// with the supplied options.
func NewConformanceProcessor(opts *extproc.ProcessingOptions) *ConformanceProcessor {
	return NewConformanceProcessorWithName("conformance", opts)
}

// NewConformanceProcessorWithName creates a ConformanceProcessor with an
// explicit processor name and options.
func NewConformanceProcessorWithName(name string, opts *extproc.ProcessingOptions) *ConformanceProcessor {
	if name == "" {
		name = "conformance"
	}
	if opts == nil {
		opts = extproc.NewDefaultProcessingOptions()
	}
	return &ConformanceProcessor{
		name:  name,
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
	return p.name
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
	registry  *prometheus.Registry
	grpcSrv   *grpc.Server
	listener  net.Listener
	port      int
}

// StartConformanceExtProcServer launches an in-process ext_proc gRPC server
// named "conformance" on 0.0.0.0:0.
func StartConformanceExtProcServer(opts *extproc.ProcessingOptions, logger *slog.Logger) (*ConformanceExtProcServer, error) {
	return StartConformanceExtProcServerWithName("conformance", opts, logger)
}

// StartConformanceExtProcServerWithName launches an in-process ext_proc gRPC
// server with the given processor name on 0.0.0.0:0.
func StartConformanceExtProcServerWithName(name string, opts *extproc.ProcessingOptions, logger *slog.Logger) (*ConformanceExtProcServer, error) {
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

	proc := NewConformanceProcessorWithName(name, opts)
	registry := prometheus.NewRegistry()
	metrics := extproc.NewEmptyMetrics().Register(registry)
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
		registry:  registry,
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

// CounterValue reads the current scalar value of a registered Prometheus counter.
func (s *ConformanceExtProcServer) CounterValue(metricName string) (float64, error) {
	families, err := s.registry.Gather()
	if err != nil {
		return 0, fmt.Errorf("gather metrics: %w", err)
	}
	for _, fam := range families {
		if fam.GetName() != metricName {
			continue
		}
		ms := fam.GetMetric()
		if len(ms) == 0 || ms[0].GetCounter() == nil {
			return 0, fmt.Errorf("metric %q has no counter value", metricName)
		}
		return ms[0].GetCounter().GetValue(), nil
	}
	return 0, fmt.Errorf("metric %q not found", metricName)
}

// GaugeValue reads the current scalar value of a registered Prometheus gauge.
func (s *ConformanceExtProcServer) GaugeValue(metricName string) (float64, error) {
	families, err := s.registry.Gather()
	if err != nil {
		return 0, fmt.Errorf("gather metrics: %w", err)
	}
	for _, fam := range families {
		if fam.GetName() != metricName {
			continue
		}
		ms := fam.GetMetric()
		if len(ms) == 0 || ms[0].GetGauge() == nil {
			return 0, fmt.Errorf("metric %q has no gauge value", metricName)
		}
		return ms[0].GetGauge().GetValue(), nil
	}
	return 0, fmt.Errorf("metric %q not found", metricName)
}

// Close stops the gRPC server.
func (s *ConformanceExtProcServer) Close() {
	s.grpcSrv.Stop()
}
