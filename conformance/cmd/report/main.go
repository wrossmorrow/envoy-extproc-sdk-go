package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance"
)

func main() {
	fs := flag.NewFlagSet("conformance-report", flag.ExitOnError)
	resultsDir := fs.String("results-dir", ".", "directory containing results-<version>.json files")
	markdownOut := fs.String("markdown-out", "", "optional path to write a Markdown summary")
	jsonOut := fs.String("json-out", "", "optional path to write the JSON summary report")
	updateDoc := fs.String("update-conformance-md", "", "optional path to CONFORMANCE.md to update between generated markers")
	strict := fs.Bool("strict", false, "exit non-zero if any run failed or a supported feature did not pass")
	_ = fs.Parse(os.Args[1:])

	manifest, err := conformance.LoadFeaturesManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-report: %v\n", err)
		os.Exit(1)
	}

	versions, err := conformance.LoadEnvoyVersions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-report: %v\n", err)
		os.Exit(1)
	}

	order := make([]string, 0, len(versions.Supported)+len(versions.Canary))
	order = append(order, versions.Supported...)
	order = append(order, versions.Canary...)

	runs, err := conformance.LoadResultsDir(*resultsDir, order)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-report: %v\n", err)
		os.Exit(1)
	}

	report, err := conformance.BuildSummaryReport(manifest, runs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-report: %v\n", err)
		os.Exit(1)
	}

	summary := conformance.RenderStepSummary(report)
	if *markdownOut != "" {
		if werr := os.WriteFile(*markdownOut, []byte(summary), 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "conformance-report: write markdown %q: %v\n", *markdownOut, werr)
			os.Exit(1)
		}
	} else {
		fmt.Print(summary)
	}

	if *jsonOut != "" {
		raw, jerr := json.MarshalIndent(report, "", "  ")
		if jerr != nil {
			fmt.Fprintf(os.Stderr, "conformance-report: marshal JSON: %v\n", jerr)
			os.Exit(1)
		}
		if werr := os.WriteFile(*jsonOut, append(raw, '\n'), 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "conformance-report: write JSON %q: %v\n", *jsonOut, werr)
			os.Exit(1)
		}
	}

	if *updateDoc != "" {
		if uerr := conformance.UpdateConformanceDoc(*updateDoc, report); uerr != nil {
			fmt.Fprintf(os.Stderr, "conformance-report: %v\n", uerr)
			os.Exit(1)
		}
	}

	if *strict {
		if serr := conformance.ValidateStrict(report); serr != nil {
			fmt.Fprintf(os.Stderr, "conformance-report: strict validation failed: %v\n", serr)
			os.Exit(1)
		}
	}
}
