package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	hostname   string       = "localhost:8080"
	urlPrefix  string       = fmt.Sprintf("http://%s/test", hostname)
	upstream   string       = fmt.Sprintf("http://%s/no-extproc", hostname)
	httpClient *http.Client = &http.Client{}
)

//go:embed cases.yaml
var testPlanYaml []byte

type TestingPlan struct {
	Cases []TestCase `json:"cases" yaml:"cases"`
}

type TestCase struct {
	Name    string        `json:"name" yaml:"name"`
	Request TesterRequest `json:"request" yaml:"request"`
}

type TesterRequest struct {
	Headers map[string]string `json:"headers" yaml:"headers"`
	Body    TesterRequestBody `json:"body" yaml:"body"`
}

type TesterResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       TesterResponseBody
	RawBody    []byte
	EmptyBody  bool
}

func (s *TesterResponse) GetHeaderByName(key string) ([]string, bool) {
	target := strings.ToLower(key)
	for k, v := range s.Headers {
		if strings.ToLower(k) == target {
			return v, true
		}
	}
	return nil, false
}

type TesterResponseBody struct {
	Datetime string
	Server   string
	Hostname string
	Method   string
	Path     string
	Query    map[string]string
	Headers  map[string][]string
	Body     string
	Duration int
	Status   int
}

func (r *TesterResponseBody) GetHeaderByName(key string) ([]string, bool) {
	target := strings.ToLower(key)
	for k, v := range r.Headers {
		if strings.ToLower(k) == target {
			return v, true
		}
	}
	return nil, false
}

