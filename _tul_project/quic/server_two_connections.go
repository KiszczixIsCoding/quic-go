package main

import (
	"context"
	"crypto/tls"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"log"
)

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
			//bytesBlock, _ := readFile(10)
			c.SendMyFrame([]byte{0x08, 0x04, 0x02})
			//c.SendMyFrame(1)

			//for {
			//	stream, err := c.AcceptStream(ctx)
			//	if err != nil {
			//		log.Println("stream accept error:", err)
			//		return
			//	}
			//	go func(s *quic.Stream) {
			//		buf := make([]byte, 4096)
			//		n, _ := s.Read(buf)
			//		log.Println("Server got:", string(buf[:n]))
			//	}(stream)
			//}
		}(conn)
	}
}

//func readFile(offset int64) ([]byte, error) {
//	const length int64 = 10
//
//	in, err := os.Open("./saved_movies/input.mp4")
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	defer func(in *os.File) {
//		err := in.Close()
//		if err != nil {
//
//		}
//	}(in)
//
//	buf := make([]byte, length)
//
//	n, err := in.ReadAt(buf, offset)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	return buf[:n], nil
//}

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
