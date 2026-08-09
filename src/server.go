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
	prom "github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	hpb "google.golang.org/grpc/health/grpc_health_v1"
)

func Serve(options *ServerOptions, processor RequestProcessor, logger *slog.Logger) error {
	if processor == nil {
		logger.Error("cannot process request stream without `processor`")
		return fmt.Errorf("processor is nil")
	}

	// setup metrics server

	metrics := NewEmptyMetrics().Register()
	go func() {
		http.Handle("/metrics", prom.HandlerFor(
			prometheus.DefaultGatherer,
			promhttp.HandlerOpts{
				EnableOpenMetrics: true,
			},
		))
		logger.Info("Started metrics HTTP server", slog.Int("port", options.MetricsHTTPPort))
		if err := http.ListenAndServe(":"+strconv.Itoa(options.MetricsHTTPPort), nil); err != nil {
			logger.Error("Failed to start metrics HTTP server", slog.String("error", err.Error()))
		}
	}()

	// setup and register the extproc server

	lis, err := net.Listen("tcp", ":"+strconv.Itoa(options.ExtProcPort))
	if err != nil {
		logger.Error("Failed to listen", slog.Int("port", options.ExtProcPort), slog.String("error", err.Error()))
		return err
	}

	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(options.MaxConcurrentStreams)}
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
	hpb.RegisterHealthServer(s, &HealthServer{})

	logger.Info("Starting extproc gRPC Server", slog.String("name", name), slog.Int("port", options.ExtProcPort))

	go s.Serve(lis)

	// handle graceful shutdown

	gracefulStop := make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	sig := <-gracefulStop
	logger.Info("Caught signal, waiting to finish processing", slog.String("signal", sig.String()), slog.Int("grace_period_seconds", options.TerminationGracePeriodSeconds))
	lis.Close()

	time.Sleep(time.Duration(options.TerminationGracePeriodSeconds) * time.Second)
	return nil
}

func MustServe(options *ServerOptions, processor RequestProcessor, logger *slog.Logger) {
	if err := Serve(options, processor, logger); err != nil {
		os.Exit(1)
	}
}
