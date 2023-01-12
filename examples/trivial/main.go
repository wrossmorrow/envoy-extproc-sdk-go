package main

import (
	"flag"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

var (
	port = *flag.Int("port", 50051, "gRPC port (default: 50051)")
)

func main() {
	flag.Parse()

	eps := make(map[string]ep.RequestProcessor)
	eps["trivial"] = trivialRequestProcessor{}
	ep.Serve(port, eps)
}
