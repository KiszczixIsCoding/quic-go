package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	_ "fmt"
	"io"
	"log"
	"math/big"
	_ "net"
	"time"

	quic "github.com/quic-go/quic-go"
)

// Simple QUIC echo server using quic-go
func main() {
	addr := "localhost:4242"

	listener, err := quic.ListenAddr(addr, generateTLSConfig(), nil)
	if err != nil {
		log.Fatalf("failed to start listener: %v", err)
	}

	log.Printf("QUIC echo server listening on %s", addr)

	for {
		sess, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("accept session error: %v", err)
			continue
		}
		go handleSession(sess)
	}
}

func handleSession(sess *quic.Conn) {
	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			log.Printf("accept stream error: %v", err)
			return
		}
		go handleStream(*stream)
	}
}

func handleStream(stream quic.Stream) {
	defer stream.Close()
	io.Copy(&stream, &stream) // echo back
}

// generateTLSConfig creates a minimal TLS config required by quic-go
func generateTLSConfig() *tls.Config {
	key, cert, err := generateSelfSignedCert()
	if err != nil {
		log.Fatalf("failed to generate cert: %v", err)
	}
	tlsCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		log.Fatalf("failed to parse cert: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, NextProtos: []string{"echo"}}
}

// generateSelfSignedCert creates a quick self-signed cert for localhost
// (for real deployments, use a valid certificate).
func generateSelfSignedCert() (keyPEM, certPEM []byte, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}

	certBuf := new(bytes.Buffer)
	pem.Encode(certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBuf := new(bytes.Buffer)
	pem.Encode(keyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return 
}
