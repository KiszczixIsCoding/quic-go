package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"io"
	"log"
	"main/quic/stats"
	"main/quic/utils"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"
)

type Range struct {
	Start int64
	End   int64
}

type Stats struct {
	mu                sync.Mutex
	totalBytesRead    int64
	readCount         int64
	startTime         time.Time
	minThroughput     float64
	maxThroughput     float64
	currentThroughput float64
	minLatency        time.Duration
	maxLatency        time.Duration
	latencySum        time.Duration
}

func NewStats() *Stats {
	return &Stats{
		startTime: time.Now(),
	}
}

func (st *Stats) RecordRead(bytesRead int, latency time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.totalBytesRead += int64(bytesRead)
	st.readCount++

	// Throughput z tego read'a
	if latency.Seconds() > 0 {
		throughput := float64(bytesRead) / latency.Seconds() / 1024 / 1024 // MB/s
		st.currentThroughput = throughput
		if st.minThroughput == 0 || throughput < st.minThroughput {
			st.minThroughput = throughput
		}
		if throughput > st.maxThroughput {
			st.maxThroughput = throughput
		}
	}

	// Latency
	if st.minLatency == 0 || latency < st.minLatency {
		st.minLatency = latency
	}
	if latency > st.maxLatency {
		st.maxLatency = latency
	}
	st.latencySum += latency
}

func (st *Stats) GetCurrentThroughput() float64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.currentThroughput
}

func (st *Stats) PrintStats(connID string, currentThroughput float64, currentLatency time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Średnia przepustowość od startu
	elapsedSeconds := time.Since(st.startTime).Seconds()
	var avgThroughput float64
	if elapsedSeconds > 0 {
		avgThroughput = float64(st.totalBytesRead) / elapsedSeconds / 1024 / 1024 // MB/s
	}

	// Średnie opóźnienie
	var avgLatency time.Duration
	if st.readCount > 0 {
		avgLatency = st.latencySum / time.Duration(st.readCount)
	}

	fmt.Printf("[%s] STATS:\n", connID)
	fmt.Printf("  Przepustowość: aktualna=%.2f MB/s, min=%.2f MB/s, max=%.2f MB/s, avg=%.2f MB/s\n",
		currentThroughput, st.minThroughput, st.maxThroughput, avgThroughput)
	fmt.Printf("  Opóźnienie:    aktualne=%v, min=%v, max=%v, avg=%v\n",
		currentLatency, st.minLatency, st.maxLatency, avgLatency)
}

type ReceivedRanges struct {
	mu            sync.Mutex
	ranges        []Range
	currentOffset int64
}

func (rr *ReceivedRanges) AddRange(start, end int64) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.ranges = append(rr.ranges, Range{Start: start, End: end})
	if end > rr.currentOffset {
		rr.currentOffset = end
	}
}

func (rr *ReceivedRanges) GetRanges() []Range {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	result := make([]Range, len(rr.ranges))
	copy(result, rr.ranges)
	return result
}

func (rr *ReceivedRanges) GetCurrentOffset() int64 {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.currentOffset
}

// IsRangeCovered sprawdza czy dany zakres jest już pokryty
func (rr *ReceivedRanges) IsRangeCovered(start, end int64) bool {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	for _, r := range rr.ranges {
		if r.Start <= start && r.End >= end {
			return true
		}
	}
	return false
}

// GetUncoveredPortion zwraca część zakresu [start, end) która nie jest jeszcze pokryta
// Zwraca (newStart, newEnd, isCovered)
// Jeśli cały zakres jest pokryty, isCovered = true
// Jeśli część jest pokryta, zwraca niezakrytą część
func (rr *ReceivedRanges) GetUncoveredPortion(start, end int64) (int64, int64, bool) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// Sprawdź czy cały zakres jest pokryty
	for _, r := range rr.ranges {
		if r.Start <= start && r.End >= end {
			return 0, 0, true
		}
	}

	// Jeśli żaden zakres nie pokrywa - zwróć cały zakres
	for _, r := range rr.ranges {
		if r.Start <= start && r.End > start {
			// Część jest pokryta od r.End
			if r.End >= end {
				return 0, 0, true
			}
			return r.End, end, false
		}
	}

	// Nic nie pokrywa, zwróć cały zakres
	return start, end, false
}

func formatConnStats(conn *quic.Conn) string {
	c := conn.ConnectionStats()
	return fmt.Sprintf(
		`========== QUIC Connection Stats ==========
%-20s : %v
%-20s : %v
%-20s : %v
%-20s : %v
---------- Traffic ----------
%-20s : %d bytes
%-20s : %d packets
%-20s : %d bytes
%-20s : %d packets
%-20s : %d bytes
%-20s : %d packets
============================================`,
		"Min RTT", c.MinRTT,
		"Latest RTT", c.LatestRTT,
		"Smoothed RTT", c.SmoothedRTT,
		"Mean Deviation", c.MeanDeviation,
		"Bytes Sent", c.BytesSent,
		"Packets Sent", c.PacketsSent,
		"Bytes Received", c.BytesReceived,
		"Packets Received", c.PacketsReceived,
		"Bytes Lost", c.BytesLost,
		"Packets Lost", c.PacketsLost,
	)
}

