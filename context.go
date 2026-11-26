package extproc

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extproccfgv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	REQUEST_PHASE_UNDETERMINED      = 0
	REQUEST_PHASE_REQUEST_HEADERS   = 1
	REQUEST_PHASE_REQUEST_BODY      = 2
	REQUEST_PHASE_REQUEST_TRAILERS  = 3
	REQUEST_PHASE_RESPONSE_HEADERS  = 4
	REQUEST_PHASE_RESPONSE_BODY     = 5
	REQUEST_PHASE_RESPONSE_TRAILERS = 6
)

func defaultPhaseIfInvalid(phase int) int {
	switch phase {
	case REQUEST_PHASE_UNDETERMINED, REQUEST_PHASE_REQUEST_HEADERS, REQUEST_PHASE_REQUEST_BODY, REQUEST_PHASE_REQUEST_TRAILERS, REQUEST_PHASE_RESPONSE_HEADERS, REQUEST_PHASE_RESPONSE_BODY, REQUEST_PHASE_RESPONSE_TRAILERS:
		return phase
	default:
		return REQUEST_PHASE_UNDETERMINED
	}
}

func RequestPhaseToString(phase int) string {
	switch phase {
	case REQUEST_PHASE_UNDETERMINED:
		return "UNDETERMINED"
	case REQUEST_PHASE_REQUEST_HEADERS:
		return "REQUEST_HEADERS"
	case REQUEST_PHASE_REQUEST_BODY:
		return "REQUEST_BODY"
	case REQUEST_PHASE_REQUEST_TRAILERS:
		return "REQUEST_TRAILERS"
	case REQUEST_PHASE_RESPONSE_HEADERS:
		return "RESPONSE_HEADERS"
	case REQUEST_PHASE_RESPONSE_BODY:
		return "RESPONSE_BODY"
	case REQUEST_PHASE_RESPONSE_TRAILERS:
		return "RESPONSE_TRAILERS"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", phase)
	}
}

const kContentLength = "content-length"

// HeaderSendMode controls whether Envoy sends headers or trailers in subsequent
// phases of the current HTTP stream.
type HeaderSendMode int32

const (
	HeaderModeDefault HeaderSendMode = HeaderSendMode(extproccfgv3.ProcessingMode_DEFAULT)
	HeaderModeSend    HeaderSendMode = HeaderSendMode(extproccfgv3.ProcessingMode_SEND)
	HeaderModeSkip    HeaderSendMode = HeaderSendMode(extproccfgv3.ProcessingMode_SKIP)
)

// BodySendMode controls how Envoy sends bodies in subsequent phases of the
// current HTTP stream.
//
// Envoy's ProcessingMode proto has no "keep default" body enum: BodyModeNone (0)
// instructs Envoy not to send that body, so callers overriding modes during
// ProcessRequestHeaders should specify both RequestBodyMode and ResponseBodyMode.
type BodySendMode int32

const (
	BodyModeNone               BodySendMode = BodySendMode(extproccfgv3.ProcessingMode_NONE)
	BodyModeStreamed           BodySendMode = BodySendMode(extproccfgv3.ProcessingMode_STREAMED)
	BodyModeBuffered           BodySendMode = BodySendMode(extproccfgv3.ProcessingMode_BUFFERED)
	BodyModeBufferedPartial    BodySendMode = BodySendMode(extproccfgv3.ProcessingMode_BUFFERED_PARTIAL)
	BodyModeFullDuplexStreamed BodySendMode = BodySendMode(extproccfgv3.ProcessingMode_FULL_DUPLEX_STREAMED)
	BodyModeGRPC               BodySendMode = BodySendMode(extproccfgv3.ProcessingMode_GRPC)
)

// ModeOverride overrides Envoy's static processing_mode for the remainder of the
// current HTTP transaction (requires allow_mode_override: true in Envoy).
// RequestHeaderMode is omitted because Envoy ignores it on mode_override.
type ModeOverride struct {
	ResponseHeaderMode  HeaderSendMode
	RequestBodyMode     BodySendMode
	ResponseBodyMode    BodySendMode
	RequestTrailerMode  HeaderSendMode
	ResponseTrailerMode HeaderSendMode
}

