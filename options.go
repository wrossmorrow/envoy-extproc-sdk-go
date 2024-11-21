package extproc

// Processing options specific to the external processor.
type ProcessingOptions struct {
	LogStream                 bool   // Log "stream" events, i.e. Process calls
	LogPhases                 bool   // Log "phase" events, i.e. specific stream messages. Unsafe for production, prints all data.
	UpdateExtProcHeader       bool   // Update a `x-extproc-names` header with the extproc name
	UpdateDurationHeader      bool   // Update a `x-extproc-duration-ns` header with extproc duration (not request duration)
	RequestIdHeaderName       string // Header name to use for request ID's
	RequestIdFallback         string // Fallback value for a request id that does not exist (default empty string)
	BufferStreamedBodies      bool   // Whether to buffer request/response bodies internally, instead of in envoy
	PerRequestBodyBufferBytes int64  // Maximum allowed size of body buffers, ignored if not buffering (-1 for no limit); cast to a uint32
	DecompressBodies          bool   // Flag to denote if the SDK itself should decompress bodies for processing, if possible and applicable
}

// Return default options, as not all the zero values are "correct".
func NewDefaultOptions() *ProcessingOptions {
	return &ProcessingOptions{
		RequestIdHeaderName: "x-request-id",
	}
}
