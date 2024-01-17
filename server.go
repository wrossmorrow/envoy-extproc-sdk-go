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

	var gracefulStop = make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	go func() {
		sig := <-gracefulStop
		log.Printf("caught sig: %+v", sig)
		log.Println("Wait for 1 second to finish processing")
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
	s.Serve(lis)

}
