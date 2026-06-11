package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	_ "fmt"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"log"
	"main/quic/utils"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type SharedStateServer struct {
	mu              sync.RWMutex
	FileOffset      uint64 // Offset in file
	BlockOffset     uint64
	BlockSize       uint64
	ServerBlockSize uint64
	CurrentOffset   uint64 // current progress for this connection
}

func handleServerConn(parentCtx context.Context, conn *quic.Conn, s *SharedStateServer) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	startSending := make(chan struct{})

	// currentOffset tracks how much this connection has sent so far
	var currentOffset atomic.Uint64

	// Goroutine 1: wysyłanie
	go func() {
		defer wg.Done()

		str, err := conn.OpenUniStream()
		if err != nil {
			log.Println(err)
			return
		}
		defer str.Close()

		<-startSending

		s.mu.RLock()
		currentFileOffset := uint64(0)
		currentBlockSize := s.ServerBlockSize
		currentSkip := s.BlockSize
		currentBlockOffset := s.BlockOffset
		s.mu.RUnlock()

		sentPacketNumber := 0 // ⬅️ LICZNIK WYSŁANYCH PAKIETÓW
		startTime := time.Now()
		fmt.Println("WYSYŁANIE :)")
		for currentFileOffset < utils.GetFileSize("../movie.mp4") {
			sentPacketNumber++ // ⬅️ INKREMENTUJ
			select {
			case <-ctx.Done():
				fmt.Println("stop sending")
				return
			default:
				if currentFileOffset > s.FileOffset {
					currentBlockSize = s.BlockSize
					currentBlockOffset = s.BlockOffset
				}

				currentFileOffset += currentBlockOffset

				// Odczytaj okreslona liczbę bajtów
				fmt.Println("Status of sending: ", currentFileOffset, " / ", utils.GetFileSize("../movie.mp4"))
				data, err := utils.ReadChunk("../movie.mp4", int64(currentFileOffset), int(currentBlockSize))
				if err != nil {
					log.Printf("READ ERR: %T %#v\n", err, err)
					return
				}
				//////////////////////////////////////////////////////////////////////////

				// Przygotuj combined packet: [8 bytes offset] + [8 bytes length] + [data]
				combined := make([]byte, 8+8+len(data))
				binary.BigEndian.PutUint64(combined[0:8], currentFileOffset)
				binary.BigEndian.PutUint64(combined[8:16], uint64(len(data)))
				copy(combined[16:], data)
				//////////////////////////////////////////////////////////////////////////

				// Wysłij offset + length + dane w jednym Write'e
				n, err := str.Write(combined)
				fmt.Printf("SERWER WYSYŁA #%d: offset=%d, ActualBlockSize=%d, bytes sent=%d, currentOffset=%d\n",
					sentPacketNumber, int64(currentFileOffset), len(data), n, currentOffset.Load())

				if err != nil {
					log.Printf("WRITE ERR: %T %#v\n", err, err)
					return
				}
				//////////////////////////////////////////////////////////////////////////

				// Update currentOffset after sending
				currentOffset.Add(currentBlockSize)

				currentFileOffset += currentSkip - currentBlockOffset
			}
		}

		elapsed := time.Since(startTime)
		fmt.Printf("SERWER: plik wysłany w całości (packet #%d), czas=%v, zatrzymuję odbiór SplitDataFrame\n", sentPacketNumber, elapsed)
		cancel()
	}()

	// Goroutine 2: odbiór
	go func() {
		defer wg.Done()
		firstFrame := true
		receivedFrameNumber := 0 // ⬅️ LICZNIK ODEBRANYCH RAMEK

		for {
			select {
			case <-ctx.Done():
				fmt.Println("SERWER ODBIÓR: ctx.Done()")
				return
			case frame, ok := <-conn.GetSplitDataFrameChannel():
				receivedFrameNumber++ // ⬅️ INKREMENTUJ
				fmt.Printf("SERWER ODBIÓR: nowa ramka #%d, ok=%v, currentOffset=%d\n", receivedFrameNumber, ok, currentOffset.Load())
				if !ok {
					fmt.Println("SERWER ODBIÓR: kanał zamknięty")
					return
				}

				fmt.Printf("SERWER ODBIÓR #%d: fileOffset=%d, serverBlockSize=%d, blockOffset=%d, blockSize=%d\n",
					receivedFrameNumber, frame.FileOffset, frame.ServerBlockSize, frame.BlockOffset, frame.BlockSize)
				s.mu.Lock()
				s.FileOffset = frame.FileOffset
				s.BlockOffset = frame.BlockOffset
				s.BlockSize = frame.BlockSize
				s.ServerBlockSize = frame.ServerBlockSize
				s.mu.Unlock()

				if firstFrame {
					fmt.Println("FIRST FRAME")
					firstFrame = false
					close(startSending)
				}
			}
		}
	}()

	wg.Wait()
}

func main() {
	ctx := context.Background()

	tls, _ := loadTLSConfig("../cert.pem", "../key.pem")
	quicConf := &quic.Config{
		Tracer: qlog.DefaultConnectionTracer,
	}

	param := os.Args[1]

	ip_address := ""
	switch param {
	case "azure":
		ip_address = AZURE_IP_ADDRESS
	case "tul":
		ip_address = TUL_IP_ADDRESS
	case "local1":
		ip_address = LOCAL_IP_ADDRESS
	case "local2":
		ip_address = LOCAL_2_IP_ADDRESS
	}

	listener, err := quic.ListenAddr(ip_address, tls, quicConf)

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

		// Osobny stan dla każdego połączenia
		state := &SharedStateServer{
			FileOffset:      0,
			BlockOffset:     0,
			BlockSize:       0,
			ServerBlockSize: 0,
		}

		go func(c *quic.Conn, s *SharedStateServer) {
			// czekaj aż handshake się zakończy
			<-c.HandshakeComplete()

			//bytesBlock, _ := readFile(10)
			handleServerConn(ctx, c, s)
		}(conn, state)
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