func makeRequest(request *TesterRequest) (*TesterResponse, error) {

	reqBody, _ := json.Marshal(request.Body)
	req, err := http.NewRequest("POST", urlPrefix, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Add("content-type", "application/json")
	req.Header.Add("accept", "application/json")
	for n := range request.Headers {
		req.Header.Add(n, request.Headers[n])
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var respObj TesterResponse
	respObj.StatusCode = resp.StatusCode
	respObj.Headers = map[string][]string(resp.Header)

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return &respObj, err
	}

	respObj.RawBody = respBytes

	if len(bytes.TrimSpace(respBytes)) == 0 {
		respObj.EmptyBody = true
		return &respObj, nil
	}

	if err := json.Unmarshal(respBytes, &respObj.Body); err != nil {
		return &respObj, err
	}

	return &respObj, nil

}

func unsafePretty(obj any) []byte {
	s, _ := json.MarshalIndent(obj, "", "  ")
	return s
}

func show(t *testing.T, s *TesterResponse) {
	t.Helper()
	t.Logf("%d, %s, %s", s.StatusCode, unsafePretty(s.Headers), unsafePretty(s.Body))
}

func check(t *testing.T, r *TesterRequest, s *TesterResponse) {
	t.Helper()

	// cancelled requests checks (terminal)

	if r.Body.CancelRequest {

		if s.StatusCode != int(r.Body.CancelRequestStatus) {
			t.Errorf("Cancelled request did not return the declared status")
		}

		// TODO: cancel request headers (any specified headers appear)

		if string(s.RawBody) != r.Body.CancelRequestBody {
			t.Errorf("Cancelled request did not return the stated body: \"%s\" vs \"%s\"", s.Body.Body, r.Body.CancelRequestBody)
		}

		return
	}

	// request headers modification checks

	if len(r.Body.AddRequestHeaders) > 0 {
		for n := range r.Body.AddRequestHeaders {
			v := r.Body.AddRequestHeaders[n]
			w, ok := s.Body.GetHeaderByName(n)
			if ok {
				if !slices.Contains(w, v) {
					t.Errorf("Request header added but value not returned in response")
				}
			} else {
				t.Errorf("Request header added but not returned in response")
			}
		}
	}

	if len(r.Body.AppendRequestHeaders) > 0 {
		for n := range r.Body.AppendRequestHeaders {
			v := r.Body.AppendRequestHeaders[n]
			w, ok := s.Body.GetHeaderByName(n)
			if ok {
				if !slices.Contains(w, v) {
					t.Errorf("Request header appended but value not returned in response")
				}
			} else {
				t.Errorf("Request header appended but not returned in response")
			}
		}
	}

	if len(r.Body.OverwriteRequestHeaders) > 0 {
		for n := range r.Body.OverwriteRequestHeaders {
			v := r.Body.OverwriteRequestHeaders[n]
			w, ok := s.Body.GetHeaderByName(n)
			t.Logf("headers: %s, %s, %v", n, v, w)
			if ok {
				if len(w) > 1 {
					t.Errorf("Request header overwritten but multiple values returned in response")
				} else {
					if w[0] != v {
						t.Errorf("Request header overwritten but value not returned in response")
					}
				}
			} else {
				t.Errorf("Request header overwritten but not returned in response")
			}
		}
	}

	if len(r.Body.RemoveRequestHeaders) > 0 {
		for _, n := range r.Body.RemoveRequestHeaders {
			_, ok := s.Body.GetHeaderByName(n)
			if ok {
				t.Errorf("Request header removed but value was returned in response")
			}
		}
	}

	// request body modification checks

	if r.Body.ClearRequestBody {
		if s.Body.Body != "" {
			t.Errorf("Request body cleared but upstream saw a body")
		}
	}

	// TODO: replace request body

	// response headers modification checks

	if len(r.Body.AddResponseHeaders) > 0 {
		for n := range r.Body.AddResponseHeaders {
			v := r.Body.AddResponseHeaders[n]
			w, ok := s.GetHeaderByName(n)
			t.Logf("Comparing headers: %s, %v, %v", v, w, slices.Contains(w, v))
			if ok {
				if !slices.Contains(w, v) {
					t.Errorf("Response header added but value not returned in response")
				}
			} else {
				t.Errorf("Response header added but not returned in response")
			}
		}
	}

	if len(r.Body.AppendResponseHeaders) > 0 {
		for n := range r.Body.AppendResponseHeaders {
			v := r.Body.AppendResponseHeaders[n]
			w, ok := s.GetHeaderByName(n)
			if ok {
				if !slices.Contains(w, v) {
					t.Errorf("Response header added but value not returned in response")
				}
			} else {
				t.Errorf("Response header added but not returned in response")
			}
		}
	}

	if len(r.Body.OverwriteResponseHeaders) > 0 {
		for n := range r.Body.OverwriteResponseHeaders {
			v := r.Body.OverwriteResponseHeaders[n]
			w, ok := s.GetHeaderByName(n)
			if ok {
				if len(w) > 1 {
					t.Errorf("Response header overwritten but multiple values returned in response")
				} else {
					if w[0] != v {
						t.Errorf("Response header overwritten but value not returned in response")
					}
				}
			} else {
				t.Errorf("Response header overwritten but not returned in response")
			}
		}
	}

	if len(r.Body.RemoveResponseHeaders) > 0 {
		for _, n := range r.Body.RemoveRequestHeaders {
			_, ok := s.GetHeaderByName(n)
			if ok {
				t.Errorf("Response header removed but value was returned in response")
			}
		}
	}

	// response body modification checks

	if r.Body.ClearResponseBody {
		if !s.EmptyBody {
			t.Errorf("Response body cleared but request returned a body")
		}
	} else {
		if s.EmptyBody {
			t.Errorf("Response body not cleared but request failed to return a body")
		}
	}

	// TODO: replace response body

}

func runTest(t *testing.T, r *TesterRequest) {
	s, err := makeRequest(r)
	if err != nil {
		t.Fatalf("Error in making request: %v", err)
	}
	show(t, s)
	check(t, r, s)
}

func TestBasicRequest(t *testing.T) {
	r := &TesterRequest{
		Headers: make(map[string]string),
		Body:    TesterRequestBody{},
	}
	t.Logf("test request: %s", unsafePretty(r))
	runTest(t, r)
}

func TestRequests_Parameterized(t *testing.T) {

	var plan TestingPlan

	if err := yaml.Unmarshal(testPlanYaml, &plan); err != nil {
		t.Fatalf("Failed to unmarshal test cases YAML: %v", err)
	}

	for _, tc := range plan.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Logf("Test Request: %s", unsafePretty(tc.Request))
			runTest(t, &tc.Request)
		})
	}

}

func TestMain(m *testing.M) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := httpClient.Get(upstream)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				break
			}
		}
		if time.Now().After(deadline) {
			log.Fatal("gateway never became ready")
		}
		time.Sleep(500 * time.Millisecond)
	}
	os.Exit(m.Run())
}
