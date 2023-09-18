package main

import (
	"flag"
	"log"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var (
	port = *flag.Int("port", 50051, "gRPC port (default: 50051)")
)

var processors = map[string]ep.RequestProcessor{
	"noop":    noopRequestProcessor{},
	"trivial": trivialRequestProcessor{},
	"timer":   timerRequestProcessor{},
	"data":    dataRequestProcessor{},
	"digest":  digestRequestProcessor{},
	"dedup":   dedupRequestProcessor{},
	"masker":  maskerRequestProcessor{},
	"echo":    echoRequestProcessor{},
}

func main() {

	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		log.Fatal("Passing a processor is required.")

	} else if len(args) > 1 {
		log.Fatal("Only a single processor can be served at once.")

	} else {
		_, exists := processors[args[0]]
		if !exists {
			log.Fatalf("Processor \"%s\" not defined.", args[0])
		}
	}

	ep.Serve(port, processors[args[0]])
}
