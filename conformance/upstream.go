package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// UpstreamEcho captures the HTTP request state observed by an in-process
// upstream server so conformance tests can verify request mutations.
type UpstreamEcho struct {
	Server    string              `json:"server"`
	Proto     string              `json:"proto"`
	Host      string              `json:"host"`
	Method    string              `json:"method"`
	Path      string              `json:"path"`
	RawQuery  string              `json:"raw_query"`
	Headers   map[string][]string `json:"headers"`
	Trailers  map[string][]string `json:"trailers,omitempty"`
	Body      string              `json:"body"`
	BodyBytes int                 `json:"body_bytes"`
}

// UpstreamServer is an in-process HTTP/1.1 + h2c echo/control server used as
// Envoy's backend during conformance tests.
type UpstreamServer struct {
	Name     string
	listener net.Listener
	server   *http.Server
	port     int
}

// StartUpstreamServer starts a new UpstreamServer bound to 0.0.0.0:0 so both
// host loopback and Dockerized Envoy instances can connect.
func StartUpstreamServer(name string) (*UpstreamServer, error) {
	lis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("listen upstream %q: %w", name, err)
	}

	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		_ = lis.Close()
		return nil, fmt.Errorf("unexpected listener address %T", lis.Addr())
	}

	u := &UpstreamServer{
		Name:     name,
		listener: lis,
		port:     tcpAddr.Port,
	}

	h2s := &http2.Server{}
	u.server = &http.Server{
		Handler:           h2c.NewHandler(http.HandlerFunc(u.handle), h2s),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		_ = u.server.Serve(lis)
	}()

	return u, nil
}

// Port returns the ephemeral TCP port the upstream server is listening on.
func (u *UpstreamServer) Port() int {
	return u.port
}

// URL returns the loopback base URL for direct testing of the upstream server.
func (u *UpstreamServer) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", u.port)
}

// Close shuts down the upstream server.
func (u *UpstreamServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return u.server.Shutdown(ctx)
}

func (u *UpstreamServer) handle(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
		return
	}

	if delayStr := r.Header.Get("x-upstream-delay-ms"); delayStr != "" {
		if ms, derr := strconv.Atoi(delayStr); derr == nil && ms > 0 {
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	}

	statusCode := http.StatusOK
	if statusStr := r.Header.Get("x-upstream-status"); statusStr != "" {
		if code, cerr := strconv.Atoi(statusStr); cerr == nil && code >= 100 && code <= 599 {
			statusCode = code
		}
	}

	if caseID := r.Header.Get(CaseHeader); caseID != "" {
		w.Header().Set(CaseHeader, caseID)
	}

	headers := make(map[string][]string, len(r.Header))
	for k, vs := range r.Header {
		headers[strings.ToLower(k)] = slices.Clone(vs)
	}

	var trailers map[string][]string
	if len(r.Trailer) > 0 {
		trailers = make(map[string][]string, len(r.Trailer))
		for k, vs := range r.Trailer {
			trailers[strings.ToLower(k)] = slices.Clone(vs)
		}
	}

	var respPayload []byte
	var contentType string
	if rawVals, hasRaw := r.Header["X-Upstream-Raw-Body"]; hasRaw {
		if len(rawVals) > 0 {
			respPayload = []byte(rawVals[0])
		}
		contentType = r.Header.Get("x-upstream-content-type")
		if contentType == "" {
			contentType = "text/plain; charset=utf-8"
		}
	} else {
		echo := UpstreamEcho{
			Server:    u.Name,
			Proto:     r.Proto,
			Host:      r.Host,
			Method:    r.Method,
			Path:      r.URL.Path,
			RawQuery:  r.URL.RawQuery,
			Headers:   headers,
			Trailers:  trailers,
			Body:      string(bodyBytes),
			BodyBytes: len(bodyBytes),
		}
		encoded, merr := json.Marshal(echo)
		if merr != nil {
			http.Error(w, fmt.Sprintf("marshal echo: %v", merr), http.StatusInternalServerError)
			return
		}
		respPayload = encoded
		contentType = "application/json"
	}

	respTrailers := map[string]string{}
	for k, vs := range r.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-upstream-trailer-") && len(vs) > 0 {
			tName := strings.TrimPrefix(lower, "x-upstream-trailer-")
			if tName != "" {
				respTrailers[tName] = vs[0]
				w.Header().Add("Trailer", tName)
			}
		}
	}

	chunks := 1
	if chunksStr := r.Header.Get("x-upstream-chunks"); chunksStr != "" {
		if n, nerr := strconv.Atoi(chunksStr); nerr == nil && n > 1 {
			chunks = n
		}
	}

	w.Header().Set("Content-Type", contentType)
	if len(respTrailers) == 0 && chunks <= 1 && statusCode != http.StatusNoContent && statusCode != http.StatusNotModified {
		w.Header().Set("Content-Length", strconv.Itoa(len(respPayload)))
	}
	w.WriteHeader(statusCode)

	if chunks > 1 && len(respPayload) > 0 {
		flusher, _ := w.(http.Flusher)
		chunkSize := (len(respPayload) + chunks - 1) / chunks
		for offset := 0; offset < len(respPayload); offset += chunkSize {
			end := offset + chunkSize
			if end > len(respPayload) {
				end = len(respPayload)
			}
			_, _ = w.Write(respPayload[offset:end])
			if flusher != nil {
				flusher.Flush()
			}
		}
	} else if len(respPayload) > 0 {
		_, _ = w.Write(respPayload)
	}

	for tName, tVal := range respTrailers {
		w.Header().Set(http.TrailerPrefix+tName, tVal)
	}
}
