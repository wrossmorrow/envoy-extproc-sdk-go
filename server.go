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

func Serve(port int, processor RequestProcessor) {
	if processor == nil {
		log.Fatalf("cannot process request stream without `processor`")
	}

	lis, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(1000)}
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

	timeout := int64(5) // 5 seconds default timeout
	timeoutStr := os.Getenv("EXTPROC_GRACEFUL_SHUTDOWN_TIMEOUT_SECONDS")
	if len(timeoutStr) > 0 {
		val, err := strconv.ParseInt(timeoutStr, 10, 64)
		if err == nil {
			timeout = val
		} else {
			log.Printf("Unable to parse timeout %s, using default %d seconds\n", timeoutStr, timeout)
		}
	}

	log.Printf("Starting ExtProc(%s) on port %d\n", name, port)

	go s.Serve(lis)

	gracefulStop := make(chan os.Signal, timeout)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	sig := <-gracefulStop
	log.Printf("caught sig: %+v", sig)
	log.Printf("Wait for %d seconds to finish processing\n", timeout)
	lis.Close()

	time.Sleep(time.Duration(timeout) * time.Second)
}