// ToEnvoyProcessingMode validates m and converts it to Envoy's ProcessingMode proto.
func (m ModeOverride) ToEnvoyProcessingMode() (*extproccfgv3.ProcessingMode, error) {
	pm := &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_DEFAULT,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_HeaderSendMode(m.ResponseHeaderMode),
		RequestBodyMode:     extproccfgv3.ProcessingMode_BodySendMode(m.RequestBodyMode),
		ResponseBodyMode:    extproccfgv3.ProcessingMode_BodySendMode(m.ResponseBodyMode),
		RequestTrailerMode:  extproccfgv3.ProcessingMode_HeaderSendMode(m.RequestTrailerMode),
		ResponseTrailerMode: extproccfgv3.ProcessingMode_HeaderSendMode(m.ResponseTrailerMode),
	}
	if err := pm.ValidateAll(); err != nil {
		return nil, fmt.Errorf("invalid mode override: %w", err)
	}
	return pm, nil
}

// ProtocolConfig describes the Envoy ext_proc filter's protocol configuration
// for the current stream, captured from the first ProcessingRequest message.
type ProtocolConfig struct {
	RequestBodyMode                         BodySendMode
	ResponseBodyMode                        BodySendMode
	SendBodyWithoutWaitingForHeaderResponse bool
}

func protocolConfigFromProto(pc *extprocv3.ProtocolConfiguration) *ProtocolConfig {
	if pc == nil {
		return nil
	}
	return &ProtocolConfig{
		RequestBodyMode:                         BodySendMode(pc.GetRequestBodyMode()),
		ResponseBodyMode:                        BodySendMode(pc.GetResponseBodyMode()),
		SendBodyWithoutWaitingForHeaderResponse: pc.GetSendBodyWithoutWaitingForHeaderResponse(),
	}
}

type PhaseResponse struct {
	headerMutation    *extprocv3.HeaderMutation    // any response
	bodyMutation      *extprocv3.BodyMutation      // body responses
	continueRequest   *extprocv3.CommonResponse    // headers/body responses
	immediateResponse *extprocv3.ImmediateResponse // headers/body responses
	dynamicMetadata   *structpb.Struct             // any response (including ImmediateResponse)
	modeOverride      *extproccfgv3.ProcessingMode // header responses only
	clearRouteCache   bool
}

type HeaderValue struct {
	Value    string
	RawValue []byte
}

func (hv HeaderValue) IsValid() bool {
	if len(hv.Value) > 0 && hv.RawValue != nil {
		return false
	}
	return true
}

func (hv HeaderValue) ToEnvoyHeaderValue(name string) *corev3.HeaderValue {
	if len(hv.Value) > 0 && hv.RawValue == nil {
		return &corev3.HeaderValue{
			Key:      name,
			RawValue: []byte(hv.Value),
		}
	}
	return &corev3.HeaderValue{
		Key:      name,
		RawValue: hv.RawValue,
	}
}

func (hv HeaderValue) ToEnvoyHeaderValueOption(name string, action string) (*corev3.HeaderValueOption, error) {
	if !hv.IsValid() {
		return nil, fmt.Errorf("invalid header value for %s", name)
	}
	v, ok := corev3.HeaderValueOption_HeaderAppendAction_value[action]
	if !ok {
		return nil, fmt.Errorf("unknown header append action %q", action)
	}
	appendAction := corev3.HeaderValueOption_HeaderAppendAction(v)
	ho := &corev3.HeaderValueOption{
		Header:       hv.ToEnvoyHeaderValue(name),
		AppendAction: appendAction,
	}
	// Envoy's ext_proc MutationUtils::applyHeaderMutations evaluates deprecated
	// HeaderValueOption.append (defaulting to false) rather than append_action
	// (envoyproxy/envoy#48886). Setting Append=true for APPEND_IF_EXISTS_OR_ADD
	// preserves append behavior without colliding with non-zero AppendAction.
	if appendAction == corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD {
		ho.Append = wrapperspb.Bool(true)
	}
	return ho, nil
}

