package main

import (
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Requests []ConfigItem `yaml:"requests"`
}

type ConfigItem struct {
	Request  HttpRequest  `yaml:"request"`
	Response HttpResponse `yaml:"response"`
}

func config() Config {
	yfile, err := os.ReadFile("requests.yaml")
	if err != nil {
		log.Fatal(err)
	}

	var C Config
	err = yaml.Unmarshal(yfile, &C)
	if err != nil {
		log.Fatal(err)
	}

	// log.Printf("data: %v\n", C)
	// for _, d := range C.Requests {
	// 	log.Printf("request: %v\n", d.Request)
	// 	log.Printf("response: %v\n", d.Response)
	// }

	return C
}
