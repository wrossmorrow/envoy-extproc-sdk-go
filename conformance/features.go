package conformance

import (
	_ "embed"
	"fmt"

	yaml "gopkg.in/yaml.v3"
)

//go:embed features.yaml
var featuresYAML []byte

// FeatureStatus describes the SDK's support level for an ext_proc capability.
type FeatureStatus string

const (
	StatusSupported   FeatureStatus = "supported"
	StatusPartial     FeatureStatus = "partial"
	StatusProtocolGap FeatureStatus = "protocol_gap"
	StatusUnsupported FeatureStatus = "unsupported"
)

// FeatureSpec maps a single ext_proc v3 capability to its SDK surface and
// corresponding conformance test names.
type FeatureSpec struct {
	ID         string        `yaml:"id" json:"id"`
	Category   string        `yaml:"category" json:"category"`
	Title      string        `yaml:"title" json:"title"`
	SDKStatus  FeatureStatus `yaml:"sdk_status" json:"sdk_status"`
	SDKSurface string        `yaml:"sdk_surface" json:"sdk_surface"`
	Tests      []string      `yaml:"tests" json:"tests"`
	Notes      string        `yaml:"notes" json:"notes"`
}

// FeaturesManifest lists every tracked ext_proc v3 capability in display order.
type FeaturesManifest struct {
	Features []FeatureSpec `yaml:"features" json:"features"`
}

// LoadFeaturesManifest parses and validates the embedded features.yaml manifest.
func LoadFeaturesManifest() (*FeaturesManifest, error) {
	var m FeaturesManifest
	if err := yaml.Unmarshal(featuresYAML, &m); err != nil {
		return nil, fmt.Errorf("parse features.yaml: %w", err)
	}
	if len(m.Features) == 0 {
		return nil, fmt.Errorf("features.yaml: no features defined")
	}

	seen := make(map[string]struct{}, len(m.Features))
	for _, f := range m.Features {
		if f.ID == "" {
			return nil, fmt.Errorf("features.yaml: feature entry with empty id")
		}
		if _, dup := seen[f.ID]; dup {
			return nil, fmt.Errorf("features.yaml: duplicate feature id %q", f.ID)
		}
		seen[f.ID] = struct{}{}

		switch f.SDKStatus {
		case StatusSupported, StatusPartial, StatusProtocolGap, StatusUnsupported:
		default:
			return nil, fmt.Errorf("features.yaml: feature %q has invalid sdk_status %q", f.ID, f.SDKStatus)
		}

		if f.SDKStatus != StatusUnsupported && len(f.Tests) == 0 {
			return nil, fmt.Errorf("features.yaml: feature %q (%s) must declare at least one conformance test", f.ID, f.SDKStatus)
		}
	}
	return &m, nil
}
