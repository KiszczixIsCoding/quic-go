package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"log"
	"os"
	"os/signal"
)

func readLoop(name string, s *quic.Stream) {
	buf := make([]byte, 4096)
	for {
		n, err := s.Read(buf)
		if err != nil {
			return
		}
		log.Printf("[%s] %s", name, buf[:n])
	}
}

func saveFile(data []byte) {
	file, err := os.OpenFile(
		"output.bin",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)

	if err != nil {
		log.Fatal(err)
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {

		}
	}(file)

	n, err := file.Write(data)
	if err != nil {
		log.Fatal(err)
	}

	if n != len(data) {
		log.Fatal("nie zapisano wszystkich bajtów")
	}
}

func main() {
	ctx := context.Background()
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"quic-echo-example"},
	}

	quicConf := &quic.Config{
		Tracer: qlog.DefaultConnectionTracer,
	}

	done := make(chan struct{})

	go func() {
		defer close(done)
		log.Println("Dialing :4443")
		conn, err := quic.DialAddr(ctx, "localhost:4443", tlsConf, quicConf)
		if err != nil {
			log.Println("FAILED :4443:", err.Error())
			return
		}

		for frame := range conn.GetTulCustomFrameChannel() {
			fmt.Println("🔥 Otrzymałem 0x21")
			data := frame.Data
			fmt.Println("Payload raw:", data)

			// jeśli to string
			fmt.Println("As string:", string(data))

			// jeśli to liczba uint32
			if len(data) >= 4 {
				value := binary.BigEndian.Uint32(data[:4])
				fmt.Println("Odczytana liczba:", value)
			}
			//saveFile(data)
		}

		for {
			fmt.Println("AcceptSTREAM")
			stream, err := conn.AcceptStream(ctx)
			if err != nil {
				log.Println("Stream accept error:", err)
				break
			}
			fmt.Println("AcceptBUFF")
			buf := make([]byte, 4096)
			n, _ := stream.Read(buf)
			fmt.Println("Data received:", buf[:n])
		}

		log.Println("CONNECTED :4443")
	}()

	// teraz main czeka bez deadlocka
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-done:
		log.Println("Client goroutine finished, exiting")
	case <-sig:
		log.Println("Interrupt received, exiting")
	}
}
