package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeaturesManifest(t *testing.T) {
	m, err := LoadFeaturesManifest()
	if err != nil {
		t.Fatalf("LoadFeaturesManifest() error: %v", err)
	}
	if len(m.Features) < 15 {
		t.Errorf("expected at least 15 tracked features, got %d", len(m.Features))
	}
}

func TestParseGoTestJSONAndBuildReport(t *testing.T) {
	v140JSON := strings.Join([]string{
		`{"Action":"start","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance"}`,
		`{"Action":"pass","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/headers_mutations_http1"}`,
		`{"Action":"pass","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/headers_mutations_h2c"}`,
		`{"Action":"pass","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/headers_multivalue_and_context_state"}`,
		`{"Action":"skip","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/header_append_action_envoy_gap"}`,
		`{"Action":"skip","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/observability_mode_envoy_gap"}`,
	}, "\n")

	devJSON := strings.Join([]string{
		`{"Action":"pass","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/headers_mutations_http1"}`,
		`{"Action":"fail","Package":"github.com/wrossmorrow/envoy-extproc-sdk-go/conformance","Test":"TestEnvoyConformance/headers_mutations_h2c"}`,
	}, "\n")

	dir := t.TempDir()
	if werr := os.WriteFile(filepath.Join(dir, "results-v1.40-latest.json"), []byte(v140JSON), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(dir, "results-dev.json"), []byte(devJSON), 0o644); werr != nil {
		t.Fatal(werr)
	}

	runs, err := LoadResultsDir(dir, []string{"v1.40-latest", "dev"})
	if err != nil {
		t.Fatalf("LoadResultsDir error: %v", err)
	}
	if len(runs) != 2 || runs[0].Version != "v1.40-latest" || runs[1].Version != "dev" {
		t.Fatalf("unexpected run ordering: %+v", runs)
	}

	manifest := &FeaturesManifest{
		Features: []FeatureSpec{
			{
				ID:         "headers_core",
				Category:   "Headers",
				Title:      "Core header mutations",
				SDKStatus:  StatusSupported,
				SDKSurface: "AddHeader, OverwriteHeader",
				Tests: []string{
					"TestEnvoyConformance/headers_mutations_http1",
					"TestEnvoyConformance/headers_mutations_h2c",
				},
			},
			{
				ID:         "append_gap",
				Category:   "Headers",
				Title:      "Append action",
				SDKStatus:  StatusPartial,
				SDKSurface: "AppendHeader",
				Tests:      []string{"TestEnvoyConformance/header_append_action_envoy_gap"},
			},
			{
				ID:         "not_run_feature",
				Category:   "Bodies",
				Title:      "Missing test",
				SDKStatus:  StatusSupported,
				SDKSurface: "ReplaceBody",
				Tests:      []string{"TestEnvoyConformance/missing_subtest"},
			},
			{
				ID:         "future_feature",
				Category:   "Spec",
				Title:      "Mode override",
				SDKStatus:  StatusUnsupported,
				SDKSurface: "mode_override",
			},
		},
	}

	report, err := BuildSummaryReport(manifest, runs)
	if err != nil {
		t.Fatalf("BuildSummaryReport error: %v", err)
	}

	if got := report.Features[0].Results["v1.40-latest"]; got.Status != CellPass || got.Display != "PASS (2/2)" {
		t.Errorf("headers_core v1.40-latest = %+v, want PASS (2/2)", got)
	}
	if got := report.Features[0].Results["dev"]; got.Status != CellFail || got.Display != "FAIL (1/2)" {
		t.Errorf("headers_core dev = %+v, want FAIL (1/2)", got)
	}
	if got := report.Features[1].Results["v1.40-latest"]; got.Status != CellKnownGap || got.Display != "KNOWN GAP" {
		t.Errorf("append_gap v1.40-latest = %+v, want KNOWN GAP", got)
	}
	if got := report.Features[2].Results["v1.40-latest"]; got.Status != CellNotRun || got.Display != "N/R" {
		t.Errorf("not_run_feature v1.40-latest = %+v, want N/R", got)
	}
	if got := report.Features[3].Results["v1.40-latest"]; got.Status != CellUnsupported || got.Display != "—" {
		t.Errorf("future_feature v1.40-latest = %+v, want —", got)
	}

	if serr := ValidateStrict(report); serr == nil {
		t.Error("expected ValidateStrict to fail when report has FAIL and N/R supported features")
	}

	summary := RenderStepSummary(report)
	for _, want := range []string{"## Envoy Conformance Matrix", "| v1.40-latest | dev |", "PASS (2/2)", "FAIL (1/2)", "KNOWN GAP"} {
		if !strings.Contains(summary, want) {
			t.Errorf("RenderStepSummary missing %q\n%s", want, summary)
		}
	}

	docPath := filepath.Join(dir, "CONFORMANCE.md")
	initialDoc := "# Header\n\n" + ConformanceMatrixStartMarker + "\nold\n" + ConformanceMatrixEndMarker + "\n\n## Footer\n"
	if werr := os.WriteFile(docPath, []byte(initialDoc), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if uerr := UpdateConformanceDoc(docPath, report); uerr != nil {
		t.Fatalf("UpdateConformanceDoc error: %v", uerr)
	}
	updatedBytes, rerr := os.ReadFile(docPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	updated := string(updatedBytes)
	if strings.Contains(updated, "\nold\n") {
		t.Errorf("UpdateConformanceDoc did not replace old content:\n%s", updated)
	}
	if !strings.Contains(updated, "## Footer") || !strings.Contains(updated, "PASS (2/2)") {
		t.Errorf("UpdateConformanceDoc corrupted surrounding doc or table:\n%s", updated)
	}
}
