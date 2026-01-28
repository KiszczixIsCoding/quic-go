package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/quic-go/quic-go/http3"
	_ "github.com/quic-go/quic-go/http3"
	_ "github.com/quic-go/webtransport-go"
	"math/big"
	"net"
	"net/http"
	"time"
)

const addr = "localhost:4242"

const message = "foobar"

// We start a server echoing data on the first stream the client opens,
// then connect with a client, send the message, and wait for its receipt.
func main() {
	fmt.Println("QUIC echo server listening on", addr)

	mux := http.NewServeMux()
	// ... add HTTP handlers to mux ...
	// If mux is nil, the http.DefaultServeMux is used.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from HTTP/3 QUIC!\nYou connected using: %s\n", r.Proto)
	})

	tlsConfig := generateTLSConfig()

	server := http3.Server{
		Addr:      ":4433", // use :4433 to avoid root privileges
		Handler:   mux,
		TLSConfig: tlsConfig,
	}

	fmt.Println("🚀 Serving HTTP/3 on https://localhost:4433")
	fmt.Println("Starting server...")
	err := server.ListenAndServe()
	fmt.Println("ListenAndServe returned:", err)

	//s := webtransport.Server{
	//	H3: http3.Server{
	//		Addr:      ":443",
	//		TLSConfig: &tls.Config{}, // use your tls.Config here
	//	},
	//}
	//
	//// Create a new HTTP endpoint /webtransport.
	//http.HandleFunc("/webtransport", func(w http.ResponseWriter, r *http.Request) {
	//	_, err := s.Upgrade(w, r)
	//	if err != nil {
	//		log.Printf("upgrading failed: %s", err)
	//		w.WriteHeader(500)
	//		return
	//	}
	//	// Handle the session. Here goes the application logic.
	//})
	//
	//err := s.ListenAndServe()
	//if err != nil {
	//	return
	//}
}

func generateTLSConfig() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,

		DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
		},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"h3"},
	}
}
