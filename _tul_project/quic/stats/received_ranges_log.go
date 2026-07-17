package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ReceivedRangeEntry struct {
	Timestamp time.Time
	ConnID    string
	Start     int64
	End       int64
}

type rangeFileHandle struct {
	ch   chan ReceivedRangeEntry
	done chan struct{}
}

type ReceivedRangeLogger struct {
	mu    sync.Mutex
	files map[string]*rangeFileHandle
}

var (
	rangeInstance *ReceivedRangeLogger
	rangeOnce     sync.Once
)

func GetReceivedRangeLogger() *ReceivedRangeLogger {
	rangeOnce.Do(func() {
		rangeInstance = &ReceivedRangeLogger{
			files: make(map[string]*rangeFileHandle),
		}
	})
	return rangeInstance
}

func (l *ReceivedRangeLogger) Start(filename string) error {
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
	writer.Write([]string{"timestamp", "conn_id", "start", "end"})

	ch := make(chan ReceivedRangeEntry, 8192)
	done := make(chan struct{})

	go func() {
		for entry := range ch {
			ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
			record := []string{
				ts,
				entry.ConnID,
				fmt.Sprintf("%d", entry.Start),
				fmt.Sprintf("%d", entry.End),
			}
			if err := writer.Write(record); err != nil {
				return
			}
		}
		writer.Flush()
		f.Close()
		close(done)
	}()

	l.files[filename] = &rangeFileHandle{ch: ch, done: done}
	return nil
}

func (l *ReceivedRangeLogger) Log(filename string, entry ReceivedRangeEntry) {
	l.mu.Lock()
	h, exists := l.files[filename]
	l.mu.Unlock()

	if !exists {
		return
	}

	select {
	case h.ch <- entry:
	default:
	}
}

func (l *ReceivedRangeLogger) Stop(filename string) {
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