func BuildHeaderValuesFromMap(headers map[string]string) map[string]HeaderValue {
	h := make(map[string]HeaderValue)
	for n := range headers {
		h[n] = HeaderValue{RawValue: []byte(headers[n])}
	}
	return h
}

type RequestContext struct {
	// parsed from header
	Scheme    string
	Authority string
	Method    string
	Path      string
	FullPath  string
	RequestID string

	AllHeaders AllHeaders

	Started           time.Time
	Duration          time.Duration
	EndOfStream       bool
	ObservabilityMode bool
	ProtocolConfig    *ProtocolConfig
	Metadata          map[string]map[string]any
	TypedMetadata     map[string]*anypb.Any
	Attributes        map[string]any
	data              map[string]any

	phase           int
	response        PhaseResponse
	reqHeaderKeys   map[string]bool
	respHeaderKeys  map[string]bool
	phaseHeaderKeys map[string]bool

	sendTimeoutOverride func(resp *extprocv3.ProcessingResponse) error
	timeoutExtended     bool
}

func headerKeySet(h AllHeaders) map[string]bool {
	keys := make(map[string]bool, len(h.Headers))
	for k := range h.Headers {
		keys[strings.ToLower(k)] = true
	}
	return keys
}

// resolveHeaderMutation filters SetHeaders using known phase headers to enforce
// ADD_IF_ABSENT and OVERWRITE_IF_EXISTS semantics while Envoy's ext_proc filter
// ignores HeaderValueOption.append_action (envoyproxy/envoy#48886). known is
// never mutated; the returned map reflects the post-mutation key set.
func resolveHeaderMutation(hm *extprocv3.HeaderMutation, known map[string]bool) (*extprocv3.HeaderMutation, map[string]bool) {
	if hm == nil || known == nil {
		return hm, nil
	}

	state := maps.Clone(known)
	for _, rm := range hm.RemoveHeaders {
		k := strings.ToLower(rm)
		// Envoy ignores attempts to remove pseudo-headers or the host header.
		if !strings.HasPrefix(k, ":") && k != "host" {
			delete(state, k)
		}
	}

	filtered := make([]*corev3.HeaderValueOption, 0, len(hm.SetHeaders))
	for _, ho := range hm.SetHeaders {
		if ho == nil {
			continue
		}
		k := strings.ToLower(ho.GetHeader().GetKey())
		exists := state[k]
		switch ho.GetAppendAction() {
		case corev3.HeaderValueOption_ADD_IF_ABSENT:
			if exists {
				continue
			}
			state[k] = true
			filtered = append(filtered, ho)
		case corev3.HeaderValueOption_OVERWRITE_IF_EXISTS:
			if !exists {
				continue
			}
			filtered = append(filtered, ho)
		default:
			state[k] = true
			filtered = append(filtered, ho)
		}
	}

	return &extprocv3.HeaderMutation{
		SetHeaders:    filtered,
		RemoveHeaders: hm.RemoveHeaders,
	}, state
}

func (rc *RequestContext) resolvedHeaderMutation() (*extprocv3.HeaderMutation, map[string]bool) {
	return resolveHeaderMutation(rc.response.headerMutation, rc.phaseHeaderKeys)
}

// data must be allocated here, not in initReqCtx: that only runs in the
// request headers phase, which request_header_mode: SKIP never reaches.
func newRequestContext() *RequestContext {
	return &RequestContext{
		Metadata:      make(map[string]map[string]any),
		TypedMetadata: make(map[string]*anypb.Any),
		Attributes:    make(map[string]any),
		// for custom data between phases
		data: make(map[string]any),
	}
}

