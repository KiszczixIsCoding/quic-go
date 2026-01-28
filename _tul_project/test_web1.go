package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/qlog"
	"log"
	"net/http"
	"time"
)

type quicConnKeyType struct{}

var quicConnKey = quicConnKeyType{}

func triggerCustomFrameSend(qc *quic.Conn, value int64) {
	// cast do internal connection (tylko w forku quic-go)
	//ic := qc.(interface {
	//	queueMyFrame(int64)
	//})
	//
	//ic.queueMyFrame(value)
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello via HTTP/3! You requested %s via %s\n", r.URL.Path, r.Proto)
		qcVal := r.Context().Value(quicConnKey)
		if qcVal == nil {
			http.Error(w, "no quic conn (not HTTP/3?)", 500)
			return
		}

		qc, ok := qcVal.(*quic.Conn)
		if !ok {
			http.Error(w, "context value is not *quic.Conn", 500)
			return
		}
		qc.SendMyFrame(uint64(time.Now().UnixNano()))

		fmt.Fprintf(w, "Hello via HTTP/3! You requested %s via %s\n", r.URL.Path, r.Proto)
	})

	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{http3.NextProtoH3},
	}

	qconf := &quic.Config{
		Tracer: qlog.DefaultConnectionTracer,
	}

	server := &http3.Server{
		Addr:       ":4433",
		Handler:    mux,
		TLSConfig:  tlsConf,
		QUICConfig: qconf,
		ConnContext: func(ctx context.Context, qc *quic.Conn) context.Context {
			return context.WithValue(ctx, quicConnKey, qc)
		},
	}

	log.Printf("Starting HTTP/3 server at https://localhost:4433")
	log.Fatal(server.ListenAndServeTLS("cert.pem", "key.pem"))
}
