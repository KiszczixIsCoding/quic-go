package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"log"
	"main/quic/stats"
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

type FillRequest struct {
	Offset uint64
	Size   uint64
}

type streamPacket struct {
	Offset uint64
	Data   []byte
}

func handleServerConn(parentCtx context.Context, conn *quic.Conn, s *SharedStateServer, logFilename string) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	var wg sync.WaitGroup

	startSending := make(chan struct{})
	fillChan := make(chan FillRequest, 4096)
	sendQueue := make(chan streamPacket, 2048)

	var currentOffset atomic.Uint64
	var sentPacketNumber atomic.Int64

	splitDataLogger := stats.GetInstance()
	splitDataLogger.Start(logFilename)
	defer splitDataLogger.Stop(logFilename)

	fileSize := utils.GetFileSize("../movie.mp4")

	// Goroutine 1: Single writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		str, err := conn.OpenUniStream()
		if err != nil {
			log.Println(err)
			return
		}
		defer str.Close()

		for pkt := range sendQueue {
			_, err := str.Write(pkt.Data)
			if err != nil {
				log.Printf("WRITE ERR: %T %#v\n", err, err)
				return
			}
			sent := sentPacketNumber.Add(1)
			fmt.Printf("SERWER WRITER #%d: offset=%d, size=%d\n",
				sent, pkt.Offset, len(pkt.Data)-16)
		}
	}()

	readerPoolDone := make(chan struct{})

	// Goroutine 2: Normal sending
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startSending

		s.mu.RLock()
		currentFileOffset := uint64(0)
		currentBlockSize := s.ServerBlockSize
		currentSkip := s.BlockSize
		currentBlockOffset := s.BlockOffset
		s.mu.RUnlock()

		startTime := time.Now()
		for currentFileOffset < fileSize {
			select {
			case <-ctx.Done():
				return
			default:
			}

			fmt.Println("CurrOffset: ", currentFileOffset)
			fmt.Println("ServerFileOffset: ", s.FileOffset)
			//if currentFileOffset > s.FileOffset {
			fmt.Println("Update danych!")
			currentBlockSize = s.ServerBlockSize
			currentBlockOffset = s.BlockOffset
			//}

			currentFileOffset += currentBlockOffset

			if currentFileOffset >= fileSize {
				currentFileOffset -= currentBlockOffset
			}

			actualBlockSize := currentBlockSize
			if currentFileOffset+actualBlockSize > fileSize {
				actualBlockSize = fileSize - currentFileOffset
			}

			data, err := utils.ReadChunk("../movie.mp4", int64(currentFileOffset), int(actualBlockSize))
			if err != nil {
				log.Printf("READ ERR: %T %#v\n", err, err)
				return
			}

			combined := make([]byte, 8+8+len(data))
			binary.BigEndian.PutUint64(combined[0:8], currentFileOffset)
			binary.BigEndian.PutUint64(combined[8:16], uint64(len(data)))
			copy(combined[16:], data)

			select {
			case sendQueue <- streamPacket{Offset: currentFileOffset, Data: combined}:
			case <-ctx.Done():
				return
			}

			currentOffset.Add(currentBlockSize)
			currentFileOffset += currentSkip - currentBlockOffset

			if actualBlockSize != currentBlockSize {
				break
			}
		}

		elapsed := time.Since(startTime)
		fmt.Printf("SERWER: normalne wysyłanie zakończone, czas=%v\n", elapsed)
		close(readerPoolDone)

		// Post-normal gap-fill reader
		for {
			select {
			case <-ctx.Done():
				return
			case fill := <-fillChan:
				if fill.Offset >= fileSize {
					continue
				}
				actualFillSize := fill.Size
				if fill.Offset+actualFillSize > fileSize {
					actualFillSize = fileSize - fill.Offset
				}
				data, err := utils.ReadChunk("../movie.mp4", int64(fill.Offset), int(actualFillSize))
				if err != nil {
					log.Printf("GAP-FILL READ ERR: %T %#v\n", err, err)
					continue
				}
				combined := make([]byte, 8+8+len(data))
				binary.BigEndian.PutUint64(combined[0:8], fill.Offset)
				binary.BigEndian.PutUint64(combined[8:16], uint64(len(data)))
				copy(combined[16:], data)
				select {
				case sendQueue <- streamPacket{Offset: fill.Offset, Data: combined}:
				case <-ctx.Done():
					return
				}
				currentOffset.Add(actualFillSize)
			}
		}
	}()

	// Goroutines 3-6: Reader pool (4 goroutines) for gap-fill
	const readerPoolSize = 4
	for i := 0; i < readerPoolSize; i++ {
		wg.Add(1)
		go func(rid int) {
			defer wg.Done()
			<-startSending
			for {
				select {
				case <-ctx.Done():
					return
				case <-readerPoolDone:
					return
				case fill := <-fillChan:
					if fill.Offset >= fileSize {
						continue
					}
					actualFillSize := fill.Size
					if fill.Offset+actualFillSize > fileSize {
						actualFillSize = fileSize - fill.Offset
					}
					data, err := utils.ReadChunk("../movie.mp4", int64(fill.Offset), int(actualFillSize))
					if err != nil {
						log.Printf("GAP-FILL READER #%d READ ERR: %T %#v\n", rid, err, err)
						continue
					}
					combined := make([]byte, 8+8+len(data))
					binary.BigEndian.PutUint64(combined[0:8], fill.Offset)
					binary.BigEndian.PutUint64(combined[8:16], uint64(len(data)))
					copy(combined[16:], data)
					select {
					case sendQueue <- streamPacket{Offset: fill.Offset, Data: combined}:
					case <-ctx.Done():
						return
					case <-readerPoolDone:
						return
					}
				}
			}
		}(i)
	}

	// Goroutine 7: SplitDataFrame receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstFrame := true

		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-conn.GetSplitDataFrameChannel():
				if !ok {
					return
				}

				splitDataLogger.Log(logFilename, stats.SplitDataFrameEntry{
					Timestamp:        time.Now(),
					Direction:        "received",
					FileOffset:       frame.FileOffset,
					BlockOffset:      frame.BlockOffset,
					BlockSize:        frame.BlockSize,
					ServerBlockSize:  frame.ServerBlockSize,
					ServerFileOffset: currentOffset.Load(),
				})

				s.mu.Lock()
				s.FileOffset = frame.FileOffset
				s.BlockOffset = frame.BlockOffset
				s.BlockSize = frame.BlockSize
				s.ServerBlockSize = frame.ServerBlockSize
				s.mu.Unlock()

				if firstFrame {
					firstFrame = false
					close(startSending)
				}
			}
		}
	}()

	// Goroutine 8: GapFillFrame receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		gapFillReceived := 0

		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-conn.GetGapFillFrameChannel():
				if !ok {
					return
				}
				if len(frame.Data) != 16 {
					continue
				}
				gapFillReceived++
				fillOffset := binary.BigEndian.Uint64(frame.Data[0:8])
				fillSize := binary.BigEndian.Uint64(frame.Data[8:16])
				select {
				case fillChan <- FillRequest{Offset: fillOffset, Size: fillSize}:
				default:
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

	// Tworzy folder `server_<param>/run_<timestamp>/`
	runDir := fmt.Sprintf("server_%s/run_%s", param, time.Now().Format("20060102_150405"))
	os.MkdirAll(runDir, 0755)

	// Tworzy listener
	listener, err := quic.ListenAddr(ip_address, tls, quicConf)

	if err != nil {
		log.Fatal(err)
	}

	log.Println("QUIC server listening on address: ", ip_address)
	log.Println("Run directory: ", runDir)

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
			<-c.HandshakeComplete()
			logFilename := fmt.Sprintf("%s/splitdata.csv", runDir)
			handleServerConn(ctx, c, s, logFilename)
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
