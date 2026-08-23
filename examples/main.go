package main

import (
	"flag"
	"log"
	"log/slog"
	"os"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go/src"
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

func parseArgs(args []string) (sopts *ep.ServerOptions, popts *ep.ProcessingOptions, nonFlagArgs []string) {
	popts = ep.NewDefaultProcessingOptions()
	sopts = ep.NewDefaultServerOptions()

	rootCmd := flag.NewFlagSet("root", flag.ExitOnError)
	port := rootCmd.Int("port", 50051, "the gRPC port.")
	sopts.TerminationGracePeriodSeconds = 1

	rootCmd.BoolVar(&popts.UpdateExtProcHeader, "update-extproc-header", false, "update the extProc header or not.")
	rootCmd.BoolVar(&popts.UpdateDurationHeader, "update-duration-header", false, "update the duration header or not.")

	rootCmd.Parse(args)
	sopts.ExtProcPort = *port
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

	sopts, popts, nonFlagArgs := parseArgs(os.Args[2:])
	if err := proc.Init(popts, nonFlagArgs); err != nil {
		log.Fatalf("Initialize the processor is failed: %v.", err.Error())
	}
	defer proc.Finish()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ep.Serve(sopts, proc, logger)
}
