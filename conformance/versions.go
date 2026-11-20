package conformance

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
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
