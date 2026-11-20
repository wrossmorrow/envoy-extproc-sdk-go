package conformance

import (
	"fmt"
	"net"
	"time"

	bootstrapv3 "github.com/envoyproxy/go-control-plane/envoy/config/bootstrap/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	mutationrulesv3 "github.com/envoyproxy/go-control-plane/envoy/config/common/mutation_rules/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extproccfgv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	routerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpupstreamsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	extProcFilterName                 = "envoy.filters.http.ext_proc"
	extProcShortTimeoutFailClosedName = "extproc_short_timeout_fail_closed"
	extProcShortTimeoutFailOpenName   = "extproc_short_timeout_fail_open"
	extProcAbortFilterName            = "extproc_abort_server"
	extProcChainFirstFilterName       = "extproc_chain_first"
	extProcChainSecondFilterName      = "extproc_chain_second"
	extProcObservabilityFilterName    = "extproc_observability"
	extProcClusterDefault             = "extproc_service"
	extProcClusterAbort               = "extproc_abort_service"
	extProcClusterSecondary           = "extproc_secondary_service"
)

// BootstrapParams configures the ports and host address used to generate an
// Envoy bootstrap configuration for the conformance harness.
type BootstrapParams struct {
	UpstreamHost          string
	PrimaryUpstreamPort   int
	SecondaryUpstreamPort int
	ExtProcPort           int
	AbortExtProcPort      int
	SecondaryExtProcPort  int
	ListenerPort          int
	AdminPort             int
}

func mustAny(msg proto.Message) (*anypb.Any, error) {
	a, err := anypb.New(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal Any(%T): %w", msg, err)
	}
	return a, nil
}

func makeEndpointCluster(name, host string, port int, httpOpts *httpupstreamsv3.HttpProtocolOptions) (*clusterv3.Cluster, error) {
	anyOpts, err := mustAny(httpOpts)
	if err != nil {
		return nil, err
	}

	c := &clusterv3.Cluster{
		Name:           name,
		ConnectTimeout: durationpb.New(2 * time.Second),
		LbPolicy:       clusterv3.Cluster_ROUND_ROBIN,
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: name,
			Endpoints: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: []*endpointv3.LbEndpoint{{
					HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
						Endpoint: &endpointv3.Endpoint{
							Address: &corev3.Address{
								Address: &corev3.Address_SocketAddress{
									SocketAddress: &corev3.SocketAddress{
										Address: host,
										PortSpecifier: &corev3.SocketAddress_PortValue{
											PortValue: uint32(port),
										},
									},
								},
							},
						},
					},
				}},
			}},
		},
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": anyOpts,
		},
	}

	if net.ParseIP(host) != nil {
		c.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC}
	} else {
		c.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_LOGICAL_DNS}
		c.DnsLookupFamily = clusterv3.Cluster_V4_ONLY
	}
	return c, nil
}

func bufferedProcessingMode() *extproccfgv3.ProcessingMode {
	return &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_SEND,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_SEND,
		RequestBodyMode:     extproccfgv3.ProcessingMode_BUFFERED,
		ResponseBodyMode:    extproccfgv3.ProcessingMode_BUFFERED,
		RequestTrailerMode:  extproccfgv3.ProcessingMode_SKIP,
		ResponseTrailerMode: extproccfgv3.ProcessingMode_SKIP,
	}
}

func streamedProcessingMode() *extproccfgv3.ProcessingMode {
	return &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_SEND,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_SEND,
		RequestBodyMode:     extproccfgv3.ProcessingMode_STREAMED,
		ResponseBodyMode:    extproccfgv3.ProcessingMode_STREAMED,
		RequestTrailerMode:  extproccfgv3.ProcessingMode_SKIP,
		ResponseTrailerMode: extproccfgv3.ProcessingMode_SKIP,
	}
}

func makeExtProcFilter(name, clusterName string, failureModeAllow bool, messageTimeout time.Duration, observability, disabledByDefault bool, mode *extproccfgv3.ProcessingMode) (*hcmv3.HttpFilter, error) {
	cfgAny, err := mustAny(&extproccfgv3.ExternalProcessor{
		GrpcService: &corev3.GrpcService{
			TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
				EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
					ClusterName: clusterName,
				},
			},
			Timeout: durationpb.New(30 * time.Second),
		},
		FailureModeAllow:  failureModeAllow,
		ObservabilityMode: observability,
		MessageTimeout:    durationpb.New(messageTimeout),
		MutationRules: &mutationrulesv3.HeaderMutationRules{
			AllowAllRouting: wrapperspb.Bool(true),
		},
		ProcessingMode: mode,
	})
	if err != nil {
		return nil, err
	}
	return &hcmv3.HttpFilter{
		Name:       name,
		Disabled:   disabledByDefault,
		ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: cfgAny},
	}, nil
}