func (rc *RequestContext) mergeMetadataContext(md *corev3.Metadata) {
	if rc == nil || md == nil {
		return
	}
	if len(md.GetFilterMetadata()) > 0 {
		if rc.Metadata == nil {
			rc.Metadata = make(map[string]map[string]any, len(md.GetFilterMetadata()))
		}
		for ns, st := range md.GetFilterMetadata() {
			if st == nil {
				continue
			}
			nsMap := rc.Metadata[ns]
			if nsMap == nil {
				nsMap = make(map[string]any, len(st.GetFields()))
				rc.Metadata[ns] = nsMap
			}
			maps.Copy(nsMap, st.AsMap())
		}
	}
	if len(md.GetTypedFilterMetadata()) > 0 {
		if rc.TypedMetadata == nil {
			rc.TypedMetadata = make(map[string]*anypb.Any, len(md.GetTypedFilterMetadata()))
		}
		for ns, anyVal := range md.GetTypedFilterMetadata() {
			if anyVal == nil {
				continue
			}
			if cloned, ok := proto.Clone(anyVal).(*anypb.Any); ok {
				rc.TypedMetadata[ns] = cloned
			}
		}
	}
}

func (rc *RequestContext) mergeAttributes(attrs map[string]*structpb.Struct) {
	if rc == nil || len(attrs) == 0 {
		return
	}
	if rc.Attributes == nil {
		rc.Attributes = make(map[string]any)
	}
	for _, ns := range slices.Sorted(maps.Keys(attrs)) {
		st := attrs[ns]
		if st == nil {
			continue
		}
		maps.Copy(rc.Attributes, st.AsMap())
	}
}

func initReqCtx(rc *RequestContext, headers *corev3.HeaderMap) error {
	rc.Started = time.Now()
	rc.Duration = 0

	eitherValue := func(h *corev3.HeaderValue) string {
		if h == nil {
			return ""
		}

		val := h.Value
		if len(h.RawValue) > 0 {
			val = string(h.RawValue)
		}
		return val
	}

	// for stream phase responses (convenience)
	rc.ResetPhase()

	// string and byte header processing

	var err error
	rc.AllHeaders, err = genHeaders(headers)
	if err != nil {
		return fmt.Errorf("parsing headers failed: %w", err)
	}
	rc.reqHeaderKeys = headerKeySet(rc.AllHeaders)
	rc.phaseHeaderKeys = rc.reqHeaderKeys

	for _, h := range headers.Headers {
		switch h.Key {
		case ":scheme":
			rc.Scheme = eitherValue(h)

		case ":authority":
			rc.Authority = eitherValue(h)

		case ":method":
			rc.Method = eitherValue(h)

		case ":path":
			rc.FullPath = eitherValue(h)
			rc.Path = strings.Split(rc.FullPath, "?")[0]

		case "x-request-id":
			rc.RequestID = eitherValue(h)

		default:
		}
	}

	return nil
}

func (rc *RequestContext) GetValue(name string) (any, error) {
	val, exists := rc.data[name]
	if exists {
		return val, nil
	}
	return nil, errors.New(name + " does not exist")
}

func (rc *RequestContext) SetValue(name string, val any) error {
	rc.data[name] = val
	return nil
}

func (rc *RequestContext) ResetPhase() error {
	rc.phase = REQUEST_PHASE_UNDETERMINED
	rc.EndOfStream = false
	rc.ObservabilityMode = false
	rc.phaseHeaderKeys = nil
	rc.response.headerMutation = &extprocv3.HeaderMutation{}
	rc.response.bodyMutation = nil
	rc.response.continueRequest = nil
	rc.response.immediateResponse = nil
	rc.response.dynamicMetadata = nil
	rc.response.modeOverride = nil
	rc.response.clearRouteCache = false
	rc.timeoutExtended = false
	return nil
}

func (rc *RequestContext) setPhase(phase int) {
	rc.phase = defaultPhaseIfInvalid(phase)
}

func (rc *RequestContext) GetPhase() int {
	return rc.phase
}

func (rc *RequestContext) GetPhaseName() string {
	return RequestPhaseToString(rc.phase)
}

