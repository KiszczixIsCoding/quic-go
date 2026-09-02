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

type InFlightEntry struct {
	Timestamp     time.Time
	ConnID        string
	BytesInFlight uint64
}

type InFlightLogger struct {
	ch   chan InFlightEntry
	done chan struct{}
	once sync.Once
}

var inFlightLogger = &InFlightLogger{}

func InFlight() *InFlightLogger { return inFlightLogger }

func (l *InFlightLogger) Start(filename string) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create in-flight log file: %v", err)
	}
	f.WriteString("timestamp,conn_id,bytes_in_flight\n")

	l.ch = make(chan InFlightEntry, 1000)
	l.done = make(chan struct{})

	var mu sync.Mutex
	var vals []float64
	connVals := make(map[string][]float64)
	var startTime, endTime time.Time

	go func() {
		for entry := range l.ch {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s,%s,%d\n",
				ts, entry.ConnID, entry.BytesInFlight)
			if _, err := f.WriteString(line); err != nil {
				log.Printf("In-flight log write error: %v", err)
			}

			mu.Lock()
			if startTime.IsZero() {
				startTime = entry.Timestamp
			}
			endTime = entry.Timestamp
			v := float64(entry.BytesInFlight)
			vals = append(vals, v)
			connVals[entry.ConnID] = append(connVals[entry.ConnID], v)
			mu.Unlock()
		}
		f.Close()

		mu.Lock()
		fmt.Printf("IN-FLIGHT LOGGER: writing summary, %d entries\n", len(vals))
		combinedVals := make([]float64, len(vals))
		copy(combinedVals, vals)
		cVals := make(map[string][]float64)
		for k, v := range connVals {
			cVals[k] = make([]float64, len(v))
			copy(cVals[k], v)
		}
		sTime := startTime
		eTime := endTime
		mu.Unlock()

		// Combined summary
		summaryPath := strings.TrimSuffix(filename, ".csv") + "_summary.txt"
		summary, err := os.Create(summaryPath)
		if err != nil {
			log.Printf("Cannot create in-flight summary file: %v", err)
			close(l.done)
			return
		}
		writeInFlightStatsTo(summary, "Combined (all connections)", combinedVals)
		if !sTime.IsZero() {
			fmt.Fprintf(summary, "\n=== Transfer Duration ===\n")
			fmt.Fprintf(summary, "Total: %v\n", eTime.Sub(sTime))
		}
		summary.Close()

		// Per-connection summaries
		for connID := range cVals {
			connSummaryPath := strings.TrimSuffix(filename, ".csv") + "_summary_" + connID + ".txt"
			connSummary, err := os.Create(connSummaryPath)
			if err != nil {
				log.Printf("Cannot create per-conn in-flight summary file: %v", err)
				continue
			}
			writeInFlightStatsTo(connSummary, connID, cVals[connID])
			connSummary.Close()
		}
		fmt.Println("IN-FLIGHT LOGGER: all summary files written")
		close(l.done)
	}()
}

func (l *InFlightLogger) Log(e InFlightEntry) {
	select {
	case l.ch <- e:
	default:
	}
}

func (l *InFlightLogger) Stop() {
	l.once.Do(func() {
		close(l.ch)
		<-l.done
	})
}

func writeInFlightStatsTo(f *os.File, label string, vals []float64) {
	fmt.Fprintf(f, "=== %s ===\n\n", label)

	if len(vals) > 0 {
		vMin, vMax, vMean, vVar := computeStats(vals)
		fmt.Fprintf(f, "--- Bytes In Flight (bytes) ---\n")
		fmt.Fprintf(f, "Count: %d\n", len(vals))
		fmt.Fprintf(f, "Min: %.0f\n", vMin)
		fmt.Fprintf(f, "Max: %.0f\n", vMax)
		fmt.Fprintf(f, "Mean: %.2f\n", vMean)
		fmt.Fprintf(f, "Variance: %.2f\n", vVar)
		fmt.Fprintf(f, "StdDev: %.2f\n", math.Sqrt(vVar))
	}
}
