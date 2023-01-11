package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	hpb "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	port = flag.String("port", "50051", "port")
)

func main() {

	flag.Parse()

	lis, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(1000)}
	s := grpc.NewServer(sopts...)

	extproc := &genericExtProcServer{
		name:      "trivial",
		processor: &trivialRequestProcessor{},
	}
	epb.RegisterExternalProcessorServer(s, extproc)
	hpb.RegisterHealthServer(s, &healthServer{})

	log.Printf("Starting extproc server on port %s\n", *port)

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

// func run() error {
//     listenOn := "0.0.0.0:50051"
//     listener, err := net.Listen("tcp", listenOn)
//     if err != nil {
//         return fmt.Errorf("failed to listen on %s: %w", listenOn, err)
//     }

//     server := grpc.NewServer()
//     petv1.RegisterPetStoreServiceServer(server, &petStoreServiceServer{})
//     log.Println("Listening on", listenOn)
//     if err := server.Serve(listener); err != nil {
//         return fmt.Errorf("failed to serve gRPC server: %w", err)
//     }

//     return nil
// }

// func main() {
//     if err := run(); err != nil {
//         log.Fatal(err)
//     }
// }
