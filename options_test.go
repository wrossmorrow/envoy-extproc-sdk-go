package extproc

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "options.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewDefaultServerOptions(t *testing.T) {
	opts := NewDefaultServerOptions()
	if opts.ExtProcPort != 50051 {
		t.Errorf("ExtProcPort = %d, want 50051", opts.ExtProcPort)
	}
	if opts.MetricsHTTPPort != 9090 {
		t.Errorf("MetricsHTTPPort = %d, want 9090", opts.MetricsHTTPPort)
	}
	if opts.MaxConcurrentStreams != 1000 {
		t.Errorf("MaxConcurrentStreams = %d, want 1000", opts.MaxConcurrentStreams)
	}
	if opts.UnreadyPropagationDelaySeconds != 5 {
		t.Errorf("UnreadyPropagationDelaySeconds = %d, want 5", opts.UnreadyPropagationDelaySeconds)
	}
	if opts.TerminationGracePeriodSeconds != 10 {
		t.Errorf("TerminationGracePeriodSeconds = %d, want 10", opts.TerminationGracePeriodSeconds)
	}
}

func TestNewDefaultProcessingOptions(t *testing.T) {
	opts := NewDefaultProcessingOptions()
	if opts.UpdateExtProcHeader != false {
		t.Errorf("UpdateExtProcHeader = %v, want false", opts.UpdateExtProcHeader)
	}
	if opts.UpdateDurationHeader != false {
		t.Errorf("UpdateDurationHeader = %v, want false", opts.UpdateDurationHeader)
	}
	if opts.AbortOnProcessorFailure != false {
		t.Errorf("AbortOnProcessorFailure = %v, want false", opts.AbortOnProcessorFailure)
	}
}

func TestServerOptionsFromJson(t *testing.T) {
	opts, err := NewServerOptionsFromJson(writeTempFile(t, `{
		"extproc_port": 50052,
		"metrics_http_port": 9091,
		"max_concurrent_streams": 42,
		"unready_propagation_delay_seconds": 3,
		"termination_grace_period_seconds": 7
	}`))
	if err != nil {
		t.Fatalf("NewServerOptionsFromJson failed: %v", err)
	}
	if opts.ExtProcPort != 50052 {
		t.Errorf("ExtProcPort = %d, want 50052", opts.ExtProcPort)
	}
	if opts.MetricsHTTPPort != 9091 {
		t.Errorf("MetricsHTTPPort = %d, want 9091", opts.MetricsHTTPPort)
	}
	if opts.MaxConcurrentStreams != 42 {
		t.Errorf("MaxConcurrentStreams = %d, want 42", opts.MaxConcurrentStreams)
	}
	if opts.UnreadyPropagationDelaySeconds != 3 {
		t.Errorf("UnreadyPropagationDelaySeconds = %d, want 3", opts.UnreadyPropagationDelaySeconds)
	}
	if opts.TerminationGracePeriodSeconds != 7 {
		t.Errorf("TerminationGracePeriodSeconds = %d, want 7", opts.TerminationGracePeriodSeconds)
	}
}

func TestServerOptionsFromJsonInvalid(t *testing.T) {
	if _, err := NewServerOptionsFromJson(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Error("expected an error for a missing JSON options file")
	}
	if _, err := NewServerOptionsFromJson(writeTempFile(t, `{"extproc_port": }`)); err == nil {
		t.Error("expected an error for malformed JSON options")
	}
}

func TestServerOptionsFromYaml(t *testing.T) {
	opts, err := NewServerOptionsFromYaml(writeTempFile(t, `
extproc_port: 50053
metrics_http_port: 9092
max_concurrent_streams: 43
unready_propagation_delay_seconds: 4
termination_grace_period_seconds: 8
`))
	if err != nil {
		t.Fatalf("NewServerOptionsFromYaml failed: %v", err)
	}
	if opts.ExtProcPort != 50053 {
		t.Errorf("ExtProcPort = %d, want 50053", opts.ExtProcPort)
	}
	if opts.MetricsHTTPPort != 9092 {
		t.Errorf("MetricsHTTPPort = %d, want 9092", opts.MetricsHTTPPort)
	}
	if opts.MaxConcurrentStreams != 43 {
		t.Errorf("MaxConcurrentStreams = %d, want 43", opts.MaxConcurrentStreams)
	}
	if opts.UnreadyPropagationDelaySeconds != 4 {
		t.Errorf("UnreadyPropagationDelaySeconds = %d, want 4", opts.UnreadyPropagationDelaySeconds)
	}
	if opts.TerminationGracePeriodSeconds != 8 {
		t.Errorf("TerminationGracePeriodSeconds = %d, want 8", opts.TerminationGracePeriodSeconds)
	}
}

func TestServerOptionsFromYamlInvalid(t *testing.T) {
	if _, err := NewServerOptionsFromYaml(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Error("expected an error for a missing YAML options file")
	}
	if _, err := NewServerOptionsFromYaml(writeTempFile(t, "extproc_port: [unclosed")); err == nil {
		t.Error("expected an error for malformed YAML options")
	}
}

func TestProcessingOptionsFromJson(t *testing.T) {
	opts, err := NewProcessingOptionsFromJson(writeTempFile(t, `{
		"update_extproc_header": true,
		"update_duration_header": true,
		"abort_on_processor_failure": true
	}`))
	if err != nil {
		t.Fatalf("NewProcessingOptionsFromJson failed: %v", err)
	}
	if opts.UpdateExtProcHeader != true {
		t.Errorf("UpdateExtProcHeader = %v, want true", opts.UpdateExtProcHeader)
	}
	if opts.UpdateDurationHeader != true {
		t.Errorf("UpdateDurationHeader = %v, want true", opts.UpdateDurationHeader)
	}
	if opts.AbortOnProcessorFailure != true {
		t.Errorf("AbortOnProcessorFailure = %v, want true", opts.AbortOnProcessorFailure)
	}
}

func TestProcessingOptionsFromJsonInvalid(t *testing.T) {
	if _, err := NewProcessingOptionsFromJson(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Error("expected an error for a missing JSON options file")
	}
	if _, err := NewProcessingOptionsFromJson(writeTempFile(t, `{not json`)); err == nil {
		t.Error("expected an error for malformed JSON options")
	}
}
