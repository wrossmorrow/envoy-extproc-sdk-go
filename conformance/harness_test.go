package conformance

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	epb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	extproc "github.com/wrossmorrow/envoy-extproc-sdk-go"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestEnvoyVersionManifest(t *testing.T) {
	m, err := LoadEnvoyVersions()
	if err != nil {
		t.Fatalf("LoadEnvoyVersions() error: %v", err)
	}
	if !slices.Contains(m.Supported, m.Default) {
		t.Errorf("default Envoy version %q not in supported list %v", m.Default, m.Supported)
	}
	for _, want := range []string{"v1.37-latest", "v1.38-latest", "v1.39-latest", "v1.40-latest"} {
		if !slices.Contains(m.Supported, want) {
			t.Errorf("supported versions %v missing %q", m.Supported, want)
		}
	}
	if !slices.Contains(m.Canary, "dev") {
		t.Errorf("canary versions %v missing %q", m.Canary, "dev")
	}

	t.Setenv("ENVOY_VERSION", "v1.39-latest")
	got, rerr := ResolveEnvoyVersion()
	if rerr != nil || got != "v1.39-latest" {
		t.Errorf("ResolveEnvoyVersion() with override = (%q, %v), want (%q, nil)", got, rerr, "v1.39-latest")
	}
}

func TestBuildBootstrapValidates(t *testing.T) {
	_, rawJSON, err := BuildBootstrap(BootstrapParams{
		UpstreamHost:          "host.docker.internal",
		PrimaryUpstreamPort:   18001,
		SecondaryUpstreamPort: 18002,
		ExtProcPort:           50051,
		ListenerPort:          18080,
		AdminPort:             19901,
	})
	if err != nil {
		t.Fatalf("BuildBootstrap failed: %v", err)
	}

	var parsed map[string]any
	if uerr := json.Unmarshal(rawJSON, &parsed); uerr != nil {
		t.Fatalf("unmarshal bootstrap JSON: %v", uerr)
	}

	rawStr := string(rawJSON)
	for _, needle := range []string{
		"upstream_primary",
		"upstream_secondary",
		"extproc_service",
		"/mode/disabled",
		"/mode/buffered",
		"/mode/streamed",
		"/mode/trailers",
		"/mode/none",
		"/mode/skip-req-headers",
	} {
		if !strings.Contains(rawStr, needle) {
			t.Errorf("bootstrap JSON missing %q", needle)
		}
	}
}

func TestUpstreamServerHTTP1AndH2CAndTrailers(t *testing.T) {
	srv, err := StartUpstreamServer("primary")
	if err != nil {
		t.Fatalf("StartUpstreamServer failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	t.Run("http1_chunked_and_trailers", func(t *testing.T) {
		client := &http.Client{Timeout: 5 * time.Second}
		req, rerr := http.NewRequest(http.MethodPost, srv.URL()+"/hello?k=v", strings.NewReader("hello-upstream"))
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set(CaseHeader, "case-1")
		req.Header.Set("x-upstream-chunks", "3")
		req.Header.Set("x-upstream-trailer-x-srv-tr", "ok")

		resp, derr := client.Do(req)
		if derr != nil {
			t.Fatalf("HTTP/1.1 request failed: %v", derr)
		}
		defer resp.Body.Close()

		body, berr := io.ReadAll(resp.Body)
		if berr != nil {
			t.Fatalf("read response body: %v", berr)
		}
		if resp.Header.Get(CaseHeader) != "case-1" {
			t.Errorf("response %s = %q, want %q", CaseHeader, resp.Header.Get(CaseHeader), "case-1")
		}
		if got := resp.Trailer.Get("X-Srv-Tr"); got != "ok" {
			t.Errorf("response trailer x-srv-tr = %q, want %q", got, "ok")
		}

		var echo UpstreamEcho
		if uerr := json.Unmarshal(body, &echo); uerr != nil {
			t.Fatalf("unmarshal UpstreamEcho: %v (raw=%q)", uerr, string(body))
		}
		if echo.Server != "primary" || echo.Body != "hello-upstream" || echo.RawQuery != "k=v" {
			t.Errorf("unexpected echo payload: %+v", echo)
		}
	})

	t.Run("h2c_request_and_response_trailers", func(t *testing.T) {
		h2cClient := &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, network, addr)
				},
			},
		}

		req, rerr := http.NewRequest(http.MethodPost, srv.URL()+"/h2c", struct{ io.Reader }{strings.NewReader("h2-body")})
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.ContentLength = -1
		req.Trailer = http.Header{"X-Req-Trailer": []string{"from-client"}}
		req.Header.Set("x-upstream-trailer-x-resp-trailer", "from-server")

		resp, derr := h2cClient.Do(req)
		if derr != nil {
			t.Fatalf("h2c request failed: %v", derr)
		}
		defer resp.Body.Close()

		body, berr := io.ReadAll(resp.Body)
		if berr != nil {
			t.Fatalf("read h2c body: %v", berr)
		}
		var echo UpstreamEcho
		if uerr := json.Unmarshal(body, &echo); uerr != nil {
			t.Fatalf("unmarshal h2c echo: %v", uerr)
		}
		if echo.Proto != "HTTP/2.0" {
			t.Errorf("echo.Proto = %q, want %q", echo.Proto, "HTTP/2.0")
		}
		if got := echo.Trailers["x-req-trailer"]; !slices.Equal(got, []string{"from-client"}) {
			t.Errorf("echo.Trailers[x-req-trailer] = %v, want [from-client]", got)
		}
		if got := resp.Trailer.Get("X-Resp-Trailer"); got != "from-server" {
			t.Errorf("resp.Trailer[X-Resp-Trailer] = %q, want %q", got, "from-server")
		}
	})
}

