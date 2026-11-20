package conformance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// ConformanceMatrixStartMarker bounds the generated Markdown matrix in CONFORMANCE.md.
	ConformanceMatrixStartMarker = "<!-- BEGIN GENERATED CONFORMANCE MATRIX -->"
	// ConformanceMatrixEndMarker terminates the generated Markdown matrix in CONFORMANCE.md.
	ConformanceMatrixEndMarker = "<!-- END GENERATED CONFORMANCE MATRIX -->"
)

// TestOutcome represents the terminal outcome of a single Go test case.
type TestOutcome string

const (
	OutcomePass TestOutcome = "pass"
	OutcomeFail TestOutcome = "fail"
	OutcomeSkip TestOutcome = "skip"
)

// VersionTestRun stores parsed go test -json outcomes for one Envoy version run.
type VersionTestRun struct {
	Outcomes      map[string]TestOutcome
	PackageFailed bool
}

// VersionResult associates parsed test outcomes with an Envoy image tag.
type VersionResult struct {
	Version string          `json:"version"`
	Run     *VersionTestRun `json:"-"`
}

// CellStatus represents the evaluated status of one feature on one Envoy version.
type CellStatus string

const (
	CellPass        CellStatus = "pass"
	CellFail        CellStatus = "fail"
	CellPartial     CellStatus = "partial"
	CellKnownGap    CellStatus = "known_gap"
	CellNotRun      CellStatus = "not_run"
	CellUnsupported CellStatus = "unsupported"
)

// CellSummary is one cell in the Capability × Envoy Version matrix.
type CellSummary struct {
	Status  CellStatus `json:"status"`
	Display string     `json:"display"`
	Passed  int        `json:"passed"`
	Failed  int        `json:"failed"`
	Skipped int        `json:"skipped"`
	Missing int        `json:"missing"`
	Total   int        `json:"total"`
}

// FeatureSummary is one row in the aggregated conformance report.
type FeatureSummary struct {
	ID         string                 `json:"id"`
	Category   string                 `json:"category"`
	Title      string                 `json:"title"`
	SDKStatus  FeatureStatus          `json:"sdk_status"`
	SDKSurface string                 `json:"sdk_surface"`
	Notes      string                 `json:"notes"`
	Results    map[string]CellSummary `json:"results"`
}

// SummaryReport holds the full Capability × Envoy Version conformance matrix.
type SummaryReport struct {
	Versions []string         `json:"versions"`
	Features []FeatureSummary `json:"features"`
}

type goTestJSONEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

// ParseGoTestJSON reads a go test -json stream and extracts terminal outcomes
// for each test and package-level failure markers.
func ParseGoTestJSON(r io.Reader) (*VersionTestRun, error) {
	run := &VersionTestRun{
		Outcomes: make(map[string]TestOutcome),
	}

	scanner := bufio.NewScanner(r)
	// Allow long log lines in go test -json output (up to 4 MiB).
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev goTestJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("parse go test -json event: %w", err)
		}
		if ev.Test == "" {
			if ev.Action == "fail" {
				run.PackageFailed = true
			}
			continue
		}
		switch ev.Action {
		case string(OutcomePass):
			run.Outcomes[ev.Test] = OutcomePass
		case string(OutcomeFail):
			run.Outcomes[ev.Test] = OutcomeFail
		case string(OutcomeSkip):
			run.Outcomes[ev.Test] = OutcomeSkip
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read go test -json stream: %w", err)
	}
	return run, nil
}

