package loggers

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type ThroughputSample struct {
	Timestamp       time.Time
	Conn1Throughput float64
	Conn2Throughput float64
	Conn1TotalBytes int64
	Conn2TotalBytes int64
}

type ThroughputLogger struct {
	ch   chan ThroughputSample
	done chan struct{}
	once sync.Once
}

var throughputLogger = &ThroughputLogger{}

func Throughput() *ThroughputLogger { return throughputLogger }

func (l *ThroughputLogger) Start(filename string) {
	l.ch = make(chan ThroughputSample, 1000)
	l.done = make(chan struct{})
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create throughput log file: %v", err)
	}

	f.WriteString("timestamp,conn1_throughput_mbps,conn2_throughput_mbps,conn1_total_bytes,conn2_total_bytes\n")
	f.Sync()

	go func() {
		for entry := range l.ch {
			ts := entry.Timestamp.Format("2006-01-02T15:04:05.000000")
			line := fmt.Sprintf("%s,%.2f,%.2f,%d,%d\n",
				ts, entry.Conn1Throughput, entry.Conn2Throughput, entry.Conn1TotalBytes, entry.Conn2TotalBytes)
			_, _ = f.WriteString(line)
		}
		f.Close()
		close(l.done)
	}()
}

func (l *ThroughputLogger) Log(e ThroughputSample) {
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
