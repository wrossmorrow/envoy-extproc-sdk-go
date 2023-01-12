package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

var (
	port = flag.String("port", "8000", "port to listen on")
)

type Request struct {
	Datetime string
	Method   string
	Path     string
	Headers  map[string]string
	Body     string
}

func echo(w http.ResponseWriter, req *http.Request) {
	log.Print("[" + time.Now().UTC().String() + "] " + req.Method + " " + req.URL.String())
	r := Request{
		Datetime: time.Now().UTC().String(),
		Method:   req.Method,
		Path:     req.URL.String(),
		Headers:  make(map[string]string),
		Body:     "",
	}
	for name, headers := range req.Header {
		r.Headers[name] = ""
		for _, h := range headers {
			// w.Header().Set("X-Request-" + name, h)
			r.Headers[name] += h + ","
		}
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
	w.Write(rb)
}

func main() {
	flag.Parse()
	log.Print("Running echo server on " + *port)
	http.HandleFunc("/", echo)
	err := http.ListenAndServe(":"+*port, nil)
	log.Print("%v", err)
}