type LogEntry struct {
	Timestamp  time.Time
	ConnID     string
	Offset     uint64
	DataSize   int
	Throughput float64
}

var logChan chan LogEntry

func startLogger(filename string) *os.File {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create log file: %v", err)
	}

	logChan = make(chan LogEntry, 1000)
	go func() {
		for entry := range logChan {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s | %s | offset=%d, dataSize=%d, throughput=%.2f MB/s\n",
				ts, entry.ConnID, entry.Offset, entry.DataSize, entry.Throughput)
			_, err := f.WriteString(line)
			if err != nil {
				log.Printf("Log write error: %v", err)
			}
		}
		f.Close()
	}()
	return f
}

func stopLogger() {
	close(logChan)
}

type Data struct {
	ConnID   string
	StreamID int64
	Payload  []byte
}

type SharedStateClient struct {
	mu              sync.RWMutex
	FileOffset      uint64 // Offset in file
	BlockOffset     uint64
	BlockSize       uint64
	ServerBlockSize uint64
}

func handleClientConn(ctx context.Context, conn *quic.Conn, connID string, out chan<- Data, ranges *ReceivedRanges, stats *Stats, finished *atomic.Bool, currentOffset *atomic.Uint64) {
	defer close(out)
	fmt.Println("Handle connection: ", connID)
	fileSize := utils.GetFileSize("../movie.mp4")

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] handleClientConn: ctx.Done()\n", connID)
			return
		default:
		}

		stream, err := conn.AcceptUniStream(ctx)
		if err != nil {
			return
		}

		go func(s *quic.ReceiveStream) {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				readStartTime := time.Now()

				headerBuf := make([]byte, 8)
				_, err := io.ReadFull(s, headerBuf)
				if err != nil {
					return
				}
				fileOffset := binary.BigEndian.Uint64(headerBuf)

				// 2. Czytaj length (8 bajtów)
				lengthBuf := make([]byte, 8)
				io.ReadFull(s, lengthBuf)
				dataLength := binary.BigEndian.Uint64(lengthBuf)

				// 3. Czytaj dokładnie tyle danych
				dataBuf := make([]byte, dataLength)
				io.ReadFull(s, dataBuf)
				fmt.Println("SIZES: ", fileOffset, dataLength, len(dataBuf))

				readLatency := time.Since(readStartTime)

				// Oblicz przepustowość
				var currentThroughput float64
				if readLatency.Seconds() > 0 {
					currentThroughput = float64(len(dataBuf)) / readLatency.Seconds() / 1024 / 1024 // kB/s
				}

				fmt.Printf("[%s] Otrzymałem packet: offset=%d, dataSize=%d, throughput=%.2f MB/s\n", connID, fileOffset, len(dataBuf), currentThroughput)

				// Log do pliku (async, non-blocking)
				select {
				case logChan <- LogEntry{
					Timestamp:  time.Now(),
					ConnID:     connID,
					Offset:     fileOffset,
					DataSize:   len(dataBuf),
					Throughput: currentThroughput,
				}:
				default:
				}

				// Zapisz statystyki
				stats.RecordRead(len(dataBuf), readLatency)
				stats.PrintStats(connID, currentThroughput, readLatency)

				// Zaktualizuj zakresy
				rangeStart := int64(fileOffset)
				rangeEnd := int64(fileOffset) + int64(dataLength)
				ranges.AddRange(rangeStart, rangeEnd)

				// Zaktualizuj currentOffset dla tego połączenia
				currentOffset.Add(dataLength)

				// Sprawdź czy cały plik został odebrany (łącznie z obu połączeń)
				if ranges.GetCurrentOffset() >= int64(fileSize) {
					fmt.Println(currentOffset.Load())
					fmt.Printf("KLIENT [%s]: cały plik odebrany! (rangesOffset=%d / fileSize=%d)\n", connID, ranges.GetCurrentOffset(), fileSize)
					finished.Store(true)
					return
				}
			}
		}(stream)
	}
}

func runConnection(addr string, connID string, wg *sync.WaitGroup, out chan<- ConnResult, ranges *ReceivedRanges, stats *Stats, finished *atomic.Bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"quic-echo-example"},
	}

	quicConf := &quic.Config{
		Tracer: qlog.DefaultConnectionTracer,
	}

	defer wg.Done()
	connAzure, err := quic.DialAddr(ctx, addr, tlsConf, quicConf)
	if err != nil {
		log.Println("azure err:", err)
		return
	}

	currentOffset := &atomic.Uint64{}
	out <- ConnResult{
		ID:            connID,
		Conn:          connAzure,
		CurrentOffset: currentOffset,
	}
	channel := make(chan Data, 100)

	go handleClientConn(ctx, connAzure, connID, channel, ranges, stats, finished, currentOffset)

	for data := range channel {
		fmt.Printf("[conn2][%d]: %s\n", data.StreamID, string(data.Payload))
	}
}

