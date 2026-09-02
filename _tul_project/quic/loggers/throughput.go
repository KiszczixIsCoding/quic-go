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

type ThroughputEntry struct {
	Timestamp  time.Time
	ConnID     string
	Throughput float64 // MB/s
	TotalBytes int64
}

type ThroughputLogger struct {
	ch   chan ThroughputEntry
	done chan struct{}
	once sync.Once
}

var throughputLogger = &ThroughputLogger{}

func Throughput() *ThroughputLogger { return throughputLogger }

func (l *ThroughputLogger) Start(filename string) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create throughput log file: %v", err)
	}
	f.WriteString("timestamp,conn_id,throughput_mbs,total_bytes\n")

	l.ch = make(chan ThroughputEntry, 1000)
	l.done = make(chan struct{})

	var mu sync.Mutex
	var tps []float64
	connTPs := make(map[string][]float64)
	var startTime, endTime time.Time

	go func() {
		for entry := range l.ch {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s,%s,%.6f,%d\n",
				ts, entry.ConnID, entry.Throughput, entry.TotalBytes)
			if _, err := f.WriteString(line); err != nil {
				log.Printf("Throughput log write error: %v", err)
			}

			mu.Lock()
			if startTime.IsZero() {
				startTime = entry.Timestamp
			}
			endTime = entry.Timestamp
			tps = append(tps, entry.Throughput)
			connTPs[entry.ConnID] = append(connTPs[entry.ConnID], entry.Throughput)
			mu.Unlock()
		}
		f.Close()

		mu.Lock()
		fmt.Printf("THROUGHPUT LOGGER: writing summary, %d entries\n", len(tps))
		combinedTPs := make([]float64, len(tps))
		copy(combinedTPs, tps)
		cTPs := make(map[string][]float64)
		for k, v := range connTPs {
			cTPs[k] = make([]float64, len(v))
			copy(cTPs[k], v)
		}
		sTime := startTime
		eTime := endTime
		mu.Unlock()

		// Combined summary
		summaryPath := strings.TrimSuffix(filename, ".csv") + "_summary.txt"
		summary, err := os.Create(summaryPath)
		if err != nil {
			log.Printf("Cannot create throughput summary file: %v", err)
			close(l.done)
			return
		}
		writeThroughputStatsTo(summary, "Combined (all connections)", combinedTPs)
		if !sTime.IsZero() {
			fmt.Fprintf(summary, "\n=== Transfer Duration ===\n")
			fmt.Fprintf(summary, "Total: %v\n", eTime.Sub(sTime))
		}
		summary.Close()

		// Per-connection summaries
		for connID := range cTPs {
			connSummaryPath := strings.TrimSuffix(filename, ".csv") + "_summary_" + connID + ".txt"
			connSummary, err := os.Create(connSummaryPath)
			if err != nil {
				log.Printf("Cannot create per-conn throughput summary file: %v", err)
				continue
			}
			writeThroughputStatsTo(connSummary, connID, cTPs[connID])
			connSummary.Close()
		}
		fmt.Println("THROUGHPUT LOGGER: all summary files written")
		close(l.done)
	}()
}

func (l *ThroughputLogger) Log(e ThroughputEntry) {
	select {
	case l.ch <- e:
	default:
	}
}

func (l *ThroughputLogger) Stop() {
	l.once.Do(func() {
		close(l.ch)
		<-l.done
	})
}

func writeThroughputStatsTo(f *os.File, label string, tps []float64) {
	fmt.Fprintf(f, "=== %s ===\n\n", label)

	if len(tps) > 0 {
		tpMin, tpMax, tpMean, tpVar := computeStats(tps)
		fmt.Fprintf(f, "--- Throughput (MB/s) ---\n")
		fmt.Fprintf(f, "Count: %d\n", len(tps))
		fmt.Fprintf(f, "Min: %.6f\n", tpMin)
		fmt.Fprintf(f, "Max: %.6f\n", tpMax)
		fmt.Fprintf(f, "Mean: %.6f\n", tpMean)
		fmt.Fprintf(f, "Variance: %.6f\n", tpVar)
		fmt.Fprintf(f, "StdDev: %.6f\n", math.Sqrt(tpVar))
	}
}