func (rc *RequestContext) ContinueRequest() error {
	if rc.response.immediateResponse != nil {
		rc.response.immediateResponse = nil
	}

	hm, _ := rc.resolvedHeaderMutation()
	rc.response.continueRequest = &extprocv3.CommonResponse{
		// status? (ie response phase status)
		HeaderMutation: hm,
		BodyMutation:   rc.response.bodyMutation,
		// trailers?
		ClearRouteCache: rc.response.clearRouteCache,
	}

	return nil
}

func (rc *RequestContext) CancelRequest(status int32, headers map[string]HeaderValue, body string) error {
	if err := rc.AppendHeaders(headers); err != nil {
		return err
	}
	rc.response.continueRequest = nil
	rc.response.immediateResponse = &extprocv3.ImmediateResponse{
		Status: &typev3.HttpStatus{
			Code: typev3.StatusCode(status),
		},
		Headers: rc.response.headerMutation,
		Body:    []byte(body),
	}
	return nil
}

func (rc *RequestContext) ClearRouteCache() {
	rc.response.clearRouteCache = true
}

func (rc *RequestContext) UnClearRouteCache() {
	rc.response.clearRouteCache = false
}

// OverrideMode sets ProcessingResponse.mode_override on the current header phase.
// It returns an error if called during a non-header phase or if any mode enum is invalid.
func (rc *RequestContext) OverrideMode(m ModeOverride) error {
	switch rc.phase {
	case REQUEST_PHASE_REQUEST_HEADERS, REQUEST_PHASE_RESPONSE_HEADERS, REQUEST_PHASE_UNDETERMINED:
	default:
		return fmt.Errorf("mode_override is only supported during header phases, not %s", rc.GetPhaseName())
	}
	pm, err := m.ToEnvoyProcessingMode()
	if err != nil {
		return err
	}
	rc.response.modeOverride = pm
	return nil
}

// ClearModeOverride removes any pending mode override for the current phase.
func (rc *RequestContext) ClearModeOverride() {
	rc.response.modeOverride = nil
}

// SkipRemainingPhases configures mode_override to skip all subsequent header,
// body, and trailer phases for the current HTTP transaction.
func (rc *RequestContext) SkipRemainingPhases() error {
	return rc.OverrideMode(ModeOverride{
		ResponseHeaderMode:  HeaderModeSkip,
		RequestBodyMode:     BodyModeNone,
		ResponseBodyMode:    BodyModeNone,
		RequestTrailerMode:  HeaderModeSkip,
		ResponseTrailerMode: HeaderModeSkip,
	})
}

// DefaultModeOverride returns a ModeOverride pre-populated with Envoy's
// configured RequestBodyMode and ResponseBodyMode from ProtocolConfig (when
// present) and HeaderModeDefault for header and trailer modes.
//
// Because Envoy's BodySendMode has no "keep default" enum (BodyModeNone is 0),
// callers overriding a single body mode during ProcessRequestHeaders can start
// from DefaultModeOverride() to preserve the other direction's body mode.
func (rc *RequestContext) DefaultModeOverride() ModeOverride {
	if rc == nil || rc.ProtocolConfig == nil {
		return ModeOverride{}
	}
	return ModeOverride{
		ResponseHeaderMode:  HeaderModeDefault,
		RequestBodyMode:     rc.ProtocolConfig.RequestBodyMode,
		ResponseBodyMode:    rc.ProtocolConfig.ResponseBodyMode,
		RequestTrailerMode:  HeaderModeDefault,
		ResponseTrailerMode: HeaderModeDefault,
	}
}

// SetDynamicMetadata sets key=value under namespace in the current phase's
// ProcessingResponse.dynamic_metadata. Envoy only accepts namespaces listed in
// metadata_options.receiving_namespaces.untyped on the ext_proc filter.
func (rc *RequestContext) SetDynamicMetadata(namespace, key string, value any) error {
	if namespace == "" {
		return errors.New("dynamic metadata namespace cannot be empty")
	}
	if key == "" {
		return errors.New("dynamic metadata key cannot be empty")
	}
	pbVal, err := structpb.NewValue(value)
	if err != nil {
		return fmt.Errorf("invalid dynamic metadata value for %s.%s: %w", namespace, key, err)
	}
	nsStruct := rc.ensureDynamicMetadataNamespace(namespace)
	nsStruct.Fields[key] = pbVal
	return nil
}

