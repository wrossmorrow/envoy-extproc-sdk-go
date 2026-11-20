package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance"
)

const defaultEOLURL = "https://endoflife.date/api/envoy.json"

func main() {
	fs := flag.NewFlagSet("sync-envoy-versions", flag.ExitOnError)
	url := fs.String("url", defaultEOLURL, "endoflife.date Envoy JSON endpoint")
	fromFile := fs.String("from-file", "", "optional local JSON file instead of HTTP fetch")
	writePath := fs.String("write", "envoy-versions.json", "output envoy-versions.json path")
	_ = fs.Parse(os.Args[1:])

	var raw []byte
	var err error
	if *fromFile != "" {
		raw, err = os.ReadFile(*fromFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sync-envoy-versions: read %s: %v\n", *fromFile, err)
			os.Exit(1)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "sync-envoy-versions: build request: %v\n", rerr)
			os.Exit(1)
		}
		resp, derr := http.DefaultClient.Do(req)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "sync-envoy-versions: fetch %s: %v\n", *url, derr)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "sync-envoy-versions: %s returned HTTP %d\n", *url, resp.StatusCode)
			os.Exit(1)
		}
		raw, err = io.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sync-envoy-versions: read response: %v\n", err)
			os.Exit(1)
		}
	}

	existing, err := conformance.LoadEnvoyVersions()
	var canary []string
	if err == nil {
		canary = existing.Canary
	}

	manifest, err := conformance.SyncEnvoyVersionsFromEOL(raw, time.Now(), canary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync-envoy-versions: %v\n", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync-envoy-versions: marshal JSON: %v\n", err)
		os.Exit(1)
	}
	if werr := os.WriteFile(*writePath, append(out, '\n'), 0o644); werr != nil {
		fmt.Fprintf(os.Stderr, "sync-envoy-versions: write %s: %v\n", *writePath, werr)
		os.Exit(1)
	}
	fmt.Printf("synced %s: default=%s supported=%v canary=%v\n", *writePath, manifest.Default, manifest.Supported, manifest.Canary)
}
