package extproc

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	hpb "google.golang.org/grpc/health/grpc_health_v1"
)

func Serve(serverOptions *ServerOptions, processor RequestProcessor, logger *slog.Logger) error {
	if processor == nil {
		logger.Error("cannot process request stream without `processor`")
		return fmt.Errorf("processor is nil")
	}

	// setup metrics server

	registry := prometheus.NewRegistry()
	metrics := NewEmptyMetrics().Register(registry)
	go func() {
		http.Handle("/metrics", promhttp.HandlerFor(
			registry,
			promhttp.HandlerOpts{
				EnableOpenMetrics: true,
			},
		))
		logger.Info("Started metrics HTTP server", slog.Int("port", serverOptions.MetricsHTTPPort))
		if err := http.ListenAndServe(":"+strconv.Itoa(serverOptions.MetricsHTTPPort), nil); err != nil {
			logger.Error("Failed to start metrics HTTP server", slog.String("error", err.Error()))
		}
	}()

	// setup and register the extproc server

	lis, err := net.Listen("tcp", ":"+strconv.Itoa(serverOptions.ExtProcPort))
	if err != nil {
		logger.Error("Failed to listen", slog.Int("port", serverOptions.ExtProcPort), slog.String("error", err.Error()))
		return err
	}

	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(serverOptions.MaxConcurrentStreams)}
	s := grpc.NewServer(sopts...)

	name := processor.GetName()
	opts := processor.GetOptions()
	extproc := &GenericExtProcServer{
		name:      name,
		processor: processor,
		options:   opts,
		metrics:   metrics,
		logger:    logger,
	}
	epb.RegisterExternalProcessorServer(s, extproc)

	health := NewReadyHealthServer()
	hpb.RegisterHealthServer(s, health)

	logger.Info("Starting extproc gRPC Server", slog.String("name", name), slog.Int("port", serverOptions.ExtProcPort))

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(lis) }()

	gracefulStop := make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)

	grace := time.Duration(serverOptions.TerminationGracePeriodSeconds) * time.Second

	select {
	case err := <-serveErr:
		logger.Error("gRPC server exited unexpectedly", slog.String("error", err.Error()))
		return err

	case sig := <-gracefulStop:

		logger.Info("Caught signal, waiting to finish processing",
			slog.String("signal", sig.String()),
			slog.Duration("grace_period", grace),
			slog.Int("grace_period_seconds", int(serverOptions.TerminationGracePeriodSeconds)))

		health.MarkUnready()

		time.Sleep(time.Duration(serverOptions.UnreadyPropagationDelaySeconds) * time.Second)

		// 3. GOAWAY + wait for in-flight streams, bounded by the grace period
		stopped := make(chan struct{})
		go func() {
			s.GracefulStop() // closes lis for us; do NOT call lis.Close()
			close(stopped)
		}()

		select {
		case <-stopped:
			logger.Info("all active streams drained")
		case <-time.After(grace):
			logger.Warn("grace period elapsed, forcing stop")
			s.Stop() // NOTE: aborts remaining streams
			<-stopped
		}

		if err := extproc.Close(serverOptions.TerminationGracePeriodSeconds); err != nil {
			logger.Error("processor close error", slog.String("error", err.Error()))
			return err
		}
		return nil
	}
}

func MustServe(serverOptions *ServerOptions, processor RequestProcessor, logger *slog.Logger) {
	if err := Serve(serverOptions, processor, logger); err != nil {
		os.Exit(1)
	}
}
