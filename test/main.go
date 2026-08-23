package main

import (
	"flag"
	"log/slog"
	"os"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go"
)

func parseArgs(args []string) (sopts *ep.ServerOptions, popts *ep.ProcessingOptions) {
	popts = ep.NewDefaultProcessingOptions()
	sopts = ep.NewDefaultServerOptions()

	rootCmd := flag.NewFlagSet("root", flag.ExitOnError)
	port := rootCmd.Int("port", 50051, "the gRPC port.")
	terminationGracePeriodSeconds := rootCmd.Int("terminationGracePeriodSeconds", 15, "grade period for shutdown.")

	rootCmd.BoolVar(&popts.UpdateExtProcHeader, "update-extproc-header", false, "update the extProc header or not.")
	rootCmd.BoolVar(&popts.UpdateDurationHeader, "update-duration-header", false, "update the duration header or not.")

	rootCmd.Parse(args)

	sopts.ExtProcPort = *port
	sopts.TerminationGracePeriodSeconds = int32(*terminationGracePeriodSeconds)

	return
}

func main() {
	sopts, popts := parseArgs(os.Args[1:])

	proc := &testingRequestProcessor{
		opts: popts,
	}

	logger := slog.New(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)

	ep.Serve(sopts, proc, logger)
}
