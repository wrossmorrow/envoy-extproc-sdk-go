package extproc

type ServerOptions struct {
	ExtProcPort                   int
	MetricsHTTPPort               int
	MaxConcurrentStreams          uint32
	TerminationGracePeriodSeconds int
}

func NewDefaultServerOptions() *ServerOptions {
	return &ServerOptions{
		ExtProcPort:                   50051,
		MetricsHTTPPort:               9090,
		MaxConcurrentStreams:          1000,
		TerminationGracePeriodSeconds: 10,
	}
}

type ProcessingOptions struct {
	UpdateExtProcHeader     bool
	UpdateDurationHeader    bool
	AbortOnProcessorFailure bool
}

func NewDefaultOptions() *ProcessingOptions {
	return &ProcessingOptions{}
}