// SetDynamicMetadataNamespace merges values under namespace in the current
// phase's ProcessingResponse.dynamic_metadata. Validation is all-or-nothing:
// if namespace or any key is empty, or any value cannot be converted via
// structpb.NewValue, no changes are applied.
func (rc *RequestContext) SetDynamicMetadataNamespace(namespace string, values map[string]any) error {
	if namespace == "" {
		return errors.New("dynamic metadata namespace cannot be empty")
	}
	converted := make(map[string]*structpb.Value, len(values))
	for k, v := range values {
		if k == "" {
			return errors.New("dynamic metadata key cannot be empty")
		}
		pbVal, err := structpb.NewValue(v)
		if err != nil {
			return fmt.Errorf("invalid dynamic metadata value for %s.%s: %w", namespace, k, err)
		}
		converted[k] = pbVal
	}
	nsStruct := rc.ensureDynamicMetadataNamespace(namespace)
	maps.Copy(nsStruct.Fields, converted)
	return nil
}

// ClearDynamicMetadata removes all pending outbound dynamic metadata for the current phase.
func (rc *RequestContext) ClearDynamicMetadata() {
	rc.response.dynamicMetadata = nil
}

func (rc *RequestContext) ensureDynamicMetadataNamespace(namespace string) *structpb.Struct {
	if rc.response.dynamicMetadata == nil {
		rc.response.dynamicMetadata = &structpb.Struct{
			Fields: make(map[string]*structpb.Value),
		}
	} else if rc.response.dynamicMetadata.Fields == nil {
		rc.response.dynamicMetadata.Fields = make(map[string]*structpb.Value)
	}
	if existingVal, ok := rc.response.dynamicMetadata.Fields[namespace]; ok && existingVal.GetStructValue() != nil {
		existing := existingVal.GetStructValue()
		if existing.Fields == nil {
			existing.Fields = make(map[string]*structpb.Value)
		}
		return existing
	}
	nsStruct := &structpb.Struct{
		Fields: make(map[string]*structpb.Value),
	}
	rc.response.dynamicMetadata.Fields[namespace] = structpb.NewStructValue(nsStruct)
	return nsStruct
}

func (rc *RequestContext) newTimeoutOverrideResponse(phase int, timeout time.Duration) (*extprocv3.ProcessingResponse, error) {
	if timeout < time.Millisecond {
		return nil, fmt.Errorf("override_message_timeout must be >= 1ms, got %s", timeout)
	}
	resp, err := rc.EmptyContinueResponse(phase)
	if err != nil {
		return nil, fmt.Errorf("override_message_timeout requires an active request phase: %w", err)
	}
	resp.OverrideMessageTimeout = durationpb.New(timeout)
	return resp, nil
}

// OverrideMessageTimeout immediately sends an interim ProcessingResponse
// asking Envoy to replace the current phase's message_timeout timer with
// timeout (requires max_message_timeout >= timeout >= 1ms on Envoy's ext_proc
// filter). It may be called at most once per phase before the processor
// returns its phase response, and is a no-op when ObservabilityMode is true.
//
// Warning: ext_proc sits inline on Envoy's data-plane hot path. Extending
// per-message timeouts holds downstream connections, Envoy filter state, and
// any BUFFERED HTTP bodies in memory, sends an extra gRPC message on the
// stream, and multiplies concurrent active streams under load (Little's Law).
// Reserve this for rare slow-path operations and size ServerOptions.MaxConcurrentStreams,
// Envoy cluster circuit breakers, and route timeouts accordingly.
func (rc *RequestContext) OverrideMessageTimeout(timeout time.Duration) error {
	if timeout < time.Millisecond {
		return fmt.Errorf("override_message_timeout must be >= 1ms, got %s", timeout)
	}
	if rc.phase == REQUEST_PHASE_UNDETERMINED {
		return errors.New("override_message_timeout requires an active request phase")
	}
	if rc.timeoutExtended {
		return errors.New("override_message_timeout already sent in current phase")
	}
	if rc.ObservabilityMode {
		rc.timeoutExtended = true
		return nil
	}
	if rc.sendTimeoutOverride == nil {
		return errors.New("override_message_timeout requires an active gRPC stream")
	}
	resp, err := rc.newTimeoutOverrideResponse(rc.phase, timeout)
	if err != nil {
		return err
	}
	if sendErr := rc.sendTimeoutOverride(resp); sendErr != nil {
		return sendErr
	}
	rc.timeoutExtended = true
	return nil
}

