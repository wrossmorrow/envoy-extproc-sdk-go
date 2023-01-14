package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"time"
	"strings"
)

var (
	port = flag.String("port", "8000", "port to listen on")
)

type Request struct {
	Datetime string
	Method   string
	Path     string
	Query    map[string][]string
	Headers  map[string][]string
	Body     string
}

func echo(w http.ResponseWriter, req *http.Request) {
	log.Print("[" + time.Now().UTC().String() + "] " + req.Method + " " + req.URL.String())

	r := Request{
		Datetime: time.Now().UTC().String(),
		Method:   req.Method,
		Path:     strings.Split(req.URL.String(), "?")[0],
		Query:    req.URL.Query(),
		Headers:  req.Header,
		Body:     "",
	}
	if req.Body != nil {
		bodyBytes, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Printf("Body reading error: %v", err)
			return
		}
		r.Body = string(bodyBytes)
		defer req.Body.Close()
	}
	rb, _ := json.Marshal(r)

	_, ok := r.Query["delay"]
	if ok && len(r.Query["delay"]) > 0 {
		delay, _ := time.ParseDuration(r.Query["delay"][0] + "s")
		log.Printf("Delay: %v\n", delay)
		time.Sleep(delay)
	}

	w.Write(rb)
}

func main() {
	flag.Parse()
	log.Print("Running echo server on " + *port)
	http.HandleFunc("/", echo)
	err := http.ListenAndServe(":"+*port, nil)
	log.Print("%v", err)
}
