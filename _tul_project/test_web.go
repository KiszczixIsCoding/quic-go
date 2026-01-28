package main

import (
	"fmt"
	"github.com/quic-go/quic-go/http3"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from HTTP/3! You requested %s via %s\n", r.URL.Path, r.Proto)
	})
	addr := ":4433"
	log.Printf("Starting HTTP/3 server at https://localhost%v", addr)
	if err := http3.ListenAndServeTLS(addr, "cert.pem", "key.pem", mux); err != nil {
		log.Fatal(err)
	}
}