func TestConformanceProcessorLoopbackGRPC(t *testing.T) {
	srv, err := StartConformanceExtProcServer(nil, nil)
	if err != nil {
		t.Fatalf("StartConformanceExtProcServer failed: %v", err)
	}
	t.Cleanup(srv.Close)

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient failed: %v", err)
	}
	defer conn.Close()

	client := epb.NewExternalProcessorClient(conn)

	t.Run("full_exchange_validates_responses", func(t *testing.T) {
		srv.Processor.Register("loopback-full", CaseHandler{
			RequestHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				return ctx.AddHeader("x-req-added", extproc.HeaderValue{RawValue: []byte("1")})
			},
			RequestBody: func(ctx *extproc.RequestContext, _ []byte) error {
				return ctx.ReplaceBody([]byte("mutated-req"))
			},
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				return ctx.AddHeader("x-resp-added", extproc.HeaderValue{RawValue: []byte("2")})
			},
			ResponseBody: func(ctx *extproc.RequestContext, _ []byte) error {
				return ctx.ReplaceBody([]byte("mutated-resp"))
			},
		})
		t.Cleanup(func() { srv.Processor.Unregister("loopback-full") })

		stream, serr := client.Process(context.Background())
		if serr != nil {
			t.Fatalf("open stream: %v", serr)
		}

		phases := []*epb.ProcessingRequest{
			{
				Request: &epb.ProcessingRequest_RequestHeaders{
					RequestHeaders: &epb.HttpHeaders{
						Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
							{Key: ":method", RawValue: []byte("POST")},
							{Key: CaseHeader, RawValue: []byte("loopback-full")},
						}},
					},
				},
			},
			{
				Request: &epb.ProcessingRequest_RequestBody{
					RequestBody: &epb.HttpBody{Body: []byte("orig"), EndOfStream: true},
				},
			},
			{
				Request: &epb.ProcessingRequest_ResponseHeaders{
					ResponseHeaders: &epb.HttpHeaders{
						Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
							{Key: ":status", RawValue: []byte("200")},
						}},
					},
				},
			},
			{
				Request: &epb.ProcessingRequest_ResponseBody{
					ResponseBody: &epb.HttpBody{Body: []byte("orig-resp"), EndOfStream: true},
				},
			},
		}

		for i, req := range phases {
			if sendErr := stream.Send(req); sendErr != nil {
				t.Fatalf("send phase %d: %v", i, sendErr)
			}
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				t.Fatalf("recv phase %d: %v", i, recvErr)
			}
			if verr := resp.ValidateAll(); verr != nil {
				t.Errorf("phase %d response ValidateAll() = %v, want nil", i, verr)
			}
		}

		if cerr := stream.CloseSend(); cerr != nil {
			t.Fatalf("CloseSend: %v", cerr)
		}
		if _, eofErr := stream.Recv(); eofErr != io.EOF {
			t.Fatalf("expected io.EOF after CloseSend, got %v", eofErr)
		}
	})

	t.Run("skip_request_headers_dispatches_via_response_headers", func(t *testing.T) {
		srv.Processor.Register("loopback-skip", CaseHandler{
			ResponseHeaders: func(ctx *extproc.RequestContext, _ extproc.AllHeaders) error {
				return ctx.AddHeader("x-skip-seen", extproc.HeaderValue{RawValue: []byte("yes")})
			},
		})
		t.Cleanup(func() { srv.Processor.Unregister("loopback-skip") })

		stream, serr := client.Process(context.Background())
		if serr != nil {
			t.Fatalf("open stream: %v", serr)
		}
		if sendErr := stream.Send(&epb.ProcessingRequest{
			Request: &epb.ProcessingRequest_ResponseHeaders{
				ResponseHeaders: &epb.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
						{Key: ":status", RawValue: []byte("200")},
						{Key: CaseHeader, RawValue: []byte("loopback-skip")},
					}},
					EndOfStream: true,
				},
			},
		}); sendErr != nil {
			t.Fatalf("send response headers: %v", sendErr)
		}

		resp, recvErr := stream.Recv()
		if recvErr != nil {
			t.Fatalf("recv response headers: %v", recvErr)
		}
		if verr := resp.ValidateAll(); verr != nil {
			t.Errorf("ValidateAll() = %v, want nil", verr)
		}
		headers := resp.GetResponseHeaders().GetResponse().GetHeaderMutation().GetSetHeaders()
		if len(headers) != 1 || headers[0].GetHeader().GetKey() != "x-skip-seen" {
			t.Errorf("unexpected response header mutation: %+v", headers)
		}

		if cerr := stream.CloseSend(); cerr != nil {
			t.Fatalf("CloseSend: %v", cerr)
		}
		if _, eofErr := stream.Recv(); eofErr != io.EOF {
			t.Fatalf("expected io.EOF, got %v", eofErr)
		}
	})
}
