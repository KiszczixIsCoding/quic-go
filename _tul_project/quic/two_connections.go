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
	ststats "main/quic/stats"
	"main/quic/utils"
	"math"
	"os"
	"os/signal"
	"sort"
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
	// Window-based throughput
	windowBytes int64
	windowStart time.Time
}

func NewStats() *Stats {
	return &Stats{
		startTime: time.Now(),
	}
}

func (st *Stats) StartWindow() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.windowBytes = 0
	st.windowStart = time.Now()
}

func (st *Stats) RecordRead(bytesRead int, latency time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.totalBytesRead += int64(bytesRead)
	st.readCount++
	st.windowBytes += int64(bytesRead)

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
	elapsed := time.Since(st.windowStart).Seconds()
	if elapsed <= 0 {
		return 0
	}
	throughput := float64(st.windowBytes) / elapsed / 1024 / 1024
	if st.minThroughput == 0 || throughput < st.minThroughput {
		st.minThroughput = throughput
	}
	if throughput > st.maxThroughput {
		st.maxThroughput = throughput
	}
	st.currentThroughput = throughput
	return throughput
}

func (st *Stats) BytesPerRTT(rtt time.Duration) uint64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	elapsed := time.Since(st.windowStart).Seconds()
	if elapsed <= 0 || rtt <= 0 {
		return 0
	}
	throughputBytesPerSec := float64(st.windowBytes) / elapsed
	result := throughputBytesPerSec * rtt.Seconds()
	if result < 0 {
		return 0
	}
	return uint64(result)
}

func (st *Stats) GetTotalBytes() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.totalBytesRead
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

type ReceivedBuffer struct {
	mu   sync.Mutex
	data []byte
	size int64
}

func NewReceivedBuffer(size int64) *ReceivedBuffer {
	return &ReceivedBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (rb *ReceivedBuffer) Write(offset int64, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	end := offset + int64(len(data))
	if end > rb.size {
		end = rb.size
	}
	copy(rb.data[offset:end], data[:end-offset])
}

func (rb *ReceivedBuffer) Save(path string) error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return os.WriteFile(path, rb.data, 0644)
}

func CountCombinedGaps(r1, r2 *ReceivedRanges, fileSize int64) int {
	_, gaps := GetCombinedGaps(r1, r2, fileSize)
	return len(gaps)
}

func GetCombinedGaps(r1, r2 *ReceivedRanges, fileSize int64) ([]Range, []Range) {
	r1.mu.Lock()
	r2.mu.Lock()
	defer r1.mu.Unlock()
	defer r2.mu.Unlock()

	merged := make([]Range, 0, len(r1.ranges)+len(r2.ranges))
	merged = append(merged, r1.ranges...)
	merged = append(merged, r2.ranges...)

	if len(merged) == 0 {
		return nil, nil
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Start < merged[j].Start
	})

	combined := []Range{merged[0]}
	for i := 1; i < len(merged); i++ {
		last := &combined[len(combined)-1]
		if merged[i].Start <= last.End {
			if merged[i].End > last.End {
				last.End = merged[i].End
			}
		} else {
			combined = append(combined, merged[i])
		}
	}

	var gaps []Range
	if combined[0].Start > 0 {
		gaps = append(gaps, Range{Start: 0, End: combined[0].Start})
	}
	for i := 1; i < len(combined); i++ {
		if combined[i].Start > combined[i-1].End {
			gaps = append(gaps, Range{Start: combined[i-1].End, End: combined[i].Start})
		}
	}
	last := combined[len(combined)-1]
	if last.End < fileSize {
		gaps = append(gaps, Range{Start: last.End, End: fileSize})
	}
	return combined, gaps
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

