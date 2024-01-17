package extproc

type ProcessingOptions struct {
	LogStream            bool
	LogPhases            bool
	UpdateExtProcHeader  bool
	UpdateDurationHeader bool
}

func NewDefaultOptions() *ProcessingOptions {
	return &ProcessingOptions{}
}
