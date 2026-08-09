package main

import (
	"flag"
	"log/slog"
	"os"

	ep "github.com/wrossmorrow/envoy-extproc-sdk-go/src"
)

func parseArgs(args []string) (sopts *ep.ServerOptions, popts *ep.ProcessingOptions) {
	popts = ep.NewDefaultOptions()
	sopts = ep.NewDefaultServerOptions()

	rootCmd := flag.NewFlagSet("root", flag.ExitOnError)
	port := rootCmd.Int("port", 50051, "the gRPC port.")
	sopts.ExtProcPort = *port
	sopts.TerminationGracePeriodSeconds = 1

	rootCmd.BoolVar(&popts.UpdateExtProcHeader, "update-extproc-header", false, "update the extProc header or not.")
	rootCmd.BoolVar(&popts.UpdateDurationHeader, "update-duration-header", false, "update the duration header or not.")

	rootCmd.Parse(args)
	return
}

func main() {
	sopts, popts := parseArgs(os.Args)

	proc := &testingRequestProcessor{
		opts: popts,
	}

	logger := slog.New(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)

	ep.Serve(sopts, proc, logger)
}
