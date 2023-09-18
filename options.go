package extproc

type ProcessingOptions struct {
	LogStream            bool
	LogPhases            bool
	UpdateExtProcHeader  bool
	UpdateDurationHeader bool
}

func NewOptions() *ProcessingOptions {
	return &ProcessingOptions{}
}
