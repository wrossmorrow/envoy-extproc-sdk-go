package main

import (
	_ "embed"

	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	urlPrefix  string       = "http://localhost:8080/test"
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
	EmptyBody  bool
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

func makeRequest(request *TesterRequest) (*TesterResponse, error) {

	reqBody, _ := json.Marshal(request.Body)
	req, err := http.NewRequest("POST", urlPrefix, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

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

	if err := json.NewDecoder(resp.Body).Decode(&respObj.Body); err != nil {
		// Empty body is ok; should we validate against Content-Length response header too?
		if !errors.Is(err, io.EOF) {
			return &respObj, err
		}
		respObj.EmptyBody = true
	}

	return &respObj, nil

}

func show(t *testing.T, s *TesterResponse) {
	t.Helper()
	t.Logf("%d, %v, %v", s.StatusCode, s.Headers, s.Body)
}

func check(t *testing.T, r *TesterRequest, s *TesterResponse) {
	t.Helper()

	if r.Body.ClearRequestBody {
		if s.Body.Body != "" {
			t.Errorf("Request body cleared but upstream saw a body")
		}
	}

	if r.Body.ClearResponseBody {
		if !s.EmptyBody {
			t.Errorf("Response body cleared but request returned a body")
		}
	} else {
		if s.EmptyBody {
			t.Errorf("Response body not cleared but request failed to return a body")
		}
	}

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
	runTest(t, r)
}

func TestRequests_Parameterized(t *testing.T) {

	var plan TestingPlan

	if err := yaml.Unmarshal(testPlanYaml, &plan); err != nil {
		t.Fatalf("Failed to unmarshal test cases YAML: %v", err)
	}

	for _, tc := range plan.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			runTest(t, &tc.Request)
		})
	}
}
