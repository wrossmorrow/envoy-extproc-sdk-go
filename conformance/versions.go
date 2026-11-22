package conformance

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed envoy-versions.json
var envoyVersionsJSON []byte

// EnvoyVersionManifest defines the default, supported, and canary Envoy
// container image tags targeted by the conformance suite.
type EnvoyVersionManifest struct {
	Default   string   `json:"default"`
	Supported []string `json:"supported"`
	Canary    []string `json:"canary"`
}

// LoadEnvoyVersions parses the embedded envoy-versions.json manifest.
func LoadEnvoyVersions() (*EnvoyVersionManifest, error) {
	var m EnvoyVersionManifest
	if err := json.Unmarshal(envoyVersionsJSON, &m); err != nil {
		return nil, fmt.Errorf("parse envoy-versions.json: %w", err)
	}
	if m.Default == "" {
		return nil, fmt.Errorf("envoy-versions.json: default version is empty")
	}
	return &m, nil
}

// ResolveEnvoyVersion returns ENVOY_VERSION when set, or the default version
// declared in envoy-versions.json.
func ResolveEnvoyVersion() (string, error) {
	if v := os.Getenv("ENVOY_VERSION"); v != "" {
		return v, nil
	}
	m, err := LoadEnvoyVersions()
	if err != nil {
		return "", err
	}
	return m.Default, nil
}

type eolCycleEntry struct {
	Cycle string          `json:"cycle"`
	EOL   json.RawMessage `json:"eol"`
}

func isCycleActive(rawEOL json.RawMessage, todayUTC string) (bool, error) {
	trimmed := strings.TrimSpace(string(rawEOL))
	if trimmed == "" || trimmed == "false" || trimmed == "null" {
		return true, nil
	}
	if trimmed == "true" {
		return false, nil
	}
	var dateStr string
	if err := json.Unmarshal(rawEOL, &dateStr); err != nil {
		return false, fmt.Errorf("unexpected eol value %q: %w", trimmed, err)
	}
	if dateStr == "" {
		return true, nil
	}
	if _, err := time.Parse("2006-01-02", dateStr); err != nil {
		return false, fmt.Errorf("parse eol date %q: %w", dateStr, err)
	}
	return dateStr > todayUTC, nil
}

func parseCycleMajorMinor(cycle string) (int, int, bool) {
	parts := strings.Split(strings.TrimPrefix(cycle, "v"), ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// SyncEnvoyVersionsFromEOL builds an EnvoyVersionManifest from an
// endoflife.date /api/envoy.json payload, retaining all cycles whose EOL date
// has not passed as of now.
func SyncEnvoyVersionsFromEOL(eolJSON []byte, now time.Time, canary []string) (*EnvoyVersionManifest, error) {
	var entries []eolCycleEntry
	if err := json.Unmarshal(eolJSON, &entries); err != nil {
		return nil, fmt.Errorf("parse endoflife.date envoy JSON: %w", err)
	}

	todayUTC := now.UTC().Format("2006-01-02")
	type parsedCycle struct {
		major int
		minor int
		tag   string
	}
	var active []parsedCycle
	seen := make(map[string]struct{})
	for _, entry := range entries {
		ok, err := isCycleActive(entry.EOL, todayUTC)
		if err != nil {
			return nil, fmt.Errorf("cycle %q: %w", entry.Cycle, err)
		}
		if !ok {
			continue
		}
		major, minor, parsed := parseCycleMajorMinor(entry.Cycle)
		if !parsed {
			continue
		}
		tag := fmt.Sprintf("v%d.%d-latest", major, minor)
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		active = append(active, parsedCycle{major: major, minor: minor, tag: tag})
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("no active Envoy release cycles found as of %s", todayUTC)
	}

	sort.Slice(active, func(i, j int) bool {
		if active[i].major != active[j].major {
			return active[i].major < active[j].major
		}
		return active[i].minor < active[j].minor
	})

	supported := make([]string, 0, len(active))
	for _, c := range active {
		supported = append(supported, c.tag)
	}

	canaryCopy := append([]string(nil), canary...)
	if len(canaryCopy) == 0 {
		canaryCopy = []string{"dev"}
	}

	return &EnvoyVersionManifest{
		Default:   supported[len(supported)-1],
		Supported: supported,
		Canary:    canaryCopy,
	}, nil
}

var (
	composeEnvoyImagePattern          = regexp.MustCompile(`envoyproxy/envoy:\$\{ENVOY_VERSION:-[^}]+\}`)
	conformanceDefaultEnvPattern      = regexp.MustCompile("defaults to `ENVOY_VERSION=[^`]+`")
	conformanceOverrideExamplePattern = regexp.MustCompile("ENVOY_VERSION=[^\\s`]+ just integration-test")
	conformanceRecipeExamplePattern   = regexp.MustCompile(`just conformance-version v[0-9]+\.[0-9]+-latest`)
	conformanceMatrixRangePattern     = regexp.MustCompile("\\| `v[0-9]+\\.[0-9]+` – `v[0-9]+\\.[0-9]+` \\+ `dev` \\|")
	justfileVersionPattern            = regexp.MustCompile(`conformance-version envoy_version="[^"]+"`)
	justfileCommentExamplePattern     = regexp.MustCompile(`\(e\.g\. v[0-9]+\.[0-9]+-latest\)`)
)

// SyncComposeYAML rewrites default ENVOY_VERSION image tags in a Compose file
// to match m.Default.
func SyncComposeYAML(content string, m *EnvoyVersionManifest) string {
	if m == nil || m.Default == "" {
		return content
	}
	return composeEnvoyImagePattern.ReplaceAllLiteralString(
		content,
		"envoyproxy/envoy:${ENVOY_VERSION:-"+m.Default+"}",
	)
}

// SyncConformanceDoc updates Envoy default version references and the
// supported minor version range in CONFORMANCE.md to match m.
func SyncConformanceDoc(content string, m *EnvoyVersionManifest) string {
	if m == nil {
		return content
	}
	updated := content
	if m.Default != "" {
		updated = composeEnvoyImagePattern.ReplaceAllLiteralString(
			updated,
			"envoyproxy/envoy:${ENVOY_VERSION:-"+m.Default+"}",
		)
		updated = conformanceDefaultEnvPattern.ReplaceAllLiteralString(
			updated,
			"defaults to `ENVOY_VERSION="+m.Default+"`",
		)
		updated = conformanceOverrideExamplePattern.ReplaceAllLiteralString(
			updated,
			"ENVOY_VERSION="+m.Default+" just integration-test",
		)
		updated = conformanceRecipeExamplePattern.ReplaceAllLiteralString(
			updated,
			"just conformance-version "+m.Default,
		)
	}
	if len(m.Supported) > 0 {
		firstCycle := strings.TrimSuffix(m.Supported[0], "-latest")
		lastCycle := strings.TrimSuffix(m.Supported[len(m.Supported)-1], "-latest")
		updated = conformanceMatrixRangePattern.ReplaceAllLiteralString(
			updated,
			"| `"+firstCycle+"` – `"+lastCycle+"` + `dev` |",
		)
	}
	return updated
}

// SyncJustfile updates the conformance-version default argument and comment
// example in justfile to match m.Default.
func SyncJustfile(content string, m *EnvoyVersionManifest) string {
	if m == nil || m.Default == "" {
		return content
	}
	updated := justfileVersionPattern.ReplaceAllLiteralString(
		content,
		`conformance-version envoy_version="`+m.Default+`"`,
	)
	updated = justfileCommentExamplePattern.ReplaceAllLiteralString(
		updated,
		"(e.g. "+m.Default+")",
	)
	return updated
}
