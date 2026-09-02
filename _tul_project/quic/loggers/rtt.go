package loggers

import (
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

type RTTEntry struct {
	Timestamp time.Time
	ConnID    string
	RTT       time.Duration
}

type RTTLogger struct {
	ch   chan RTTEntry
	done chan struct{}
	once sync.Once
}

var rttLogger = &RTTLogger{}

func RTT() *RTTLogger { return rttLogger }

func (l *RTTLogger) Start(filename string) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create RTT log file: %v", err)
	}
	f.WriteString("timestamp,conn_id,rtt_ns,rtt_ms\n")

	l.ch = make(chan RTTEntry, 1000)
	l.done = make(chan struct{})

	var mu sync.Mutex
	var rtts []float64
	connRTTs := make(map[string][]float64)
	var startTime, endTime time.Time

	go func() {
		for entry := range l.ch {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s,%s,%d,%.6f\n",
				ts, entry.ConnID, entry.RTT.Nanoseconds(), entry.RTT.Seconds()*1000)
			if _, err := f.WriteString(line); err != nil {
				log.Printf("RTT log write error: %v", err)
			}

			mu.Lock()
			if startTime.IsZero() {
				startTime = entry.Timestamp
			}
			endTime = entry.Timestamp
			rtts = append(rtts, entry.RTT.Seconds()*1000)
			connRTTs[entry.ConnID] = append(connRTTs[entry.ConnID], entry.RTT.Seconds()*1000)
			mu.Unlock()
		}
		f.Close()

		mu.Lock()
		fmt.Printf("RTT LOGGER: writing summary, %d entries\n", len(rtts))
		combinedRTTs := make([]float64, len(rtts))
		copy(combinedRTTs, rtts)
		cRTTs := make(map[string][]float64)
		for k, v := range connRTTs {
			cRTTs[k] = make([]float64, len(v))
			copy(cRTTs[k], v)
		}
		sTime := startTime
		eTime := endTime
		mu.Unlock()

		// Combined summary
		summaryPath := strings.TrimSuffix(filename, ".csv") + "_summary.txt"
		summary, err := os.Create(summaryPath)
		if err != nil {
			log.Printf("Cannot create RTT summary file: %v", err)
			close(l.done)
			return
		}
		writeRTTStatsTo(summary, "Combined (all connections)", combinedRTTs)
		if !sTime.IsZero() {
			fmt.Fprintf(summary, "\n=== Transfer Duration ===\n")
			fmt.Fprintf(summary, "Total: %v\n", eTime.Sub(sTime))
		}
		summary.Close()

		// Per-connection summaries
		for connID := range cRTTs {
			connSummaryPath := strings.TrimSuffix(filename, ".csv") + "_summary_" + connID + ".txt"
			connSummary, err := os.Create(connSummaryPath)
			if err != nil {
				log.Printf("Cannot create per-conn RTT summary file: %v", err)
				continue
			}
			writeRTTStatsTo(connSummary, connID, cRTTs[connID])
			connSummary.Close()
		}
		fmt.Println("RTT LOGGER: all summary files written")
		close(l.done)
	}()
}

func (l *RTTLogger) Log(e RTTEntry) {
	select {
	case l.ch <- e:
	default:
	}
}

func (l *RTTLogger) Stop() {
	l.once.Do(func() {
		close(l.ch)
		<-l.done
	})
}

func writeRTTStatsTo(f *os.File, label string, rtts []float64) {
	fmt.Fprintf(f, "=== %s ===\n\n", label)

	if len(rtts) > 0 {
		rttMin, rttMax, rttMean, rttVar := computeStats(rtts)
		fmt.Fprintf(f, "--- Smoothed RTT (ms) ---\n")
		fmt.Fprintf(f, "Count: %d\n", len(rtts))
		fmt.Fprintf(f, "Min: %.6f\n", rttMin)
		fmt.Fprintf(f, "Max: %.6f\n", rttMax)
		fmt.Fprintf(f, "Mean: %.6f\n", rttMean)
		fmt.Fprintf(f, "Variance: %.6f\n", rttVar)
		fmt.Fprintf(f, "StdDev: %.6f\n", math.Sqrt(rttVar))
	}
}