func makeOverridesAny(mode *extproccfgv3.ProcessingMode, failureModeAllow *bool) (*anypb.Any, error) {
	overrides := &extproccfgv3.ExtProcOverrides{
		ProcessingMode: mode,
	}
	if failureModeAllow != nil {
		overrides.FailureModeAllow = wrapperspb.Bool(*failureModeAllow)
	}
	return mustAny(&extproccfgv3.ExtProcPerRoute{
		Override: &extproccfgv3.ExtProcPerRoute_Overrides{
			Overrides: overrides,
		},
	})
}

func makeModeRoute(prefix string, mode *extproccfgv3.ProcessingMode) (*routev3.Route, error) {
	perRouteAny, err := makeOverridesAny(mode, nil)
	if err != nil {
		return nil, err
	}
	return &routev3.Route{
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: prefix},
		},
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "upstream_primary"},
			},
		},
		TypedPerFilterConfig: map[string]*anypb.Any{
			extProcFilterName: perRouteAny,
		},
	}, nil
}

func makeProfileRoute(prefix string, disabledDefault *anypb.Any, enableFilters ...string) (*routev3.Route, error) {
	return makeProfileRouteWithOverrides(prefix, disabledDefault, nil, enableFilters...)
}

func makeProfileRouteWithOverrides(prefix string, disabledDefault *anypb.Any, failureModeAllow *bool, enableFilters ...string) (*routev3.Route, error) {
	return makeProfileRouteWithMode(prefix, disabledDefault, bufferedProcessingMode(), failureModeAllow, enableFilters...)
}

func makeProfileRouteWithMode(prefix string, disabledDefault *anypb.Any, mode *extproccfgv3.ProcessingMode, failureModeAllow *bool, enableFilters ...string) (*routev3.Route, error) {
	perFilter := map[string]*anypb.Any{
		extProcFilterName: disabledDefault,
	}
	enableAny, err := makeOverridesAny(mode, failureModeAllow)
	if err != nil {
		return nil, err
	}
	for _, f := range enableFilters {
		perFilter[f] = enableAny
	}
	return &routev3.Route{
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: prefix},
		},
		Action: &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "upstream_primary"},
			},
		},
		TypedPerFilterConfig: perFilter,
	}, nil
}