// LoadResultsDir discovers and parses every results-<version>.json file in dir,
// ordering known versions according to versionOrder followed by extras sorted
// lexicographically.
func LoadResultsDir(dir string, versionOrder []string) ([]VersionResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read results dir %q: %w", dir, err)
	}

	byVersion := make(map[string]VersionResult)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "results-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		version := strings.TrimSuffix(strings.TrimPrefix(name, "results-"), ".json")
		if version == "" {
			continue
		}
		f, ferr := os.Open(filepath.Join(dir, name))
		if ferr != nil {
			return nil, fmt.Errorf("open %s: %w", name, ferr)
		}
		run, perr := ParseGoTestJSON(f)
		_ = f.Close()
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", name, perr)
		}
		byVersion[version] = VersionResult{Version: version, Run: run}
	}

	ordered := make([]VersionResult, 0, len(byVersion))
	seen := make(map[string]struct{}, len(byVersion))
	for _, v := range versionOrder {
		if res, ok := byVersion[v]; ok {
			ordered = append(ordered, res)
			seen[v] = struct{}{}
		}
	}

	var extras []string
	for v := range byVersion {
		if _, ok := seen[v]; !ok {
			extras = append(extras, v)
		}
	}
	sort.Strings(extras)
	for _, v := range extras {
		ordered = append(ordered, byVersion[v])
	}
	return ordered, nil
}

func evaluateCell(spec FeatureSpec, run *VersionTestRun) CellSummary {
	if spec.SDKStatus == StatusUnsupported && len(spec.Tests) == 0 {
		return CellSummary{
			Status:  CellUnsupported,
			Display: "—",
		}
	}

	total := len(spec.Tests)
	if run == nil {
		return CellSummary{
			Status:  CellNotRun,
			Display: "N/R",
			Missing: total,
			Total:   total,
		}
	}

	var passed, failed, skipped, missing int
	for _, testName := range spec.Tests {
		outcome, ok := run.Outcomes[testName]
		if !ok {
			missing++
			continue
		}
		switch outcome {
		case OutcomePass:
			passed++
		case OutcomeFail:
			failed++
		case OutcomeSkip:
			skipped++
		}
	}

	if run.PackageFailed && passed == 0 && skipped == 0 {
		return CellSummary{
			Status:  CellFail,
			Display: fmt.Sprintf("FAIL (0/%d)", total),
			Failed:  total,
			Total:   total,
		}
	}

	if failed > 0 {
		return CellSummary{
			Status:  CellFail,
			Display: fmt.Sprintf("FAIL (%d/%d)", passed, total),
			Passed:  passed,
			Failed:  failed,
			Skipped: skipped,
			Missing: missing,
			Total:   total,
		}
	}

	if total > 0 && passed == total {
		return CellSummary{
			Status:  CellPass,
			Display: fmt.Sprintf("PASS (%d/%d)", passed, total),
			Passed:  passed,
			Total:   total,
		}
	}

	if passed > 0 && skipped > 0 && missing == 0 {
		return CellSummary{
			Status:  CellPartial,
			Display: fmt.Sprintf("PARTIAL (%d/%d)", passed, total),
			Passed:  passed,
			Skipped: skipped,
			Total:   total,
		}
	}

	if total > 0 && skipped == total {
		return CellSummary{
			Status:  CellKnownGap,
			Display: "KNOWN GAP",
			Skipped: skipped,
			Total:   total,
		}
	}

	return CellSummary{
		Status:  CellNotRun,
		Display: "N/R",
		Passed:  passed,
		Skipped: skipped,
		Missing: missing,
		Total:   total,
	}
}

// BuildSummaryReport aggregates parsed test runs against the features manifest.
func BuildSummaryReport(manifest *FeaturesManifest, runs []VersionResult) (*SummaryReport, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	versions := make([]string, 0, len(runs))
	for _, r := range runs {
		versions = append(versions, r.Version)
	}

	features := make([]FeatureSummary, 0, len(manifest.Features))
	for _, spec := range manifest.Features {
		cells := make(map[string]CellSummary, len(runs))
		for _, r := range runs {
			cells[r.Version] = evaluateCell(spec, r.Run)
		}
		features = append(features, FeatureSummary{
			ID:         spec.ID,
			Category:   spec.Category,
			Title:      spec.Title,
			SDKStatus:  spec.SDKStatus,
			SDKSurface: spec.SDKSurface,
			Notes:      spec.Notes,
			Results:    cells,
		})
	}

	return &SummaryReport{
		Versions: versions,
		Features: features,
	}, nil
}

