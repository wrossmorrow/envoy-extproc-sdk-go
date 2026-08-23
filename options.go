package extproc

import (
	"encoding/json"
	"os"

	yaml "gopkg.in/yaml.v3"
)

type ServerOptions struct {
	ExtProcPort                    int    `json:"extproc_port" yaml:"extproc_port"`
	MetricsHTTPPort                int    `json:"metrics_http_port" yaml:"metrics_http_port"`
	MaxConcurrentStreams           uint32 `json:"max_concurrent_streams" yaml:"max_concurrent_streams"`
	UnreadyPropagationDelaySeconds uint32 `json:"unready_propogation_delay_seconds" yaml:"unready_propogation_delay_seconds"`
	TerminationGracePeriodSeconds  int32  `json:"termination_grace_period_seconds" yaml:"termination_grace_period_seconds"`
}

func NewDefaultServerOptions() *ServerOptions {
	return &ServerOptions{
		ExtProcPort:                    50051,
		MetricsHTTPPort:                9090,
		MaxConcurrentStreams:           1000,
		UnreadyPropagationDelaySeconds: 5,
		TerminationGracePeriodSeconds:  10,
	}
}

func NewServerOptionsFromJson(filePath string) (*ServerOptions, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var opts ServerOptions
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&opts); err != nil {
		return nil, err
	}

	return &opts, nil
}

func NewServerOptionsFromYaml(filePath string) (*ServerOptions, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var opts ServerOptions
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&opts); err != nil {
		return nil, err
	}

	return &opts, nil
}

type ProcessingOptions struct {
	UpdateExtProcHeader     bool
	UpdateDurationHeader    bool
	AbortOnProcessorFailure bool
}

func NewDefaultProcessingOptions() *ProcessingOptions {
	return &ProcessingOptions{}
}

func NewProcessingOptionsFromJson(filePath string) (*ProcessingOptions, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var opts ProcessingOptions
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&opts); err != nil {
		return nil, err
	}

	return &opts, nil
}

func NewProcessingOptionsFromYaml(filePath string) (*ProcessingOptions, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var opts ProcessingOptions
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&opts); err != nil {
		return nil, err
	}

	return &opts, nil
}