func (rc *RequestContext) GetResponse(phase int) (*extprocv3.ProcessingResponse, error) {

	// handle immediate responses
	if rc.response.immediateResponse != nil {
		switch phase {
		case REQUEST_PHASE_REQUEST_HEADERS, REQUEST_PHASE_REQUEST_BODY, REQUEST_PHASE_RESPONSE_HEADERS, REQUEST_PHASE_RESPONSE_BODY:
			// TODO: post-process modifications?
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ImmediateResponse{
					ImmediateResponse: rc.response.immediateResponse,
				},
				DynamicMetadata: rc.response.dynamicMetadata,
			}, nil

		// trailers phases don't have an ImmediateResponse option
		// (only changes to headers permitted)
		default:
		}
	}

	// handle "common" responses (immediateResponse == nil and/or phase ignored)

	// Rebuild the common response unconditionally. Mutations can be added after
	// the processor returns - the extproc name and duration headers, say - and
	// this is what folds them in. It also defaults a processor that set nothing
	// at all to "continue".
	if err := rc.ContinueRequest(); err != nil {
		return nil, err
	}

	switch phase {
	case REQUEST_PHASE_REQUEST_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: rc.response.continueRequest,
				},
			},
			DynamicMetadata: rc.response.dynamicMetadata,
			ModeOverride:    rc.response.modeOverride,
		}, nil

	case REQUEST_PHASE_REQUEST_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{
					Response: rc.response.continueRequest,
				},
			},
			DynamicMetadata: rc.response.dynamicMetadata,
		}, nil

	case REQUEST_PHASE_REQUEST_TRAILERS:
		hm, _ := rc.resolvedHeaderMutation()
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestTrailers{
				RequestTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: hm,
				},
			},
			DynamicMetadata: rc.response.dynamicMetadata,
		}, nil

	case REQUEST_PHASE_RESPONSE_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{
					Response: rc.response.continueRequest,
				},
			},
			DynamicMetadata: rc.response.dynamicMetadata,
			ModeOverride:    rc.response.modeOverride,
		}, nil

	case REQUEST_PHASE_RESPONSE_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: rc.response.continueRequest,
				},
			},
			DynamicMetadata: rc.response.dynamicMetadata,
		}, nil

	case REQUEST_PHASE_RESPONSE_TRAILERS:
		hm, _ := rc.resolvedHeaderMutation()
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseTrailers{
				ResponseTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: hm,
				},
			},
			DynamicMetadata: rc.response.dynamicMetadata,
		}, nil

	default:
		return nil, errors.New("unknown request phase")
	}
}

func (rc *RequestContext) EmptyContinueResponse(phase int) (*extprocv3.ProcessingResponse, error) {
	switch phase {
	case REQUEST_PHASE_REQUEST_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_REQUEST_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_REQUEST_TRAILERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestTrailers{
				RequestTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: &extprocv3.HeaderMutation{},
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_HEADERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_BODY:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{},
				},
			},
		}, nil

	case REQUEST_PHASE_RESPONSE_TRAILERS:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseTrailers{
				ResponseTrailers: &extprocv3.TrailersResponse{
					HeaderMutation: &extprocv3.HeaderMutation{},
				},
			},
		}, nil

	default:
		return nil, errors.New("unknown request phase")
	}
}

