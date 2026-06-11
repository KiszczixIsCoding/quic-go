package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/internal/wire"
	"github.com/quic-go/quic-go/qlog"
	"log"
)

func EncodeFrame(f wire.TulCustomFrame) []byte {
	// header (8 bytes) + length (8 bytes) + data
	combined := make([]byte, 8+8+len(f.Data))
	binary.BigEndian.PutUint64(combined[0:8], 0)                    // header
	binary.BigEndian.PutUint64(combined[8:16], uint64(len(f.Data))) // length
	copy(combined[16:], f.Data)
	return combined
}

func main() {
	ctx := context.Background()
	//
	//tlsConf := &tls.Config{
	//	InsecureSkipVerify: true,
	//	NextProtos:         []string{"quic-echo-example"},
	//}

	tls, _ := loadTLSConfig("../cert.pem", "../key.pem")
	quicConf := &quic.Config{
		Tracer: qlog.DefaultConnectionTracer,
	}

	listener, err := quic.ListenAddr("localhost:4443", tls, quicConf)

	if err != nil {
		log.Fatal(err)
	}

	log.Println("QUIC server listening on :443")

	for {
		conn, err := listener.Accept(ctx)

		if err != nil {
			log.Println("accept error:", err)
			continue
		}

		go func(c *quic.Conn) {
			// czekaj aż handshake się zakończy
			<-c.HandshakeComplete()

			stream, err := conn.OpenStreamSync(ctx)
			if err != nil {
				log.Println("stream error:", err)
				return
			}

			frame := wire.TulCustomFrame{
				Data: []byte{0x08},
			}

			encoded := frame.parseTulCustomFrame([]byte{0x08})

			_, err = stream.Write(encoded)
			if err != nil {
				log.Fatal(err)
			}
		}(conn)
	}
}

func loadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"quic-echo-example"},
	}, nil
}
