package extproc

import (
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"

	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	hpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

var defaultLogger = slog.New(
	slog.NewJSONHandler(
		os.Stdout,
		&slog.HandlerOptions{
			Level:     getLogLevelFromEnv(),
			AddSource: true, // file and line numbers
		},
	),
)

// Supported gRPC service options in the Serve* helpers.
type ExtProcServerOptions struct {
	GracefulShutdownTimeout int
	MaxConcurrentStreams    uint32
}

// Default gRPC service options in the Serve* helpers.
func DefaultServerOptions() ExtProcServerOptions {
	return ExtProcServerOptions{
		GracefulShutdownTimeout: 15,
		MaxConcurrentStreams:    100,
	}
}

// Wrapper for running gRPC ExternalProcessor service with a given RequestProcessor
// implementation. Includes the standard gRPC Health service as well as reflection.
//
// Uses a default 15s shutdown timeout. It is up to the caller to execute shutdown
// behaviors after this shutdown completes, likely using defer processor.Finish()
//
// Using this wrapper is not required, users can run their own gRPC server implementation
// with this SDK.
func Serve(port int, processor RequestProcessor) {
	ServeWithOptions(port, DefaultServerOptions(), processor, defaultLogger)
}

func ServeWithLogger(port int, processor RequestProcessor, logger *slog.Logger) {
	ServeWithOptions(port, DefaultServerOptions(), processor, logger)
}

// Wrapper for running gRPC ExternalProcessor service with a given RequestProcessor
// implementation, with a declared shutdown timeout. It is still up to the caller to
// execute shutdown behaviors after this shutdown completes, likely using defer. Note
// that any deferred actions to "finalize" processing occur _after_ the server shutdown
// so plan accordingly. The reason for this is we should probably expect to need to
// drain existing streams _before_ any finalization of actions taken in external processing.
//
// Using this wrapper is not required, users can run their own gRPC server implementation
// with this SDK.
func ServeWithOptions(port int, serverOpts ExtProcServerOptions, processor RequestProcessor, logger *slog.Logger) {
	if processor == nil {
		logger.Error("Cannot process request stream without `processor`")
		os.Exit(1)
	}

	lis, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		logger.Error("Failed to listen", slog.Any("error", err))
		os.Exit(1)
	}

	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(serverOpts.MaxConcurrentStreams)}
	s := grpc.NewServer(sopts...)
	reflection.Register(s)

	name := processor.GetName()
	opts := processor.GetOptions() // TODO: figure out command line overrides
	extproc := &GenericExtProcServer{
		name:      name,
		processor: processor,
		options:   opts,
		logger:    logger,
	}
	epb.RegisterExternalProcessorServer(s, extproc)
	hpb.RegisterHealthServer(s, &HealthServer{})

	logger.Info("Starting ExtProc", slog.Any("name", name), slog.Any("port", port))

	go s.Serve(lis)

	gracefulStop := make(chan os.Signal, serverOpts.GracefulShutdownTimeout)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	sig := <-gracefulStop
	logger.Warn("Caught signal", slog.Any("signal", sig))
	logger.Info("Waiting finish processing\n", slog.Any("delay", serverOpts.GracefulShutdownTimeout))
	lis.Close()

	time.Sleep(time.Duration(serverOpts.GracefulShutdownTimeout) * time.Second)
}
