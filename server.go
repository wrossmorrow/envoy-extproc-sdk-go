package extproc

import (
	"log"
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
	ServeWithOptions(port, DefaultServerOptions(), processor)
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
func ServeWithOptions(port int, serverOpts ExtProcServerOptions, processor RequestProcessor) {
	if processor == nil {
		log.Fatalf("cannot process request stream without `processor`")
	}

	lis, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
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
	}
	epb.RegisterExternalProcessorServer(s, extproc)
	hpb.RegisterHealthServer(s, &HealthServer{})

	log.Printf("Starting ExtProc(%s) on port %d\n", name, port)

	go s.Serve(lis)

	gracefulStop := make(chan os.Signal, serverOpts.GracefulShutdownTimeout)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	sig := <-gracefulStop
	log.Printf("caught sig: %+v", sig)
	log.Printf("Wait for %d seconds to finish processing\n", serverOpts.GracefulShutdownTimeout)
	lis.Close()

	time.Sleep(time.Duration(serverOpts.GracefulShutdownTimeout) * time.Second)
}