// ValidateStrict ensures at least one version run was loaded, no cell failed,
// and every supported feature executed completely without missing tests.
func ValidateStrict(report *SummaryReport) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	if len(report.Versions) == 0 {
		return fmt.Errorf("no conformance result files found")
	}
	for _, f := range report.Features {
		for _, v := range report.Versions {
			cell := f.Results[v]
			if cell.Status == CellFail {
				return fmt.Errorf("feature %q failed on Envoy %s (%s)", f.ID, v, cell.Display)
			}
			if f.SDKStatus == StatusSupported && cell.Status != CellPass {
				return fmt.Errorf("supported feature %q did not pass on Envoy %s (got %s)", f.ID, v, cell.Display)
			}
		}
	}
	return nil
}

func formatSDKStatus(status FeatureStatus) string {
	switch status {
	case StatusSupported:
		return "Supported"
	case StatusPartial:
		return "Partial"
	case StatusProtocolGap:
		return "Protocol Gap"
	case StatusUnsupported:
		return "Not Yet Supported"
	default:
		return string(status)
	}
}

func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// RenderMarkdownTable formats the Capability × Envoy Version matrix as GitHub Markdown.
func RenderMarkdownTable(report *SummaryReport) string {
	var b strings.Builder
	b.WriteString("| Category | Capability | SDK Status | SDK Surface |")
	for _, v := range report.Versions {
		b.WriteString(" " + escapePipe(v) + " |")
	}
	b.WriteString(" Notes |\n")

	b.WriteString("| :--- | :--- | :--- | :--- |")
	for range report.Versions {
		b.WriteString(" :---: |")
	}
	b.WriteString(" :--- |\n")

	for _, f := range report.Features {
		b.WriteString("| ")
		b.WriteString(escapePipe(f.Category))
		b.WriteString(" | ")
		b.WriteString(escapePipe(f.Title))
		b.WriteString(" | ")
		b.WriteString(formatSDKStatus(f.SDKStatus))
		b.WriteString(" | ")
		if f.SDKSurface != "" {
			b.WriteString("`" + escapePipe(f.SDKSurface) + "`")
		}
		b.WriteString(" |")
		for _, v := range report.Versions {
			cell := f.Results[v]
			b.WriteString(" " + escapePipe(cell.Display) + " |")
		}
		b.WriteString(" ")
		b.WriteString(escapePipe(f.Notes))
		b.WriteString(" |\n")
	}
	return b.String()
}

// RenderStepSummary formats a full GitHub Actions step summary document.
func RenderStepSummary(report *SummaryReport) string {
	var b strings.Builder
	b.WriteString("## Envoy Conformance Matrix\n\n")
	if len(report.Versions) == 0 {
		b.WriteString("_No conformance test results were collected._\n")
		return b.String()
	}
	b.WriteString(RenderMarkdownTable(report))
	return b.String()
}

// UpdateConformanceDoc replaces the section bounded by ConformanceMatrixStartMarker
// and ConformanceMatrixEndMarker inside docPath with the generated matrix table.
func UpdateConformanceDoc(docPath string, report *SummaryReport) error {
	raw, err := os.ReadFile(docPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", docPath, err)
	}
	content := string(raw)
	startIdx := strings.Index(content, ConformanceMatrixStartMarker)
	endIdx := strings.Index(content, ConformanceMatrixEndMarker)
	if startIdx < 0 || endIdx < 0 || endIdx < startIdx {
		return fmt.Errorf("%s missing %q / %q markers", docPath, ConformanceMatrixStartMarker, ConformanceMatrixEndMarker)
	}

	replacement := ConformanceMatrixStartMarker + "\n\n" + RenderMarkdownTable(report) + "\n" + ConformanceMatrixEndMarker
	updated := content[:startIdx] + replacement + content[endIdx+len(ConformanceMatrixEndMarker):]
	if werr := os.WriteFile(docPath, []byte(updated), 0o644); werr != nil {
		return fmt.Errorf("write %s: %w", docPath, werr)
	}
	return nil
}
