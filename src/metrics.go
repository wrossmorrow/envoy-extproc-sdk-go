package extproc

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	ActiveStreams         prometheus.Gauge
	ErroredStreams        prometheus.Counter
	TotalStreams          prometheus.Counter
	StreamDurationSeconds prometheus.Histogram
	TotalRequestHeaders   prometheus.Counter
	TotalRequestBody      prometheus.Counter
	TotalRequestTrailers  prometheus.Counter
	TotalResponseHeaders  prometheus.Counter
	TotalResponseBody     prometheus.Counter
	TotalResponseTrailers prometheus.Counter
	TotalEmptyResponses   prometheus.Counter
	BodyBytesReceived     prometheus.Counter
	BodyBytesReturned     prometheus.Counter
	ResponseSendErrors    prometheus.Counter
}

func NewEmptyMetrics() *Metrics {
	return &Metrics{
		ActiveStreams: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "extproc_active_streams",
				Help: "Current number of active streams.",
			},
		),
		ErroredStreams: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_errored_streams",
				Help: "Total number of streams that errored.",
			},
		),
		TotalStreams: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_streams",
				Help: "Total number of streams processed.",
			},
		),
		StreamDurationSeconds: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:                        "extproc_stream_duration_seconds",
				Help:                        "Stream duration in seconds (native histogram).",
				NativeHistogramBucketFactor: 1.1, // O(10%) resolution across any scale
			},
		),
		TotalRequestHeaders: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_request_headers",
				Help: "Total number of request headers processed.",
			},
		),
		TotalRequestBody: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_request_body",
				Help: "Total number of request bodies processed.",
			},
		),
		TotalRequestTrailers: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_request_trailers",
				Help: "Total number of request trailers processed.",
			},
		),
		TotalResponseHeaders: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_response_headers",
				Help: "Total number of response headers processed.",
			},
		),
		TotalResponseBody: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_response_body",
				Help: "Total number of response bodies processed.",
			},
		),
		TotalResponseTrailers: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_total_response_trailers",
				Help: "Total number of response trailers processed.",
			},
		),
		TotalEmptyResponses: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_empty_responses",
				Help: "Total number of empty responses processed.",
			},
		),
		BodyBytesReceived: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_body_bytes_received",
				Help: "Total number of body bytes received.",
			},
		),
		BodyBytesReturned: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_body_bytes_returned",
				Help: "Total number of body bytes returned.",
			},
		),
		ResponseSendErrors: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "extproc_response_send_errors",
				Help: "Total number of response send errors.",
			},
		),
	}
}

func (m *Metrics) Register() *Metrics {
	prometheus.MustRegister(m.ActiveStreams)
	prometheus.MustRegister(m.ErroredStreams)
	prometheus.MustRegister(m.TotalStreams)
	prometheus.MustRegister(m.StreamDurationSeconds)
	prometheus.MustRegister(m.TotalRequestHeaders)
	prometheus.MustRegister(m.TotalRequestBody)
	prometheus.MustRegister(m.TotalRequestTrailers)
	prometheus.MustRegister(m.TotalResponseHeaders)
	prometheus.MustRegister(m.TotalResponseBody)
	prometheus.MustRegister(m.TotalResponseTrailers)
	prometheus.MustRegister(m.TotalEmptyResponses)
	prometheus.MustRegister(m.ResponseSendErrors)
	return m
}
