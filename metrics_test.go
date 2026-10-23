package extproc

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var wantMetricsNames = []string{
	"extproc_active_streams",
	"extproc_errored_streams",
	"extproc_phase_errors_total",
	"extproc_total_streams",
	"extproc_stream_duration_seconds",
	"extproc_total_request_headers",
	"extproc_request_body_messages_total",
	"extproc_total_request_trailers",
	"extproc_total_response_headers",
	"extproc_response_body_messages_total",
	"extproc_total_response_trailers",
	"extproc_empty_responses",
	"extproc_request_body_bytes_total",
	"extproc_response_body_bytes_total",
	"extproc_response_send_errors",
}

func TestMetricsRegisterExposesEveryCollector(t *testing.T) {
	m := NewEmptyMetrics()

	registry := prometheus.NewPedanticRegistry()
	m.Register(registry)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics from a fresh registry failed: %v", err)
	}

	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}

	if len(families) != len(wantMetricsNames) {
		t.Errorf("gathered %d metric families, want %d: %v", len(families), len(wantMetricsNames), familyNames(families))
	}
	for _, want := range wantMetricsNames {
		if !names[want] {
			t.Errorf("gathered metrics are missing %q (got %v)", want, familyNames(families))
		}
	}
}

func TestMetricsCounterValues(t *testing.T) {
	m := NewEmptyMetrics()
	registry := prometheus.NewPedanticRegistry()
	m.Register(registry)

	m.TotalStreams.Inc()
	m.PhaseErrors.Inc()
	m.PhaseErrors.Inc()
	m.ResponseSendErrors.Inc()

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics failed: %v", err)
	}

	totalStreams := findMetricFamily(t, families, "extproc_total_streams")
	if got := totalStreams.GetMetric()[0].GetCounter().GetValue(); got != 1 {
		t.Errorf("extproc_total_streams value = %v, want 1", got)
	}

	phaseErrors := findMetricFamily(t, families, "extproc_phase_errors_total")
	if got := phaseErrors.GetMetric()[0].GetCounter().GetValue(); got != 2 {
		t.Errorf("extproc_phase_errors_total value = %v, want 2", got)
	}

	sendErrors := findMetricFamily(t, families, "extproc_response_send_errors")
	if got := sendErrors.GetMetric()[0].GetCounter().GetValue(); got != 1 {
		t.Errorf("extproc_response_send_errors value = %v, want 1", got)
	}
}

func TestMetricsGaugeValue(t *testing.T) {
	m := NewEmptyMetrics()
	registry := prometheus.NewPedanticRegistry()
	m.Register(registry)

	m.ActiveStreams.Set(3)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics failed: %v", err)
	}
	gauge := findMetricFamily(t, families, "extproc_active_streams")
	if got := gauge.GetMetric()[0].GetGauge().GetValue(); got != 3 {
		t.Errorf("extproc_active_streams value = %v, want 3", got)
	}
}

func familyNames(families []*dto.MetricFamily) []string {
	names := make([]string, 0, len(families))
	for _, f := range families {
		names = append(names, f.GetName())
	}
	return names
}

func findMetricFamily(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	t.Fatalf("no metric family named %q in %v", name, familyNames(families))
	return nil
}
