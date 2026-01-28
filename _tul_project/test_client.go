package main

import (
	"crypto/tls"
	"fmt"
	"github.com/quic-go/quic-go/http3"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	// URL of the HTTP/3 server
	url := "https://localhost:4433/"
	// Create an HTTP/3 transport (QUIC)
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // For local dev/self-signed certs
			MinVersion:         tls.VersionTLS13,
		},
	}
	defer transport.Close()
	// Create HTTP client using the HTTP/3 transport
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}
	// Send a GET request
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	elapsed := time.Since(start)
	// Output results
	fmt.Printf("Status   : %s\n", resp.Status)
	fmt.Printf("Protocol : %s\n", resp.Proto) // Should be "HTTP/3"
	fmt.Printf("Elapsed  : %v\n", elapsed)
	fmt.Println("--------- Body ---------")
	fmt.Print(string(body))
}
