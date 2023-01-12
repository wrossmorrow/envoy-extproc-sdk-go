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

func Serve(port int, processors map[string]RequestProcessor) {

	lis, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(1000)}
	s := grpc.NewServer(sopts...)

	for n, p := range processors {
		log.Printf("Registering ExtProc \"%s\"\n", n)
		extproc := &GenericExtProcServer{
			name:      n,
			processor: &p,
		}
		epb.RegisterExternalProcessorServer(s, extproc)
	}
	hpb.RegisterHealthServer(s, &HealthServer{})

	log.Printf("Starting ExtProc server on port %d\n", port)

	var gracefulStop = make(chan os.Signal)
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
