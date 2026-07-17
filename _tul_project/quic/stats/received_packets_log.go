package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ReceivedPacketEntry struct {
	Timestamp  time.Time
	ConnID     string
	DataSize   int
	Offset     uint64
	Throughput float64
}

type receivedFileHandle struct {
	ch   chan ReceivedPacketEntry
	done chan struct{}
}

type ReceivedPacketLogger struct {
	mu    sync.Mutex
	files map[string]*receivedFileHandle
}

var (
	receivedInstance *ReceivedPacketLogger
	receivedOnce     sync.Once
)

func GetReceivedPacketLogger() *ReceivedPacketLogger {
	receivedOnce.Do(func() {
		receivedInstance = &ReceivedPacketLogger{
			files: make(map[string]*receivedFileHandle),
		}
	})
	return receivedInstance
}

func (l *ReceivedPacketLogger) Start(filename string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.files[filename]; exists {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	f, err := os.Create(filename)
	if err != nil {
		return err
	}

	writer := csv.NewWriter(f)
	writer.Write([]string{"timestamp", "conn_id", "data_size", "offset", "throughput"})

	ch := make(chan ReceivedPacketEntry, 8192)
	done := make(chan struct{})

	go func() {
		for entry := range ch {
			ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
			record := []string{
				ts,
				entry.ConnID,
				fmt.Sprintf("%d", entry.DataSize),
				fmt.Sprintf("%d", entry.Offset),
				fmt.Sprintf("%.2f", entry.Throughput),
			}
			if err := writer.Write(record); err != nil {
				return
			}
		}
		writer.Flush()
		f.Close()
		close(done)
	}()

	l.files[filename] = &receivedFileHandle{ch: ch, done: done}
	return nil
}

func (l *ReceivedPacketLogger) Log(filename string, entry ReceivedPacketEntry) {
	l.mu.Lock()
	h, exists := l.files[filename]
	l.mu.Unlock()

	if !exists {
		return
	}

	h.ch <- entry
}

func (l *ReceivedPacketLogger) Stop(filename string) {
	l.mu.Lock()
	h, exists := l.files[filename]
	if exists {
		close(h.ch)
		delete(l.files, filename)
	}
	l.mu.Unlock()

	if exists {
		<-h.done
	}
}