// BuildBootstrap constructs and PGV-validates an Envoy v3 Bootstrap config
// covering per-route processing modes (buffered, streamed, trailers, none,
// buffered-partial, skip-request-headers, disabled), failure/timeout/chaining/
// observability profiles, and dual HTTP/1.1 + h2c traffic.
func BuildBootstrap(p BootstrapParams) (*bootstrapv3.Bootstrap, []byte, error) {
	if p.UpstreamHost == "" {
		p.UpstreamHost = "127.0.0.1"
	}
	if p.AbortExtProcPort == 0 {
		p.AbortExtProcPort = p.ExtProcPort
	}
	if p.SecondaryExtProcPort == 0 {
		p.SecondaryExtProcPort = p.ExtProcPort
	}

	downstreamProtoOpts := &httpupstreamsv3.HttpProtocolOptions{
		UpstreamProtocolOptions: &httpupstreamsv3.HttpProtocolOptions_UseDownstreamProtocolConfig{
			UseDownstreamProtocolConfig: &httpupstreamsv3.HttpProtocolOptions_UseDownstreamHttpConfig{
				HttpProtocolOptions:  &corev3.Http1ProtocolOptions{EnableTrailers: true},
				Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
			},
		},
	}

	grpcProtoOpts := &httpupstreamsv3.HttpProtocolOptions{
		UpstreamProtocolOptions: &httpupstreamsv3.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &httpupstreamsv3.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &httpupstreamsv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
					Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
				},
			},
		},
	}

	primaryCluster, err := makeEndpointCluster("upstream_primary", p.UpstreamHost, p.PrimaryUpstreamPort, downstreamProtoOpts)
	if err != nil {
		return nil, nil, err
	}
	secondaryCluster, err := makeEndpointCluster("upstream_secondary", p.UpstreamHost, p.SecondaryUpstreamPort, downstreamProtoOpts)
	if err != nil {
		return nil, nil, err
	}
	extprocCluster, err := makeEndpointCluster(extProcClusterDefault, p.UpstreamHost, p.ExtProcPort, grpcProtoOpts)
	if err != nil {
		return nil, nil, err
	}
	abortExtprocCluster, err := makeEndpointCluster(extProcClusterAbort, p.UpstreamHost, p.AbortExtProcPort, grpcProtoOpts)
	if err != nil {
		return nil, nil, err
	}
	secondaryExtprocCluster, err := makeEndpointCluster(extProcClusterSecondary, p.UpstreamHost, p.SecondaryExtProcPort, grpcProtoOpts)
	if err != nil {
		return nil, nil, err
	}

	disabledPerRouteAny, err := mustAny(&extproccfgv3.ExtProcPerRoute{
		Override: &extproccfgv3.ExtProcPerRoute_Disabled{Disabled: true},
	})
	if err != nil {
		return nil, nil, err
	}

	bufferedRoute, err := makeModeRoute("/mode/buffered", bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}

	bufferedPartialRoute, err := makeModeRoute("/mode/buffered-partial", &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_SEND,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_SEND,
		RequestBodyMode:     extproccfgv3.ProcessingMode_BUFFERED_PARTIAL,
		ResponseBodyMode:    extproccfgv3.ProcessingMode_BUFFERED_PARTIAL,
		RequestTrailerMode:  extproccfgv3.ProcessingMode_SKIP,
		ResponseTrailerMode: extproccfgv3.ProcessingMode_SKIP,
	})
	if err != nil {
		return nil, nil, err
	}
	bufferedPartialRoute.PerRequestBufferLimitBytes = wrapperspb.UInt32(64)

	streamedRoute, err := makeModeRoute("/mode/streamed", streamedProcessingMode())
	if err != nil {
		return nil, nil, err
	}

	trailersRoute, err := makeModeRoute("/mode/trailers", &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_SEND,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_SEND,
		RequestBodyMode:     extproccfgv3.ProcessingMode_STREAMED,
		ResponseBodyMode:    extproccfgv3.ProcessingMode_STREAMED,
		RequestTrailerMode:  extproccfgv3.ProcessingMode_SEND,
		ResponseTrailerMode: extproccfgv3.ProcessingMode_SEND,
	})
	if err != nil {
		return nil, nil, err
	}

	noneRoute, err := makeModeRoute("/mode/none", &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_SEND,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_SEND,
		RequestBodyMode:     extproccfgv3.ProcessingMode_NONE,
		ResponseBodyMode:    extproccfgv3.ProcessingMode_NONE,
		RequestTrailerMode:  extproccfgv3.ProcessingMode_SKIP,
		ResponseTrailerMode: extproccfgv3.ProcessingMode_SKIP,
	})
	if err != nil {
		return nil, nil, err
	}

	skipReqHeadersRoute, err := makeModeRoute("/mode/skip-req-headers", &extproccfgv3.ProcessingMode{
		RequestHeaderMode:   extproccfgv3.ProcessingMode_SKIP,
		ResponseHeaderMode:  extproccfgv3.ProcessingMode_SEND,
		RequestBodyMode:     extproccfgv3.ProcessingMode_NONE,
		ResponseBodyMode:    extproccfgv3.ProcessingMode_BUFFERED,
		RequestTrailerMode:  extproccfgv3.ProcessingMode_SKIP,
		ResponseTrailerMode: extproccfgv3.ProcessingMode_SKIP,
	})
	if err != nil {
		return nil, nil, err
	}

	timeoutClosedRoute, err := makeProfileRoute("/profile/timeout-fail-closed", disabledPerRouteAny, extProcShortTimeoutFailClosedName)
	if err != nil {
		return nil, nil, err
	}
	timeoutOpenRoute, err := makeProfileRoute("/profile/timeout-fail-open", disabledPerRouteAny, extProcShortTimeoutFailOpenName)
	if err != nil {
		return nil, nil, err
	}
	abortClosedRoute, err := makeProfileRoute("/profile/abort-fail-closed", disabledPerRouteAny, extProcAbortFilterName)
	if err != nil {
		return nil, nil, err
	}
	failOpenTrue := true
	abortOpenRoute, err := makeProfileRouteWithOverrides("/profile/abort-fail-open", disabledPerRouteAny, &failOpenTrue, extProcAbortFilterName)
	if err != nil {
		return nil, nil, err
	}
	chainedRoute, err := makeProfileRoute("/profile/chained", disabledPerRouteAny, extProcChainFirstFilterName, extProcChainSecondFilterName)
	if err != nil {
		return nil, nil, err
	}
	observabilityRoute, err := makeProfileRouteWithMode("/profile/observability", disabledPerRouteAny, streamedProcessingMode(), nil, extProcObservabilityFilterName)
	if err != nil {
		return nil, nil, err
	}

	defaultFilter, err := makeExtProcFilter(extProcFilterName, extProcClusterDefault, false, 5*time.Second, false, false, bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}
	shortTimeoutClosedFilter, err := makeExtProcFilter(extProcShortTimeoutFailClosedName, extProcClusterDefault, false, 150*time.Millisecond, false, true, bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}
	shortTimeoutOpenFilter, err := makeExtProcFilter(extProcShortTimeoutFailOpenName, extProcClusterDefault, true, 150*time.Millisecond, false, true, bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}
	abortFilter, err := makeExtProcFilter(extProcAbortFilterName, extProcClusterAbort, false, 5*time.Second, false, true, bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}
	chainFirstFilter, err := makeExtProcFilter(extProcChainFirstFilterName, extProcClusterDefault, false, 5*time.Second, false, true, bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}
	chainSecondFilter, err := makeExtProcFilter(extProcChainSecondFilterName, extProcClusterSecondary, false, 5*time.Second, false, true, bufferedProcessingMode())
	if err != nil {
		return nil, nil, err
	}
	observabilityFilter, err := makeExtProcFilter(extProcObservabilityFilterName, extProcClusterDefault, true, 5*time.Second, true, true, streamedProcessingMode())
	if err != nil {
		return nil, nil, err
	}

	routerAny, err := mustAny(&routerv3.Router{})
	if err != nil {
		return nil, nil, err
	}

	hcm := &hcmv3.HttpConnectionManager{
		StatPrefix:          "conformance_http",
		CodecType:           hcmv3.HttpConnectionManager_AUTO,
		HttpProtocolOptions: &corev3.Http1ProtocolOptions{EnableTrailers: true},
		Http2ProtocolOptions: &corev3.Http2ProtocolOptions{
			MaxConcurrentStreams: wrapperspb.UInt32(1024),
		},
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{
			RouteConfig: &routev3.RouteConfiguration{
				Name: "conformance_routes",
				VirtualHosts: []*routev3.VirtualHost{{
					Name:    "conformance_vhost",
					Domains: []string{"*"},
					Routes: []*routev3.Route{
						{
							Match: &routev3.RouteMatch{
								PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
								Headers: []*routev3.HeaderMatcher{{
									Name: ":authority",
									HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
										StringMatch: &matcherv3.StringMatcher{
											MatchPattern: &matcherv3.StringMatcher_Exact{Exact: "upstream-secondary"},
										},
									},
								}},
							},
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "upstream_secondary"},
								},
							},
						},
						{
							Match: &routev3.RouteMatch{
								PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/mode/disabled"},
							},
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "upstream_primary"},
								},
							},
							TypedPerFilterConfig: map[string]*anypb.Any{
								extProcFilterName: disabledPerRouteAny,
							},
						},
						bufferedPartialRoute,
						bufferedRoute,
						streamedRoute,
						trailersRoute,
						noneRoute,
						skipReqHeadersRoute,
						timeoutClosedRoute,
						timeoutOpenRoute,
						abortClosedRoute,
						abortOpenRoute,
						chainedRoute,
						observabilityRoute,
						{
							Match: &routev3.RouteMatch{
								PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
							},
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "upstream_primary"},
								},
							},
						},
					},
				}},
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{
			defaultFilter,
			shortTimeoutClosedFilter,
			shortTimeoutOpenFilter,
			abortFilter,
			chainFirstFilter,
			chainSecondFilter,
			observabilityFilter,
			{
				Name:       "envoy.filters.http.router",
				ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: routerAny},
			},
		},
	}

	hcmAny, err := mustAny(hcm)
	if err != nil {
		return nil, nil, err
	}

	bootstrap := &bootstrapv3.Bootstrap{
		StaticResources: &bootstrapv3.Bootstrap_StaticResources{
			Listeners: []*listenerv3.Listener{{
				Name: "conformance_listener",
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: "0.0.0.0",
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: uint32(p.ListenerPort),
							},
						},
					},
				},
				FilterChains: []*listenerv3.FilterChain{{
					Filters: []*listenerv3.Filter{{
						Name:       "envoy.filters.network.http_connection_manager",
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
					}},
				}},
			}},
			Clusters: []*clusterv3.Cluster{
				primaryCluster,
				secondaryCluster,
				extprocCluster,
				abortExtprocCluster,
				secondaryExtprocCluster,
			},
		},
		Admin: &bootstrapv3.Admin{
			Address: &corev3.Address{
				Address: &corev3.Address_SocketAddress{
					SocketAddress: &corev3.SocketAddress{
						Address: "0.0.0.0",
						PortSpecifier: &corev3.SocketAddress_PortValue{
							PortValue: uint32(p.AdminPort),
						},
					},
				},
			},
		},
	}

	if verr := bootstrap.ValidateAll(); verr != nil {
		return nil, nil, fmt.Errorf("validate envoy bootstrap: %w", verr)
	}

	rawJSON, err := protojson.Marshal(bootstrap)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal envoy bootstrap: %w", err)
	}
	return bootstrap, rawJSON, nil
}
