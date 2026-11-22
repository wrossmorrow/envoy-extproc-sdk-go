package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

	manifestPath, absErr := filepath.Abs(*writePath)
	if absErr != nil {
		fmt.Fprintf(os.Stderr, "sync-envoy-versions: resolve path %s: %v\n", *writePath, absErr)
		os.Exit(1)
	}
	repoRoot := filepath.Dir(filepath.Dir(manifestPath))
	if syncErr := syncRepoFiles(repoRoot, manifest); syncErr != nil {
		fmt.Fprintf(os.Stderr, "sync-envoy-versions: %v\n", syncErr)
		os.Exit(1)
	}
}

func syncRepoFiles(repoRoot string, m *conformance.EnvoyVersionManifest) error {
	targets := []struct {
		relPath   string
		transform func(string, *conformance.EnvoyVersionManifest) string
	}{
		{relPath: filepath.Join("examples", "compose.yaml"), transform: conformance.SyncComposeYAML},
		{relPath: filepath.Join("test", "compose.yaml"), transform: conformance.SyncComposeYAML},
		{relPath: "CONFORMANCE.md", transform: conformance.SyncConformanceDoc},
		{relPath: "justfile", transform: conformance.SyncJustfile},
	}
	for _, target := range targets {
		if err := syncRepoFile(repoRoot, target.relPath, target.transform, m); err != nil {
			return err
		}
	}
	return nil
}

func syncRepoFile(
	repoRoot string,
	relPath string,
	transform func(string, *conformance.EnvoyVersionManifest) string,
	m *conformance.EnvoyVersionManifest,
) error {
	path := filepath.Join(repoRoot, relPath)
	raw, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		return nil
	}
	if readErr != nil {
		return fmt.Errorf("read %s: %w", path, readErr)
	}
	original := string(raw)
	updated := transform(original, m)
	if updated == original {
		return nil
	}
	if writeErr := os.WriteFile(path, []byte(updated), 0o644); writeErr != nil {
		return fmt.Errorf("write %s: %w", path, writeErr)
	}
	fmt.Printf("updated %s\n", relPath)
	return nil
}