func (rc *RequestContext) UpdateHeader(name string, hv HeaderValue, action string) error {
	hm := rc.response.headerMutation
	ho, err := hv.ToEnvoyHeaderValueOption(name, action)
	if err != nil {
		return err
	}
	hm.SetHeaders = append(hm.SetHeaders, ho)
	return nil
}

func (rc *RequestContext) AppendHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "APPEND_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) AddHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "ADD_IF_ABSENT")
}

func (rc *RequestContext) OverwriteHeader(name string, hv HeaderValue) error {
	return rc.UpdateHeader(name, hv, "OVERWRITE_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) UpdateHeaders(headers map[string]HeaderValue, action string) error {
	hos := make([]*corev3.HeaderValueOption, 0, len(headers))
	for n, hv := range headers {
		ho, err := hv.ToEnvoyHeaderValueOption(n, action)
		if err != nil {
			return err
		}
		hos = append(hos, ho)
	}
	rc.response.headerMutation.SetHeaders = append(rc.response.headerMutation.SetHeaders, hos...)
	return nil
}

func (rc *RequestContext) AppendHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "APPEND_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) AddHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "ADD_IF_ABSENT")
}

func (rc *RequestContext) OverwriteHeaders(headers map[string]HeaderValue) error {
	return rc.UpdateHeaders(headers, "OVERWRITE_IF_EXISTS_OR_ADD")
}

func (rc *RequestContext) RemoveHeader(name string) error {
	hm := rc.response.headerMutation
	if !slices.Contains(hm.RemoveHeaders, name) {
		hm.RemoveHeaders = append(hm.RemoveHeaders, name)
	}
	return nil
}

func (rc *RequestContext) RemoveHeaders(headers []string) error {
	hm := rc.response.headerMutation
	for _, h := range headers {
		if !slices.Contains(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}

func (rc *RequestContext) RemoveHeadersVariadic(headers ...string) error {
	hm := rc.response.headerMutation
	for _, h := range headers {
		if !slices.Contains(hm.RemoveHeaders, h) {
			hm.RemoveHeaders = append(hm.RemoveHeaders, h)
		}
	}
	return nil
}

// ReplaceBodyChunk replaces the body bytes of the current message.
//
// It deliberately leaves Content-Length alone: under a STREAMED body mode the
// chunk is only part of the body, so its length is not the message length. Use
// ReplaceBody when the filter is configured BUFFERED and this call carries the
// whole body.
//
// An empty body is a no-op; use ClearBodyChunk or ClearBody to remove a body.
func (rc *RequestContext) ReplaceBodyChunk(body []byte) error {
	if len(body) == 0 {
		return nil
	}

	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: body,
		},
	}

	return nil
}

// ReplaceBody replaces the whole body and updates Content-Length to match.
//
// Only correct when the filter is configured BUFFERED, so that a single body
// message carries the entire body. Under STREAMED use ReplaceBodyChunk. Note
// too that a chunked HTTP/1.1 message carries no Content-Length at all, so the
// header written here is meaningless in that case.
func (rc *RequestContext) ReplaceBody(body []byte) error {
	if err := rc.ReplaceBodyChunk(body); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return rc.OverwriteHeader(kContentLength, HeaderValue{RawValue: []byte(strconv.Itoa(len(body)))})
}

// ClearBodyChunk removes the body bytes of the current message, leaving
// Content-Length alone. See ReplaceBodyChunk on why.
func (rc *RequestContext) ClearBodyChunk() error {
	rc.response.bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_ClearBody{
			ClearBody: true,
		},
	}
	return nil
}

// ClearBody removes the body and sets Content-Length to 0. See ReplaceBody on
// when this is the right call.
func (rc *RequestContext) ClearBody() error {
	if err := rc.ClearBodyChunk(); err != nil {
		return err
	}
	return rc.OverwriteHeader(kContentLength, HeaderValue{RawValue: []byte(strconv.Itoa(0))})
}