// CountGaps zwraca liczbę przerw w zakresie [0, currentOffset)
func (rr *ReceivedRanges) CountGaps() int {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if len(rr.ranges) == 0 {
		return 0
	}

	// Kopia i sortowanie po start
	sorted := make([]Range, len(rr.ranges))
	copy(sorted, rr.ranges)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start < sorted[j].Start
	})

	// Scalanie nakładających się zakresów
	merged := []Range{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		last := &merged[len(merged)-1]
		if sorted[i].Start <= last.End {
			if sorted[i].End > last.End {
				last.End = sorted[i].End
			}
		} else {
			merged = append(merged, sorted[i])
		}
	}

	// Liczenie przerw
	gaps := 0
	if merged[0].Start > 0 {
		gaps++
	}
	for i := 1; i < len(merged); i++ {
		if merged[i].Start > merged[i-1].End {
			gaps++
		}
	}
	return gaps
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

	f.WriteString("timestamp,conn_id,offset,data_size,throughput\n")

	logChan = make(chan LogEntry, 1000)
	go func() {
		for entry := range logChan {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s,%s,%d,%d,%.2f\n",
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

func handleClientConn(ctx context.Context, conn *quic.Conn, connID string, out chan<- Data, ranges *ReceivedRanges, stats *Stats, finished *atomic.Bool, currentOffset *atomic.Uint64, rangeLogger *ststats.ReceivedRangeLogger, rangeFile string, gapSignal chan<- struct{}, buf *ReceivedBuffer) {
	defer close(out)
	fmt.Println("Handle connection: ", connID)

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

				readLatency := time.Since(readStartTime)

				// Oblicz przepustowość
				var currentThroughput float64
				if readLatency.Seconds() > 0 {
					currentThroughput = float64(len(dataBuf)) / readLatency.Seconds() / 1024 / 1024 // kB/s
				}

				// Log do stats/received_packets.csv
				ststats.GetReceivedPacketLogger().Log("stats/received_packets/received_packets.csv", ststats.ReceivedPacketEntry{
					Timestamp:  time.Now(),
					ConnID:     connID,
					DataSize:   len(dataBuf),
					Offset:     fileOffset,
					Throughput: currentThroughput,
				})

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

				// Zaktualizuj zakresy
				rangeStart := int64(fileOffset)
				rangeEnd := int64(fileOffset) + int64(dataLength)
				ranges.AddRange(rangeStart, rangeEnd)

				if buf != nil {
					buf.Write(rangeStart, dataBuf)
				}

				// Signal gap-fill goroutine
				select {
				case gapSignal <- struct{}{}:
				default:
				}

				// Log zakres do pliku
				rangeLogger.Log(rangeFile, ststats.ReceivedRangeEntry{
					Timestamp: time.Now(),
					ConnID:    connID,
					Start:     rangeStart,
					End:       rangeEnd,
				})

				// Zaktualizuj currentOffset dla tego połączenia
				currentOffset.Add(dataLength)
			}
		}(stream)
	}
}

func runConnection(addr string, connID string, wg *sync.WaitGroup, out chan<- ConnResult, ranges *ReceivedRanges, stats *Stats, finished *atomic.Bool, rangeLogger *ststats.ReceivedRangeLogger, rangeFile string, gapSignal chan<- struct{}, buf *ReceivedBuffer) {
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
		out <- ConnResult{ID: connID, Err: err}
		return
	}

	currentOffset := &atomic.Uint64{}
	out <- ConnResult{
		ID:            connID,
		Conn:          connAzure,
		CurrentOffset: currentOffset,
	}
	channel := make(chan Data, 100)

	go handleClientConn(ctx, connAzure, connID, channel, ranges, stats, finished, currentOffset, rangeLogger, rangeFile, gapSignal, buf)

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

type ThroughputSample struct {
	Timestamp       time.Time
	Conn1Throughput float64
	Conn2Throughput float64
	Conn1TotalBytes int64
	Conn2TotalBytes int64
}

var throughputLogChan chan ThroughputSample

func startThroughputLogger(filename string) {
	throughputLogChan = make(chan ThroughputSample, 1000)
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create throughput log file: %v", err)
	}

	f.WriteString("timestamp,conn1_throughput_mbps,conn2_throughput_mbps,conn1_total_bytes,conn2_total_bytes\n")
	f.Sync()

	go func() {
		for entry := range throughputLogChan {
			ts := entry.Timestamp.Format("2006-01-02T15:04:05.000000")
			line := fmt.Sprintf("%s,%.2f,%.2f,%d,%d\n",
				ts, entry.Conn1Throughput, entry.Conn2Throughput, entry.Conn1TotalBytes, entry.Conn2TotalBytes)
			_, _ = f.WriteString(line)
		}
		f.Close()
	}()
}