type ConnResult struct {
	ID            string
	Conn          *quic.Conn
	Err           error
	CurrentOffset *atomic.Uint64
}

func main() {
	//var wg sync.WaitGroup
	done := make(chan struct{})

	// Start async file logger
	_ = startLogger("packet_log2.txt")
	defer stopLogger()

	splitDataLogger := stats.GetInstance()
	splitDataLogger.Start("stats/splitdata_conn1.csv")
	splitDataLogger.Start("stats/splitdata_conn2.csv")
	defer splitDataLogger.Stop("stats/splitdata_conn1.csv")
	defer splitDataLogger.Stop("stats/splitdata_conn2.csv")

	var wg sync.WaitGroup
	wg.Add(2)
	connCh := make(chan ConnResult, 2)
	ranges := &ReceivedRanges{}
	stats1 := NewStats()
	stats2 := NewStats()
	var finished atomic.Bool

	go runConnection(LOCAL_IP_ADDRESS, "conn1", &wg, connCh, ranges, stats1, &finished)
	go runConnection(TUL_IP_PUBLIC_ADDRESS, "conn2", &wg, connCh, ranges, stats2, &finished)

	//go func() {
	conn1, conn2 := <-connCh, <-connCh
	currentProgress := uint64(0) // ⬅️ TUTAJ - zmienna przed pętlą
	frameNumber := 0             // ⬅️ LICZNIK RAMEK

	for {
		if finished.Load() {
			fmt.Println("KLIENT: plik w całości odebrany, zatrzymuję wysyłanie SplitDataFrame")
			break
		}

		frameNumber++ // ⬅️ INKREMENTUJ NA STARCIE ITERACJI
		rtt1 := conn1.Conn.ConnectionStats().SmoothedRTT
		rtt2 := conn2.Conn.ConnectionStats().SmoothedRTT

		minSRTT := func() time.Duration {
			if rtt1 < rtt2 {
				return rtt1
			}
			return rtt2
		}()

		//if minSRTT < 10*time.Millisecond {
		//	minSRTT = 10 * time.Millisecond
		//}

		fmt.Println("Min sRTT: ", minSRTT)
		time.Sleep(minSRTT)
		throughput1 := stats1.GetCurrentThroughput()
		throughput2 := stats2.GetCurrentThroughput()

		var curr1, curr2 uint64

		// Oblicz curr1 i curr2 na bazie stosunku throughput'u
		totalThroughput := throughput1 + throughput2
		totalBlockSize := uint64(BLOCK_SIZE_MULTIPLIER * MTU)

		if totalThroughput > 0 {
			// Stosunek przepustowości
			curr1 = uint64((throughput1 / totalThroughput) * float64(totalBlockSize))
			curr2 = totalBlockSize - curr1
		} else {
			// Jeśli brak danych, równy podział
			curr1 = totalBlockSize / 2
			curr2 = totalBlockSize / 2
		}

		fmt.Println("throughput 1 ", throughput1)
		fmt.Println("throughput 2 ", throughput2)
		fmt.Println("curr1 ", curr1)
		fmt.Println("curr2 ", curr2)
		fmt.Printf("KLIENT: wysyłam SplitDataFrame #%d - curr1=%d, curr2=%d\n", frameNumber, curr1, curr2)

		fileOffset1 := conn1.CurrentOffset.Load()
		fileOffset2 := conn2.CurrentOffset.Load()

		splitDataLogger.Log("stats/splitdata_conn1.csv", stats.SplitDataFrameEntry{
			Timestamp:        time.Now(),
			Direction:        "sent",
			FileOffset:       fileOffset1,
			BlockOffset:      0,
			BlockSize:        totalBlockSize,
			ServerBlockSize:  curr1,
			ServerFileOffset: fileOffset1,
		})
		splitDataLogger.Log("stats/splitdata_conn2.csv", stats.SplitDataFrameEntry{
			Timestamp:        time.Now(),
			Direction:        "sent",
			FileOffset:       fileOffset2,
			BlockOffset:      curr1,
			BlockSize:        totalBlockSize,
			ServerBlockSize:  curr2,
			ServerFileOffset: fileOffset2,
		})

		go conn1.Conn.SendSplitDataFrame(fileOffset1, 0, totalBlockSize, curr1)
		go conn2.Conn.SendSplitDataFrame(fileOffset2, curr1, totalBlockSize, curr2)

		fmt.Printf("KLIENT: wysłano SplitDataFrame #%d\n", frameNumber)

		// Inkrementuj currentProgress
		currentProgress += totalBlockSize

		// Wyświetl aktualne zakresy
		currentRanges := ranges.GetRanges()
		fmt.Printf("Aktualne zakresy: %v\n", currentRanges)

	}
	//}()
	wg.Wait()

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
