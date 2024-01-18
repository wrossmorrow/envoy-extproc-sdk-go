package main

import (
	"flag"
	"log"
	"os"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

type processor interface {
	Init(opts *ep.ProcessingOptions, nonFlagArgs []string) error
	Finish()

	ep.RequestProcessor
}

var processors = map[string]processor{
	"noop":    &noopRequestProcessor{},
	"trivial": &trivialRequestProcessor{},
	"timer":   &timerRequestProcessor{},
	"data":    &dataRequestProcessor{},
	"digest":  &digestRequestProcessor{},
	"dedup":   &dedupRequestProcessor{},
	"masker":  &maskerRequestProcessor{},
	"echo":    &echoRequestProcessor{},
}

func parseArgs(args []string) (port *int, opts *ep.ProcessingOptions, nonFlagArgs []string) {
	rootCmd := flag.NewFlagSet("root", flag.ExitOnError)
	port = rootCmd.Int("port", 50051, "the gRPC port.")

	opts = ep.NewDefaultOptions()

	rootCmd.BoolVar(&opts.LogStream, "log-stream", false, "log the stream or not.")
	rootCmd.BoolVar(&opts.LogPhases, "log-phases", false, "log the phases or not.")
	rootCmd.BoolVar(&opts.UpdateExtProcHeader, "update-extproc-header", false, "update the extProc header or not.")
	rootCmd.BoolVar(&opts.UpdateDurationHeader, "update-duration-header", false, "update the duration header or not.")

	rootCmd.Parse(args)
	nonFlagArgs = rootCmd.Args()
	return
}

func main() {

	// cmd subCmd arg, arg2,...
	args := os.Args
	if len(args) < 2 {
		log.Fatal("Passing a processor is required.")
	}

	cmd := args[1]
	proc, exists := processors[cmd]
	if !exists {
		log.Fatalf("Processor \"%s\" not defined.", cmd)
	}

	port, opts, nonFlagArgs := parseArgs(os.Args[2:])
	if err := proc.Init(opts, nonFlagArgs); err != nil {
		log.Fatalf("Initialize the processor is failed: %v.", err.Error())
	}
	defer proc.Finish()

	ep.Serve(*port, proc)
}
