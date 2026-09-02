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

type LatencyEntry struct {
	Timestamp  time.Time
	ConnID     string
	Offset     uint64
	DataSize   int
	Latency    time.Duration
	Throughput float64
}

type LatencyLogger struct {
	ch   chan LatencyEntry
	done chan struct{}
	once sync.Once
}

var latencyLogger = &LatencyLogger{}

func Latency() *LatencyLogger { return latencyLogger }

func (l *LatencyLogger) Start(filename string) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create latency log file: %v", err)
	}
	f.WriteString("timestamp,conn_id,offset,data_size,latency_ns,latency_ms,throughput_mbps\n")

	l.ch = make(chan LatencyEntry, 1000)
	l.done = make(chan struct{})

	var mu sync.Mutex
	var latencies []float64
	var throughputs []float64
	connLatencies := make(map[string][]float64)
	connThroughputs := make(map[string][]float64)
	var startTime, endTime time.Time

	go func() {
		for entry := range l.ch {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s,%s,%d,%d,%d,%.6f,%.2f\n",
				ts, entry.ConnID, entry.Offset, entry.DataSize, entry.Latency.Nanoseconds(), entry.Latency.Seconds()*1000, entry.Throughput)
			if _, err := f.WriteString(line); err != nil {
				log.Printf("Latency log write error: %v", err)
			}

			mu.Lock()
			if startTime.IsZero() {
				startTime = entry.Timestamp
			}
			endTime = entry.Timestamp
			latencies = append(latencies, entry.Latency.Seconds()*1000)
			throughputs = append(throughputs, entry.Throughput)
			connLatencies[entry.ConnID] = append(connLatencies[entry.ConnID], entry.Latency.Seconds()*1000)
			connThroughputs[entry.ConnID] = append(connThroughputs[entry.ConnID], entry.Throughput)
			mu.Unlock()
		}
		f.Close()

		mu.Lock()
		fmt.Printf("LATENCY LOGGER: writing summary, %d entries\n", len(latencies))
		combinedLatencies := make([]float64, len(latencies))
		copy(combinedLatencies, latencies)
		combinedThroughputs := make([]float64, len(throughputs))
		copy(combinedThroughputs, throughputs)
		cLatencies := make(map[string][]float64)
		for k, v := range connLatencies {
			cLatencies[k] = make([]float64, len(v))
			copy(cLatencies[k], v)
		}
		cThroughputs := make(map[string][]float64)
		for k, v := range connThroughputs {
			cThroughputs[k] = make([]float64, len(v))
			copy(cThroughputs[k], v)
		}
		sTime := startTime
		eTime := endTime
		mu.Unlock()

		// Combined summary
		summaryPath := strings.TrimSuffix(filename, ".csv") + "_summary.txt"
		summary, err := os.Create(summaryPath)
		if err != nil {
			log.Printf("Cannot create summary file: %v", err)
			close(l.done)
			return
		}
		writeStatsTo(summary, "Combined (all connections)", combinedLatencies, combinedThroughputs)
		if !sTime.IsZero() {
			fmt.Fprintf(summary, "\n=== Transfer Duration ===\n")
			fmt.Fprintf(summary, "Total: %v\n", eTime.Sub(sTime))
		}
		summary.Close()

		// Per-connection summaries
		for connID := range cLatencies {
			connSummaryPath := strings.TrimSuffix(filename, ".csv") + "_summary_" + connID + ".txt"
			connSummary, err := os.Create(connSummaryPath)
			if err != nil {
				log.Printf("Cannot create per-conn summary file: %v", err)
				continue
			}
			writeStatsTo(connSummary, connID, cLatencies[connID], cThroughputs[connID])
			connSummary.Close()
		}
		fmt.Println("LATENCY LOGGER: all summary files written")
		close(l.done)
	}()
}

func (l *LatencyLogger) Log(e LatencyEntry) {
	select {
	case l.ch <- e:
	default:
	}
}

func (l *LatencyLogger) Stop() {
	l.once.Do(func() {
		close(l.ch)
		<-l.done
	})
}

func writeStatsTo(f *os.File, label string, latencies, throughputs []float64) {
	fmt.Fprintf(f, "=== %s ===\n\n", label)

	if len(latencies) > 0 {
		latMin, latMax, latMean, latVar := computeStats(latencies)
		fmt.Fprintf(f, "--- Latency (ms) ---\n")
		fmt.Fprintf(f, "Count: %d\n", len(latencies))
		fmt.Fprintf(f, "Min: %.6f\n", latMin)
		fmt.Fprintf(f, "Max: %.6f\n", latMax)
		fmt.Fprintf(f, "Mean: %.6f\n", latMean)
		fmt.Fprintf(f, "Variance: %.6f\n", latVar)
		fmt.Fprintf(f, "StdDev: %.6f\n\n", math.Sqrt(latVar))
	}

	if len(throughputs) > 0 {
		tpMin, tpMax, tpMean, tpVar := computeStats(throughputs)
		fmt.Fprintf(f, "--- Throughput (MB/s) ---\n")
		fmt.Fprintf(f, "Count: %d\n", len(throughputs))
		fmt.Fprintf(f, "Min: %.6f\n", tpMin)
		fmt.Fprintf(f, "Max: %.6f\n", tpMax)
		fmt.Fprintf(f, "Mean: %.6f\n", tpMean)
		fmt.Fprintf(f, "Variance: %.6f\n", tpVar)
		fmt.Fprintf(f, "StdDev: %.6f\n", math.Sqrt(tpVar))
	}
}

func computeStats(values []float64) (min, max, mean, variance float64) {
	if len(values) == 0 {
		return
	}
	min = values[0]
	max = values[0]
	sum := 0.0
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	mean = sum / float64(len(values))
	varSum := 0.0
	for _, v := range values {
		diff := v - mean
		varSum += diff * diff
	}
	variance = varSum / float64(len(values))
	return
}
