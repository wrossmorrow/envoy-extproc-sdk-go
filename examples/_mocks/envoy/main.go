package main

import (
	"context"
	"flag"
	"io"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

var serverAddr = flag.String("addr", "0.0.0.0:50051", "The server address in the format of host:port (default: 0.0.0.0:50051)")

func (req *envoyStream) processRequest(client extprocv3.ExternalProcessorClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := client.Process(ctx)
	if err != nil {
		log.Fatalf("extprocv3.ExternalProcessorClient.Process failed: %v", err)
	}
	waitc := make(chan struct{})

	// does this really "chat"? ie, wait for response before sending another
	// message (or the next message in the "stack")

	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				// read done.
				close(waitc)
				return
			}
			if err != nil {
				log.Fatalf("Failed to recieve extprocv3.ProcessingResponse: %v", err)
			}
			log.Printf("Got extprocv3.ProcessingResponse %v", resp)
		}
	}()

	for _, phase := range req.phases {
		log.Printf("Sending extprocv3.ProcessingRequest %v", phase)
		if err := stream.Send(&phase); err != nil {
			log.Fatalf("Failed to send extprocv3.ProcessRequest: %v", err)
		}
	}

	stream.CloseSend()
	<-waitc

}

func main() {
	flag.Parse()

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	conn, err := grpc.Dial(*serverAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()

	client := extprocv3.NewExternalProcessorClient(conn)

	for _, cfg := range config().Requests {
		newEnvoyStream(cfg.Request, cfg.Response).processRequest(client)
	}

}
