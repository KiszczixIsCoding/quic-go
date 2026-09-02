package clientstats

import (
	"sync"
	"time"
)

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

func (st *Stats) GetTotalBytes() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.totalBytesRead
}
