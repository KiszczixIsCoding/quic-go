package main

import (
	"fmt"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello from QUIC (HTTP/3) on localhost!")
	})

	// Self-signed certs for localhost testing
	certFile := "server.crt"
	keyFile := "server.key"

	fmt.Println("Serving on https://127.0.0.1:8443 using QUIC (HTTP/3)")
	err := http3.ListenAndServeQUIC("127.0.0.1:8443", certFile, keyFile, mux)
	if err != nil {
		panic(err)
	}
}
