package extproc

import "errors"

type ProcessingOptions struct {
	LogStream            bool
	LogPhases            bool
	UpdateExtProcHeader  bool
	UpdateDurationHeader bool
}

func DefaultOptions(opts *ProcessingOptions) error {
	if opts == nil {
		return errors.New("cannot set default options without a reference")
	}
	opts.LogStream = false
	opts.LogPhases = false
	opts.UpdateExtProcHeader = false
	opts.UpdateDurationHeader = false
	return nil
}

func NewOptions() *ProcessingOptions {
	opts := &ProcessingOptions{}
	DefaultOptions(opts)
	return opts
}
