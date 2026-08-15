package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type GapDetailEntry struct {
	Timestamp time.Time
	Offset    int64
	Start     int64
	End       int64
	Size      int64
}

type gapDetailFileHandle struct {
	ch   chan GapDetailEntry
	done chan struct{}
}

type GapDetailLogger struct {
	mu    sync.Mutex
	files map[string]*gapDetailFileHandle
}

var (
	gapDetailInstance *GapDetailLogger
	gapDetailOnce     sync.Once
)

func GetGapDetailLogger() *GapDetailLogger {
	gapDetailOnce.Do(func() {
		gapDetailInstance = &GapDetailLogger{
			files: make(map[string]*gapDetailFileHandle),
		}
	})
	return gapDetailInstance
}

func (l *GapDetailLogger) Start(filename string) error {
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
	writer.Write([]string{"timestamp", "offset", "gap_start", "gap_end", "gap_size"})
	writer.Flush()
	if writer.Error() != nil {
		f.Close()
		return writer.Error()
	}

	ch := make(chan GapDetailEntry, 8192)
	done := make(chan struct{})

	go func() {
		for entry := range ch {
			ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
			record := []string{
				ts,
				fmt.Sprintf("%d", entry.Offset),
				fmt.Sprintf("%d", entry.Start),
				fmt.Sprintf("%d", entry.End),
				fmt.Sprintf("%d", entry.Size),
			}
			if err := writer.Write(record); err != nil {
				fmt.Printf("[GapDetailLogger] write error: %v\n", err)
				return
			}
			writer.Flush()
			if writer.Error() != nil {
				fmt.Printf("[GapDetailLogger] flush error: %v\n", writer.Error())
				return
			}
		}
		writer.Flush()
		f.Close()
		close(done)
	}()

	l.files[filename] = &gapDetailFileHandle{ch: ch, done: done}
	return nil
}

func (l *GapDetailLogger) Log(filename string, entry GapDetailEntry) {
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

func (l *GapDetailLogger) Stop(filename string) {
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
