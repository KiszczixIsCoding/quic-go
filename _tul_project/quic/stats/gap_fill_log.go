package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type GapFillEntry struct {
	Timestamp time.Time
	ConnID    string
	Offset    int64
	Size      int64
	Seq       int
}

type gapFillFileHandle struct {
	ch   chan GapFillEntry
	done chan struct{}
}

type GapFillLogger struct {
	mu    sync.Mutex
	files map[string]*gapFillFileHandle
}

var (
	gapFillInstance *GapFillLogger
	gapFillOnce     sync.Once
)

func GetGapFillLogger() *GapFillLogger {
	gapFillOnce.Do(func() {
		gapFillInstance = &GapFillLogger{
			files: make(map[string]*gapFillFileHandle),
		}
	})
	return gapFillInstance
}

func (l *GapFillLogger) Start(filename string) error {
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
	writer.Write([]string{"timestamp", "conn_id", "seq", "offset", "size"})
	writer.Flush()
	if writer.Error() != nil {
		f.Close()
		return writer.Error()
	}

	ch := make(chan GapFillEntry, 8192)
	done := make(chan struct{})

	go func() {
		for entry := range ch {
			ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
			record := []string{
				ts,
				entry.ConnID,
				fmt.Sprintf("%d", entry.Seq),
				fmt.Sprintf("%d", entry.Offset),
				fmt.Sprintf("%d", entry.Size),
			}
			if err := writer.Write(record); err != nil {
				fmt.Printf("[GapFillLogger] write error: %v\n", err)
				return
			}
			writer.Flush()
			if writer.Error() != nil {
				fmt.Printf("[GapFillLogger] flush error: %v\n", writer.Error())
				return
			}
		}
		writer.Flush()
		f.Close()
		close(done)
	}()

	l.files[filename] = &gapFillFileHandle{ch: ch, done: done}
	return nil
}

func (l *GapFillLogger) Log(filename string, entry GapFillEntry) {
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

func (l *GapFillLogger) Stop(filename string) {
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
