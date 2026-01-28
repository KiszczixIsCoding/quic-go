package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"math/big"

	"github.com/quic-go/quic-go"
	_ "github.com/quic-go/quic-go/http3"
)

const addr = "localhost:4242"

const message = "foobar"

// We start a server echoing data on the first stream the client opens,
// then connect with a client, send the message, and wait for its receipt.
func main() {
	fmt.Println("QUIC echo server listening on", addr)
	log.Fatal(echoServer())
	//
	//if err := clientMain(); err != nil {
	//	panic(err)
	//}
}

// Start a server that echos all data on the first stream opened by the client
func echoServer() error {
	listener, err := quic.ListenAddr(addr, generateTLSConfig(), nil)
	if err != nil {
		return err
	}
	defer listener.Close()

	fmt.Println("QUIC echo server listening on", addr)

	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}

		// Handle each connection in a new goroutine
		go handleConnection(conn)
	}
}

func handleConnection(conn *quic.Conn) {
	defer conn.CloseWithError(0, "done")

	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			log.Printf("stream accept error: %v", err)
			return
		}

		go func(s *quic.Stream) {
			defer s.Close()
			io.Copy(loggingWriter{s}, s)
		}(stream)
	}
}

func clientMain() error {
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"quic-echo-example"},
	}
	conn, err := quic.DialAddr(context.Background(), addr, tlsConf, nil)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		return err
	}
	defer stream.Close()

	fmt.Printf("Client: Sending '%s'\n", message)
	if _, err := stream.Write([]byte(message)); err != nil {
		return err
	}

	buf := make([]byte, len(message))
	if _, err := io.ReadFull(stream, buf); err != nil {
		return err
	}
	fmt.Printf("Client: Got '%s'\n", buf)

	return nil
}

// A wrapper for io.Writer that also logs the message.
type loggingWriter struct{ io.Writer }

func (w loggingWriter) Write(b []byte) (int, error) {
	fmt.Printf("Server: Got '%s'\n", string(b))
	return w.Writer.Write(b)
}

// Setup a bare-bones TLS config for the server
func generateTLSConfig() *tls.Config {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public(), priv)
	if err != nil {
		panic(err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  priv,
		}},
		NextProtos: []string{"quic-echo-example"},
	}
}
