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
	"main/quic/clientstats"
	"main/quic/loggers"
	"main/quic/ranges"
	ststats "main/quic/stats"
	"main/quic/utils"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Data struct {
	ConnID   string
	StreamID int64
	Payload  []byte
}

func handleClientConn(ctx context.Context, conn *quic.Conn, connID string, out chan<- Data, ranges *ranges.ReceivedRanges, stats *clientstats.Stats, finished *atomic.Bool, currentOffset *atomic.Uint64, rangeLogger *ststats.ReceivedRangeLogger, rangeFile string, gapSignal chan<- struct{}, buf *ranges.ReceivedBuffer) {
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
				if finished.Load() {
					return
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

				rangeStart := int64(fileOffset)
				rangeEnd := int64(fileOffset) + int64(dataLength)
				ranges.AddRange(rangeStart, rangeEnd)

				if buf != nil {
					buf.Write(rangeStart, dataBuf)
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
				loggers.Packet().Log(loggers.PacketEntry{
					Timestamp:  time.Now(),
					ConnID:     connID,
					Offset:     fileOffset,
					DataSize:   len(dataBuf),
					Throughput: currentThroughput,
				})

				// Zapisz statystyki
				stats.RecordRead(len(dataBuf), readLatency)

				// Log opóźnienia (async, non-blocking)
				loggers.Latency().Log(loggers.LatencyEntry{
					Timestamp:  time.Now(),
					ConnID:     connID,
					Offset:     fileOffset,
					DataSize:   len(dataBuf),
					Latency:    readLatency,
					Throughput: currentThroughput,
				})

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

func runConnection(addr string, connID string, wg *sync.WaitGroup, out chan<- ConnResult, ranges *ranges.ReceivedRanges, stats *clientstats.Stats, finished *atomic.Bool, rangeLogger *ststats.ReceivedRangeLogger, rangeFile string, gapSignal chan<- struct{}, buf *ranges.ReceivedBuffer) {
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

type transferState struct {
	conn1, conn2 ConnResult

	ranges1, ranges2 *ranges.ReceivedRanges
	stats1, stats2   *clientstats.Stats

	finished      *atomic.Bool
	conn1Finished *atomic.Bool
	conn2Finished *atomic.Bool

	tp1Val, tp2Val     atomic.Uint64
	tpBytes1, tpBytes2 int64
	tpTime             time.Time

	prevServerOffset1, prevServerOffset2 uint64
	conn1Stagnant, conn2Stagnant         int

	loggedGaps   map[int64]bool
	sentGapFills map[int64]bool
	gapFillSent  int

	currentProgress uint64
	transferStart   time.Time
	tailFrameSent   bool
	lastSentBlock   uint64
}

func newTransferState() *transferState {
	return &transferState{
		ranges1:       ranges.NewReceivedRanges("conn1"),
		ranges2:       ranges.NewReceivedRanges("conn2"),
		stats1:        clientstats.NewStats(),
		stats2:        clientstats.NewStats(),
		finished:      &atomic.Bool{},
		conn1Finished: &atomic.Bool{},
		conn2Finished: &atomic.Bool{},
		loggedGaps:    make(map[int64]bool),
		sentGapFills:  make(map[int64]bool),
	}
}

type loggerSet struct {
	splitDataLogger  *ststats.Logger
	splitData1       string
	splitData2       string
	rangeLogger      *ststats.ReceivedRangeLogger
	gapLogger        *ststats.GapLogger
	gapLogFile       string
	gapFillLogger    *ststats.GapFillLogger
	gapFillLogFile   string
	gapDetailLogger  *ststats.GapDetailLogger
	gapDetailLogFile string
}

func setupLoggers(runDir string) *loggerSet {
	os.MkdirAll(fmt.Sprintf("%s/stats_rtt", runDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/stats_throughput", runDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/stats_inflight", runDir), 0755)

	loggers.Packet().Start(fmt.Sprintf("%s/packet_log2.csv", runDir))
	loggers.Latency().Start(fmt.Sprintf("%s/latency.csv", runDir))
	loggers.RTT().Start(fmt.Sprintf("%s/stats_rtt/rtt.csv", runDir))
	loggers.Throughput().Start(fmt.Sprintf("%s/stats_throughput/throughput.csv", runDir))
	loggers.InFlight().Start(fmt.Sprintf("%s/stats_inflight/inflight.csv", runDir))

	splitDataLogger := ststats.GetInstance()
	splitData1 := fmt.Sprintf("%s/split_data/splitdata_conn1.csv", runDir)
	splitData2 := fmt.Sprintf("%s/split_data/splitdata_conn2.csv", runDir)
	splitDataLogger.Start(splitData1)
	splitDataLogger.Start(splitData2)

	receivedLogger := ststats.GetReceivedPacketLogger()
	receivedLogger.Start("stats/received_packets/received_packets.csv")

	rangeLogger := ststats.GetReceivedRangeLogger()
	rangeLogger.Start("stats/received_ranges/received_ranges_conn1.csv")
	rangeLogger.Start("stats/received_ranges/received_ranges_conn2.csv")

	gapLogger := ststats.GetGapLogger()
	gapLogFile := fmt.Sprintf("%s/gaps.csv", runDir)
	gapLogger.Start(gapLogFile)

	gapFillLogger := ststats.GetGapFillLogger()
	gapFillLogFile := fmt.Sprintf("%s/gap_fill_sent.csv", runDir)
	gapFillLogger.Start(gapFillLogFile)

	gapDetailLogger := ststats.GetGapDetailLogger()
	gapDetailLogFile := fmt.Sprintf("%s/gap_details.csv", runDir)
	gapDetailLogger.Start(gapDetailLogFile)

	return &loggerSet{
		splitDataLogger:  splitDataLogger,
		splitData1:       splitData1,
		splitData2:       splitData2,
		rangeLogger:      rangeLogger,
		gapLogger:        gapLogger,
		gapLogFile:       gapLogFile,
		gapFillLogger:    gapFillLogger,
		gapFillLogFile:   gapFillLogFile,
		gapDetailLogger:  gapDetailLogger,
		gapDetailLogFile: gapDetailLogFile,
	}
}

func (ls *loggerSet) StopStstats() {
	ls.splitDataLogger.Stop(ls.splitData1)
	ls.splitDataLogger.Stop(ls.splitData2)
	ststats.GetReceivedPacketLogger().Stop("stats/received_packets/received_packets.csv")
	ls.rangeLogger.Stop("stats/received_ranges/received_ranges_conn1.csv")
	ls.rangeLogger.Stop("stats/received_ranges/received_ranges_conn2.csv")
	ls.gapLogger.Stop(ls.gapLogFile)
	ls.gapFillLogger.Stop(ls.gapFillLogFile)
	ls.gapDetailLogger.Stop(ls.gapDetailLogFile)
}

func runGapFiller(st *transferState, gapSignal chan struct{}, gapFillLogger *ststats.GapFillLogger, gapFillLogFile string) (chan struct{}, context.CancelFunc) {
	gapFillDone := make(chan struct{})
	if SPLIT_TYPE == "WRR" {
		close(gapFillDone)
		return gapFillDone, func() {}
	}

	gapFillCtx, gapFillCancel := context.WithCancel(context.Background())
	gapTick := time.NewTicker(1 * time.Millisecond)

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
			fileSize := utils.GetFileSize(FILE_NAME)
			offset1 := st.ranges1.GetCurrentOffset()
			offset2 := st.ranges2.GetCurrentOffset()
			_, gapIntervals := ranges.GetCombinedGaps(st.ranges1, st.ranges2, int64(fileSize))
			if len(gapIntervals) == 0 {
				continue
			}

			tp1 := math.Float64frombits(st.tp1Val.Load())
			tp2 := math.Float64frombits(st.tp2Val.Load())
			fasterName := "conn1"
			fasterConn := st.conn1.Conn
			if tp2 > tp1 {
				fasterName = "conn2"
				fasterConn = st.conn2.Conn
			}
			// If faster connection finished, use the other one
			if fasterName == "conn1" && st.conn1Finished.Load() {
				fasterName = "conn2"
				fasterConn = st.conn2.Conn
			} else if fasterName == "conn2" && st.conn2Finished.Load() {
				fasterName = "conn1"
				fasterConn = st.conn1.Conn
			}

			limitOffset := min(offset1, offset2)

			for _, g := range gapIntervals {
				if g.End > limitOffset {
					continue
				}
				if st.sentGapFills[g.Start] {
					continue
				}
				gapSize := uint64(g.End - g.Start)
				if gapSize == 0 {
					continue
				}
				st.sentGapFills[g.Start] = true
				st.gapFillSent++
				gapFillLogger.Log(gapFillLogFile, ststats.GapFillEntry{
					Timestamp: time.Now(),
					ConnID:    fasterName,
					Seq:       st.gapFillSent,
					Offset:    g.Start,
					Size:      int64(gapSize),
				})
				fasterConn.SendGapFillFrame(uint64(g.Start), gapSize)
			}
		}
	}()
	return gapFillDone, gapFillCancel
}

func sendSplitLoop(st *transferState, ls *loggerSet) {
	const stagnantThreshold = 2
	const tpZeroLimit = 5
	lastSampleLog := time.Time{}
	var tpPrevBytes1, tpPrevBytes2 int64
	var tpPrevTime time.Time
	var tpZeroCount1, tpZeroCount2 int
	var tpDone1, tpDone2 bool
	for {
		if st.finished.Load() {
			fmt.Println("KLIENT: plik w całości odebrany, zatrzymuję wysyłanie SplitDataFrame")
			break
		}

		fileSize := utils.GetFileSize(FILE_NAME)
		offset1 := st.ranges1.GetCurrentOffset()
		offset2 := st.ranges2.GetCurrentOffset()
		_, gapIntervals := ranges.GetCombinedGaps(st.ranges1, st.ranges2, int64(fileSize))
		maxRecv := max(offset1, offset2)
		internalGaps := make([]ranges.Range, 0, len(gapIntervals))
		for _, g := range gapIntervals {
			if g.Start >= maxRecv {
				continue
			}
			internalGaps = append(internalGaps, g)
		}
		gapIntervals = internalGaps
		combinedGaps := len(gapIntervals)

		if combinedGaps == 1 {
			fmt.Printf("DEBUG: fileSize=%d maxOffset=%d combinedGaps=%d gaps:\n", fileSize, max(offset1, offset2), combinedGaps)
			for _, g := range gapIntervals {
				fmt.Printf("  [%d, %d) size=%d\n", g.Start, g.End, g.End-g.Start)
			}
		}

		if max(offset1, offset2) >= int64(fileSize) && combinedGaps == 0 {
			elapsed := time.Since(st.transferStart)
			fmt.Printf("KLIENT: cały plik odebrany! czas=%v conn1(offset=%d, gaps=%d), conn2(offset=%d, gaps=%d), combinedGaps=%d, fileSize=%d\n",
				elapsed, offset1, st.ranges1.CountGaps(), offset2, st.ranges2.CountGaps(), combinedGaps, fileSize)
			st.finished.Store(true)
			break
		}

		elapsed := time.Since(st.transferStart)
		received := max(offset1, offset2)
		pct := float64(received) / float64(fileSize) * 100
		totalBytes := st.stats1.GetTotalBytes() + st.stats2.GetTotalBytes()
		throughput := float64(totalBytes) / elapsed.Seconds() / 1024 / 1024
		fmt.Printf("\rKLIENT: %5.1f%% (%d/%d bytes) | conn1=%d conn2=%d | gaps=%d | %.2f MB/s | %v     ",
			pct, received, fileSize, offset1, offset2, combinedGaps, throughput, elapsed)

		st.stats1.StartWindow()
		st.stats2.StartWindow()
		rtt1 := st.conn1.Conn.ConnectionStats().SmoothedRTT
		rtt2 := st.conn2.Conn.ConnectionStats().SmoothedRTT

		// Log RTT + throughput (async, non-blocking) co 100 ms, tylko dla połączeń, które jeszcze wysyłają
		sampleNow := time.Now()
		if sampleNow.Sub(lastSampleLog) >= 100*time.Millisecond {
			lastSampleLog = sampleNow
			conn1Active := !st.conn1Finished.Load()
			conn2Active := !st.conn2Finished.Load()

			if conn1Active {
				loggers.RTT().Log(loggers.RTTEntry{Timestamp: sampleNow, ConnID: st.conn1.ID, RTT: rtt1})
			}
			if conn2Active {
				loggers.RTT().Log(loggers.RTTEntry{Timestamp: sampleNow, ConnID: st.conn2.ID, RTT: rtt2})
			}

			if conn1Active {
				loggers.InFlight().Log(loggers.InFlightEntry{Timestamp: sampleNow, ConnID: st.conn1.ID, BytesInFlight: st.conn1.Conn.BytesInFlight()})
			}
			if conn2Active {
				loggers.InFlight().Log(loggers.InFlightEntry{Timestamp: sampleNow, ConnID: st.conn2.ID, BytesInFlight: st.conn2.Conn.BytesInFlight()})
			}

			if !tpPrevTime.IsZero() {
				tpElapsed := sampleNow.Sub(tpPrevTime).Seconds()
				if tpElapsed > 0 {
					tpBytes1 := st.stats1.GetTotalBytes()
					tpBytes2 := st.stats2.GetTotalBytes()
					if conn1Active && !tpDone1 {
						delta1 := tpBytes1 - tpPrevBytes1
						if delta1 <= 0 {
							tpZeroCount1++
							if tpZeroCount1 >= tpZeroLimit {
								tpDone1 = true
							}
						} else {
							tpZeroCount1 = 0
						}
						if !tpDone1 {
							loggers.Throughput().Log(loggers.ThroughputEntry{
								Timestamp:  sampleNow,
								ConnID:     st.conn1.ID,
								Throughput: float64(delta1) / tpElapsed / 1024 / 1024,
								TotalBytes: tpBytes1,
							})
						}
					}
					if conn2Active && !tpDone2 {
						delta2 := tpBytes2 - tpPrevBytes2
						if delta2 <= 0 {
							tpZeroCount2++
							if tpZeroCount2 >= tpZeroLimit {
								tpDone2 = true
							}
						} else {
							tpZeroCount2 = 0
						}
						if !tpDone2 {
							loggers.Throughput().Log(loggers.ThroughputEntry{
								Timestamp:  sampleNow,
								ConnID:     st.conn2.ID,
								Throughput: float64(delta2) / tpElapsed / 1024 / 1024,
								TotalBytes: tpBytes2,
							})
						}
					}
				}
			}
			tpPrevBytes1 = st.stats1.GetTotalBytes()
			tpPrevBytes2 = st.stats2.GetTotalBytes()
			tpPrevTime = sampleNow
		}

		maxSRTT := func() time.Duration {
			if rtt1 > rtt2 {
				return rtt1
			}
			return rtt2
		}()

		minSRTT := min(rtt1, rtt2)

		offset1 = st.ranges1.GetCurrentOffset()
		offset2 = st.ranges2.GetCurrentOffset()

		serverOffset1 := st.conn1.CurrentOffset.Load()
		serverOffset2 := st.conn2.CurrentOffset.Load()
		if serverOffset1 > st.prevServerOffset1 {
			st.conn1Stagnant = 0
		} else if serverOffset1 > 0 {
			st.conn1Stagnant++
		}
		if serverOffset2 > st.prevServerOffset2 {
			st.conn2Stagnant = 0
		} else if serverOffset2 > 0 {
			st.conn2Stagnant++
		}
		st.prevServerOffset1 = serverOffset1
		st.prevServerOffset2 = serverOffset2
		if st.conn1Stagnant >= stagnantThreshold && !st.conn1Finished.Load() {
			st.conn1Finished.Store(true)
		}
		if st.conn2Stagnant >= stagnantThreshold && !st.conn2Finished.Load() {
			st.conn2Finished.Store(true)
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

		ls.gapLogger.Log(ls.gapLogFile, ststats.GapEntry{
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
			if st.loggedGaps[g.Start] {
				continue
			}
			st.loggedGaps[g.Start] = true
			ls.gapDetailLogger.Log(ls.gapDetailLogFile, ststats.GapDetailEntry{
				Timestamp: time.Now(),
				Offset:    maxOffset,
				Start:     g.Start,
				End:       g.End,
				Size:      g.End - g.Start,
			})
		}

		var curr1, curr2 uint64

		throughput1 := math.Float64frombits(st.tp1Val.Load())
		throughput2 := math.Float64frombits(st.tp2Val.Load())
		// Update throughput sample every sRTT
		now := time.Now()
		elapsedSec := now.Sub(st.tpTime).Seconds()
		if elapsedSec > 0 {
			currBytes1 := st.stats1.GetTotalBytes()
			currBytes2 := st.stats2.GetTotalBytes()
			st.tp1Val.Store(math.Float64bits(float64(currBytes1-st.tpBytes1) / elapsedSec / 1024 / 1024))
			st.tp2Val.Store(math.Float64bits(float64(currBytes2-st.tpBytes2) / elapsedSec / 1024 / 1024))
			st.tpBytes1 = currBytes1
			st.tpBytes2 = currBytes2
			st.tpTime = now
		}
		totalThroughput := throughput1 + throughput2

		totalRTT := rtt1 + rtt2
		totalBlockSize := uint64(BLOCK_SIZE_MULTIPLIER * MTU)

		if SPLIT_TYPE == "WRR" {
			curr1 = totalBlockSize / 2
			curr2 = totalBlockSize / 2
		} else if totalThroughput == 0 {
			curr1 = totalBlockSize / 2
			curr2 = totalBlockSize / 2
		} else {
			//w2 := min(max(throughput2/totalThroughput, 0.1), 0.9)
			w2 := min(max(float64(rtt2)/float64(totalRTT), 0.1), 0.9)

			curr1 = uint64(w2 * float64(totalBlockSize))
			curr2 = totalBlockSize - curr1
		}

		fileOffset1 := st.conn1.CurrentOffset.Load()
		fileOffset2 := st.conn2.CurrentOffset.Load()

		sendSplitFrame := !(st.conn1Finished.Load() || st.conn2Finished.Load())
		if !sendSplitFrame && !st.finished.Load() {
			fmt.Println("KLIENT: jedno z połączeń zakończone, przestaję wysyłać SplitDataFrame")
		}

		//fileoff := uint64(maxOffset)/totalBlockSize*totalBlockSize + 57*totalBlockSize
		//fileoff := uint64(maxOffset)/totalBlockSize*totalBlockSize + 75*totalBlockSize
		fileoff := uint64(maxOffset)/totalBlockSize*totalBlockSize + 50*totalBlockSize
		curBlock := fileoff / totalBlockSize
		if sendSplitFrame && curBlock != st.lastSentBlock {
			st.lastSentBlock = curBlock
			ls.splitDataLogger.Log(ls.splitData1, ststats.SplitDataFrameEntry{
				Timestamp:        time.Now(),
				Direction:        "sent",
				FileOffset:       fileoff,
				BlockOffset:      0,
				BlockSize:        totalBlockSize,
				ServerBlockSize:  curr1,
				ServerFileOffset: fileOffset1,
			})
			ls.splitDataLogger.Log(ls.splitData2, ststats.SplitDataFrameEntry{
				Timestamp:        time.Now(),
				Direction:        "sent",
				FileOffset:       fileoff,
				BlockOffset:      curr1,
				BlockSize:        totalBlockSize,
				ServerBlockSize:  curr2,
				ServerFileOffset: fileOffset2,
			})

			go st.conn1.Conn.SendSplitDataFrame(fileoff, 0, totalBlockSize, curr1)
			go st.conn2.Conn.SendSplitDataFrame(fileoff, curr1, totalBlockSize, curr2)
		}

		tailStart := uint64(fileSize) / totalBlockSize * totalBlockSize
		if !st.tailFrameSent && maxOffset >= int64(tailStart) && maxOffset < int64(fileSize) && combinedGaps == 0 {
			ls.splitDataLogger.Log(ls.splitData1, ststats.SplitDataFrameEntry{
				Timestamp:        time.Now(),
				Direction:        "sent",
				FileOffset:       tailStart,
				BlockOffset:      0,
				BlockSize:        totalBlockSize,
				ServerBlockSize:  curr1,
				ServerFileOffset: fileOffset1,
			})
			ls.splitDataLogger.Log(ls.splitData2, ststats.SplitDataFrameEntry{
				Timestamp:        time.Now(),
				Direction:        "sent",
				FileOffset:       tailStart,
				BlockOffset:      curr1,
				BlockSize:        totalBlockSize,
				ServerBlockSize:  curr2,
				ServerFileOffset: fileOffset2,
			})
			go st.conn1.Conn.SendSplitDataFrame(tailStart, 0, totalBlockSize, curr1)
			go st.conn2.Conn.SendSplitDataFrame(tailStart, curr1, totalBlockSize, curr2)
			st.tailFrameSent = true
		}

		st.currentProgress += totalBlockSize

		fmt.Println(minSRTT)
		fmt.Println(maxSRTT)
		time.Sleep(maxSRTT)
	}
}

func finalize(st *transferState, ls *loggerSet, gapFillDone chan struct{}, gapFillCancel context.CancelFunc, wg *sync.WaitGroup, receivedBuf *ranges.ReceivedBuffer, runDir string) {
	gapFillCancel()
	<-gapFillDone
	wg.Wait()

	finalTotal1 := st.stats1.GetTotalBytes()
	finalTotal2 := st.stats2.GetTotalBytes()
	finalTotalPath := fmt.Sprintf("%s/stats_throughput/final_totals.txt", runDir)
	finalTotalContent := fmt.Sprintf(
		"conn1_total_bytes=%d\nconn2_total_bytes=%d\ncombined_total_bytes=%d\n",
		finalTotal1, finalTotal2, finalTotal1+finalTotal2,
	)
	if err := os.WriteFile(finalTotalPath, []byte(finalTotalContent), 0644); err != nil {
		log.Printf("Failed to write final totals: %v", err)
	}

	// Flush loggers before saving results
	loggers.Packet().Stop()
	loggers.Latency().Stop()
	loggers.RTT().Stop()
	loggers.Throughput().Stop()
	loggers.InFlight().Stop()

	gapFillDiagPath := fmt.Sprintf("%s/gapfill_client_diag.txt", runDir)
	gapFillDiagContent := fmt.Sprintf(
		"gapFillSent=%d\nsentGapFillsMapSize=%d\n",
		st.gapFillSent, len(st.sentGapFills),
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
}

func main() {
	// Create run directory with parameters
	runDir := fmt.Sprintf("runs/run_%s", time.Now().Format("20060102_150405"))
	os.MkdirAll(runDir, 0755)
	paramsFile := fmt.Sprintf("%s/params.txt", runDir)
	paramsContent := fmt.Sprintf("MTU: %d\nSPLIT_TYPE: %s\nSCOPE: %s\nBLOCK_SIZE_MULTIPLIER: %d\nFILE: %s\n", MTU, SPLIT_TYPE, SCOPE, BLOCK_SIZE_MULTIPLIER, FILE_NAME)
	os.WriteFile(paramsFile, []byte(paramsContent), 0644)

	ls := setupLoggers(runDir)
	defer ls.StopStstats()

	st := newTransferState()

	var wg sync.WaitGroup
	wg.Add(2)
	connCh := make(chan ConnResult, 2)
	gapSignal := make(chan struct{}, 1)
	receivedBuf := ranges.NewReceivedBuffer(int64(utils.GetFileSize(FILE_NAME)))

	if SCOPE == "LOCAL" {
		go runConnection(LOCAL_IP_ADDRESS, "conn1", &wg, connCh, st.ranges1, st.stats1, st.finished, ls.rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn1.csv", runDir), gapSignal, receivedBuf)
		go runConnection(LOCAL_2_IP_ADDRESS, "conn2", &wg, connCh, st.ranges2, st.stats2, st.finished, ls.rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn2.csv", runDir), gapSignal, receivedBuf)
	} else {
		go runConnection(AZURE_IP_PUBLIC_ADDRESS, "conn1", &wg, connCh, st.ranges1, st.stats1, st.finished, ls.rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn1.csv", runDir), gapSignal, receivedBuf)
		go runConnection(TUL_IP_PUBLIC_ADDRESS, "conn2", &wg, connCh, st.ranges2, st.stats2, st.finished, ls.rangeLogger, fmt.Sprintf("%s/received_ranges/received_ranges_conn2.csv", runDir), gapSignal, receivedBuf)
	}

	resA, resB := <-connCh, <-connCh
	if resA.Err != nil || resB.Err != nil {
		log.Fatalf("Error during connection: %v / %v", resA.Err, resB.Err)
	}
	if resA.ID == "conn1" {
		st.conn1, st.conn2 = resA, resB
	} else {
		st.conn1, st.conn2 = resB, resA
	}
	st.transferStart = time.Now()
	st.tpTime = time.Now()

	gapFillDone, gapFillCancel := runGapFiller(st, gapSignal, ls.gapFillLogger, ls.gapFillLogFile)

	sendSplitLoop(st, ls)
	finalize(st, ls, gapFillDone, gapFillCancel, &wg, receivedBuf, runDir)
}