func stopThroughputLogger() {
	close(throughputLogChan)
}

func main() {
	done := make(chan struct{})

	// Create run directory with parameters
	runDir := fmt.Sprintf("runs/run_%s", time.Now().Format("20060102_150405"))
	os.MkdirAll(runDir, 0755)
	paramsFile := fmt.Sprintf("%s/params.txt", runDir)
	paramsContent := fmt.Sprintf("MTU: %d\nSPLIT_TYPE: %s\nSCOPE: %s\nBLOCK_SIZE_MULTIPLIER: %d\n", MTU, SPLIT_TYPE, SCOPE, BLOCK_SIZE_MULTIPLIER)
	os.WriteFile(paramsFile, []byte(paramsContent), 0644)

	_ = startLogger(fmt.Sprintf("%s/packet_log2.csv", runDir))
	defer stopLogger()

	startThroughputLogger(fmt.Sprintf("%s/throughput_periodic.csv", runDir))
	defer stopThroughputLogger()

	splitDataLogger := ststats.GetInstance()
	splitData1 := fmt.Sprintf("%s/split_data/splitdata_conn1.csv", runDir)
	splitData2 := fmt.Sprintf("%s/split_data/splitdata_conn2.csv", runDir)
	splitDataLogger.Start(splitData1)
	splitDataLogger.Start(splitData2)
	defer splitDataLogger.Stop(splitData1)
	defer splitDataLogger.Stop(splitData2)

	receivedLogger := ststats.GetReceivedPacketLogger()
	receivedLogger.Start("stats/received_packets/received_packets.csv")
	defer receivedLogger.Stop("stats/received_packets/received_packets.csv")

	rangeLogger := ststats.GetReceivedRangeLogger()
	rangeLogger.Start("stats/received_ranges/received_ranges_conn1.csv")
	rangeLogger.Start("stats/received_ranges/received_ranges_conn2.csv")
	defer rangeLogger.Stop("stats/received_ranges/received_ranges_conn1.csv")
	defer rangeLogger.Stop("stats/received_ranges/received_ranges_conn2.csv")

	gapLogger := ststats.GetGapLogger()
	gapLogFile := fmt.Sprintf("%s/gaps.csv", runDir)
	gapLogger.Start(gapLogFile)
	defer gapLogger.Stop(gapLogFile)

	gapFillLogger := ststats.GetGapFillLogger()
	gapFillLogFile := fmt.Sprintf("%s/gap_fill_sent.csv", runDir)
	gapFillLogger.Start(gapFillLogFile)
	defer gapFillLogger.Stop(gapFillLogFile)

	gapDetailLogger := ststats.GetGapDetailLogger()
	gapDetailLogFile := fmt.Sprintf("%s/gap_details.csv", runDir)
	gapDetailLogger.Start(gapDetailLogFile)
	defer gapDetailLogger.Stop(gapDetailLogFile)

	var wg sync.WaitGroup
	wg.Add(2)
	connCh := make(chan ConnResult, 2)
	ranges1 := &ReceivedRanges{}
	ranges2 := &ReceivedRanges{}
	stats1 := NewStats()
	stats2 := NewStats()
	var finished atomic.Bool
	gapSignal := make(chan struct{}, 1)
	receivedBuf := NewReceivedBuffer(int64(utils.GetFileSize("../movie.mp4")))

	if SCOPE == "LOCAL" {
		go runConnection(LOCAL_IP_ADDRESS, "conn1", &wg, connCh, ranges1, stats1, &finished, rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn1.csv", runDir), gapSignal, receivedBuf)
		go runConnection(LOCAL_2_IP_ADDRESS, "conn2", &wg, connCh, ranges2, stats2, &finished, rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn2.csv", runDir), gapSignal, receivedBuf)
	} else {
		go runConnection(AZURE_IP_PUBLIC_ADDRESS, "conn1", &wg, connCh, ranges1, stats1, &finished, rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn1.csv", runDir), gapSignal, receivedBuf)
		go runConnection(TUL_IP_PUBLIC_ADDRESS, "conn2", &wg, connCh, ranges2, stats2, &finished, rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn2.csv", runDir), gapSignal, receivedBuf)
	}

	conn1, conn2 := <-connCh, <-connCh
	if conn1.Err != nil || conn2.Err != nil {
		log.Fatalf("Nie udało się połączyć: conn1=%v conn2=%v", conn1.Err, conn2.Err)
	}

	// Periodic throughput sampler — logs every 500ms
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var prevBytes1, prevBytes2 int64
		var prevTime time.Time
		prevTime = time.Now()

		for {
			if finished.Load() {
				now := time.Now()
				elapsed := now.Sub(prevTime).Seconds()
				if elapsed > 0 {
					curr1Bytes := stats1.GetTotalBytes()
					curr2Bytes := stats2.GetTotalBytes()
					tp1 := float64(curr1Bytes-prevBytes1) / elapsed / 1024 / 1024
					tp2 := float64(curr2Bytes-prevBytes2) / elapsed / 1024 / 1024
					select {
					case throughputLogChan <- ThroughputSample{
						Timestamp:       now,
						Conn1Throughput: tp1,
						Conn2Throughput: tp2,
						Conn1TotalBytes: curr1Bytes,
						Conn2TotalBytes: curr2Bytes,
					}:
					default:
					}
				}
				return
			}

			<-ticker.C
			now := time.Now()
			elapsed := now.Sub(prevTime).Seconds()
			if elapsed <= 0 {
				continue
			}
			curr1Bytes := stats1.GetTotalBytes()
			curr2Bytes := stats2.GetTotalBytes()
			tp1 := float64(curr1Bytes-prevBytes1) / elapsed / 1024 / 1024
			tp2 := float64(curr2Bytes-prevBytes2) / elapsed / 1024 / 1024
			prevBytes1 = curr1Bytes
			prevBytes2 = curr2Bytes
			prevTime = now

			select {
			case throughputLogChan <- ThroughputSample{
				Timestamp:       now,
				Conn1Throughput: tp1,
				Conn2Throughput: tp2,
				Conn1TotalBytes: curr1Bytes,
				Conn2TotalBytes: curr2Bytes,
			}:
			default:
			}
		}
	}()

	currentProgress := uint64(0)
	gapFillSent := 0
	loggedGaps := make(map[int64]bool)
	conn1Finished := false
	conn2Finished := false
	var prevServerOffset1, prevServerOffset2 uint64
	conn1Stagnant := 0
	conn2Stagnant := 0
	const stagnantThreshold = 2
	var tp1Val atomic.Uint64
	var tp2Val atomic.Uint64

	// Throughput sampler: mierzy co sRTT
	var tpBytes1, tpBytes2 int64
	var tpTime time.Time
	tpTime = time.Now()

	// Gap-fill goroutine (disabled for WRR)
	gapFillDone := make(chan struct{})
	if SPLIT_TYPE == "WRR" {
		close(gapFillDone)
	}

	gapFillCtx, gapFillCancel := context.WithCancel(context.Background())
	gapTick := time.NewTicker(1 * time.Millisecond)
	sentGapFills := make(map[int64]bool)
	if SPLIT_TYPE != "WRR" {
		go func() {
			defer close(gapFillDone)
			defer gapTick.Stop()
			for {
				select {
				case <-gapFillCtx.Done():
					return
				case <-gapSignal:
				case <-gapTick.C:
				}
				fileSize := utils.GetFileSize("../movie.mp4")
				offset1 := ranges1.GetCurrentOffset()
				offset2 := ranges2.GetCurrentOffset()
				_, gapIntervals := GetCombinedGaps(ranges1, ranges2, int64(fileSize))
				if len(gapIntervals) == 0 {
					continue
				}

				limitOffset := max(offset1, offset2)

				tp1 := math.Float64frombits(tp1Val.Load())
				tp2 := math.Float64frombits(tp2Val.Load())
				fasterName := "conn1"
				fasterConn := conn1.Conn
				if tp2 > tp1 {
					fasterName = "conn2"
					fasterConn = conn2.Conn
				}

				for _, g := range gapIntervals {
					if g.End > limitOffset {
						continue
					}
					if sentGapFills[g.Start] {
						continue
					}
					gapSize := uint64(g.End - g.Start)
					if gapSize == 0 {
						continue
					}
					sentGapFills[g.Start] = true
					gapFillSent++
					gapFillLogger.Log(gapFillLogFile, ststats.GapFillEntry{
						Timestamp: time.Now(),
						ConnID:    fasterName,
						Seq:       gapFillSent,
						Offset:    g.Start,
						Size:      int64(gapSize),
					})
					fasterConn.SendGapFillFrame(uint64(g.Start), gapSize)
				}
			}
		}()
	}

	for {
		if finished.Load() {
			fmt.Println("KLIENT: plik w całości odebrany, zatrzymuję wysyłanie SplitDataFrame")
			break
		}

		fileSize := utils.GetFileSize("../movie.mp4")
		offset1 := ranges1.GetCurrentOffset()
		offset2 := ranges2.GetCurrentOffset()
		_, gapIntervals := GetCombinedGaps(ranges1, ranges2, int64(fileSize))
		combinedGaps := len(gapIntervals)
		if max(offset1, offset2) >= int64(fileSize) && combinedGaps == 0 {
			fmt.Printf("KLIENT: cały plik odebrany! conn1(offset=%d, gaps=%d), conn2(offset=%d, gaps=%d), fileSize=%d\n",
				offset1, ranges1.CountGaps(), offset2, ranges2.CountGaps(), fileSize)
			finished.Store(true)
			break
		}

		stats1.StartWindow()
		stats2.StartWindow()
		rtt1 := conn1.Conn.ConnectionStats().SmoothedRTT
		rtt2 := conn2.Conn.ConnectionStats().SmoothedRTT

		maxSRTT := func() time.Duration {
			if rtt1 > rtt2 {
				return rtt1
			}
			return rtt2
		}()

		_, gapIntervals = GetCombinedGaps(ranges1, ranges2, int64(fileSize))
		combinedGaps = len(gapIntervals)

		offset1 = ranges1.GetCurrentOffset()
		offset2 = ranges2.GetCurrentOffset()

		serverOffset1 := conn1.CurrentOffset.Load()
		serverOffset2 := conn2.CurrentOffset.Load()
		if serverOffset1 > prevServerOffset1 {
			conn1Stagnant = 0
		} else if serverOffset1 > 0 {
			conn1Stagnant++
		}
		if serverOffset2 > prevServerOffset2 {
			conn2Stagnant = 0
		} else if serverOffset2 > 0 {
			conn2Stagnant++
		}
		prevServerOffset1 = serverOffset1
		prevServerOffset2 = serverOffset2
		if conn1Stagnant >= stagnantThreshold && !conn1Finished {
			conn1Finished = true
		}
		if conn2Stagnant >= stagnantThreshold && !conn2Finished {
			conn2Finished = true
		}

		maxOffset := max(offset1, offset2)
		minOffset := min(offset1, offset2)
		gapsBeforeMin := 0
		gapsBetweenOffset := 0
		for _, g := range gapIntervals {
			if g.End <= minOffset {
				gapsBeforeMin++
			}
			if g.Start >= minOffset && g.End <= maxOffset {
				gapsBetweenOffset++
			}
		}

		gapLogger.Log(gapLogFile, ststats.GapEntry{
			Timestamp:         time.Now(),
			CurrentOffset:     maxOffset,
			Conn1Offset:       offset1,
			Conn2Offset:       offset2,
			Gaps:              combinedGaps,
			GapsBeforeMin:     gapsBeforeMin,
			GapsBetweenOffset: gapsBetweenOffset,
		})
		for _, g := range gapIntervals {
			if g.End > maxOffset {
				continue
			}
			if loggedGaps[g.Start] {
				continue
			}
			loggedGaps[g.Start] = true
			gapDetailLogger.Log(gapDetailLogFile, ststats.GapDetailEntry{
				Timestamp: time.Now(),
				Offset:    maxOffset,
				Start:     g.Start,
				End:       g.End,
				Size:      g.End - g.Start,
			})
		}

		var curr1, curr2 uint64

		throughput1 := math.Float64frombits(tp1Val.Load())
		throughput2 := math.Float64frombits(tp2Val.Load())
		// Update throughput sample every sRTT
		now := time.Now()
		elapsed := now.Sub(tpTime).Seconds()
		if elapsed > 0 {
			currBytes1 := stats1.GetTotalBytes()
			currBytes2 := stats2.GetTotalBytes()
			tp1Val.Store(math.Float64bits(float64(currBytes1-tpBytes1) / elapsed / 1024 / 1024))
			tp2Val.Store(math.Float64bits(float64(currBytes2-tpBytes2) / elapsed / 1024 / 1024))
			tpBytes1 = currBytes1
			tpBytes2 = currBytes2
			tpTime = now
		}
		totalThroughput := throughput1 + throughput2
		totalBlockSize := uint64(BLOCK_SIZE_MULTIPLIER * MTU)

		if SPLIT_TYPE == "WRR" {
			curr1 = totalBlockSize / 2
			curr2 = totalBlockSize / 2
		} else if totalThroughput == 0 {
			curr1 = totalBlockSize / 2
			curr2 = totalBlockSize / 2
		} else {
			//w1 := 1.0 / max(throughput1, 0.01)
			//w2 := 1.0 / max(throughput2, 0.01)
			//total := w1 + w2

			//w1 := min(max(throughput1/totalThroughput, 0.1), 0.9)
			//w2 := min(max(throughput2 / totalThroughput, 0.01), 0.99)
			w2 := min(max(throughput2/totalThroughput, 0.1), 0.9)

			curr1 = uint64(w2 * float64(totalBlockSize))
			curr2 = totalBlockSize - curr1
		}

		fileOffset1 := conn1.CurrentOffset.Load()
		fileOffset2 := conn2.CurrentOffset.Load()

		splitDataLogger.Log(splitData1, ststats.SplitDataFrameEntry{
			Timestamp:        time.Now(),
			Direction:        "sent",
			FileOffset:       max(fileOffset1, fileOffset2),
			BlockOffset:      0,
			BlockSize:        totalBlockSize,
			ServerBlockSize:  curr1,
			ServerFileOffset: fileOffset1,
		})
		splitDataLogger.Log(splitData2, ststats.SplitDataFrameEntry{
			Timestamp:        time.Now(),
			Direction:        "sent",
			FileOffset:       max(fileOffset1, fileOffset2),
			BlockOffset:      curr1,
			BlockSize:        totalBlockSize,
			ServerBlockSize:  curr2,
			ServerFileOffset: fileOffset2,
		})

		fmt.Println("SPLIT OFFSET: ", max(fileOffset1, fileOffset2))
		go conn1.Conn.SendSplitDataFrame(max(fileOffset1, fileOffset2), 0, totalBlockSize, curr1)
		go conn2.Conn.SendSplitDataFrame(max(fileOffset1, fileOffset2), curr1, totalBlockSize, curr2)

		currentProgress += totalBlockSize

		time.Sleep(maxSRTT)
	}
	gapFillCancel()
	<-gapFillDone
	wg.Wait()

	gapFillDiagPath := fmt.Sprintf("%s/gapfill_client_diag.txt", runDir)
	gapFillDiagContent := fmt.Sprintf(
		"gapFillSent=%d\nsentGapFillsMapSize=%d\n",
		gapFillSent, len(sentGapFills),
	)
	if err := os.WriteFile(gapFillDiagPath, []byte(gapFillDiagContent), 0644); err != nil {
		log.Printf("Failed to write client gap-fill diagnostics: %v", err)
	}

	outputFile := fmt.Sprintf("%s/movie_received.mp4", runDir)
	if err := receivedBuf.Save(outputFile); err != nil {
		log.Printf("Error saving received file: %v", err)
	} else {
		fmt.Printf("KLIENT: plik zapisany w %s\n", outputFile)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-done:
		log.Println("Client goroutine finished, exiting")
	case <-sig:
		log.Println("Interrupt received, exiting")
	}
}
