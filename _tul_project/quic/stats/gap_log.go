package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type GapEntry struct {
	Timestamp     time.Time
	CurrentOffset int64
	Gaps          int
}

type gapFileHandle struct {
	ch   chan GapEntry
	done chan struct{}
}

type GapLogger struct {
	mu    sync.Mutex
	files map[string]*gapFileHandle
}

var (
	gapInstance *GapLogger
	gapOnce     sync.Once
)

func GetGapLogger() *GapLogger {
	gapOnce.Do(func() {
		gapInstance = &GapLogger{
			files: make(map[string]*gapFileHandle),
		}
	})
	return gapInstance
}

func (l *GapLogger) Start(filename string) error {
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
	writer.Write([]string{"timestamp", "current_offset", "gaps"})
	writer.Flush()
	if writer.Error() != nil {
		f.Close()
		return writer.Error()
	}

	ch := make(chan GapEntry, 8192)
	done := make(chan struct{})

	go func() {
		fmt.Printf("[GapLogger] goroutine started for %s\n", filename)
		for entry := range ch {
			ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
			record := []string{
				ts,
				fmt.Sprintf("%d", entry.CurrentOffset),
				fmt.Sprintf("%d", entry.Gaps),
			}
			if err := writer.Write(record); err != nil {
				fmt.Printf("[GapLogger] write error: %v\n", err)
				return
			}
			writer.Flush()
			if writer.Error() != nil {
				fmt.Printf("[GapLogger] flush error: %v\n", writer.Error())
				return
			}
		}
		writer.Flush()
		f.Close()
		fmt.Printf("[GapLogger] goroutine done for %s\n", filename)
		close(done)
	}()

	l.files[filename] = &gapFileHandle{ch: ch, done: done}
	return nil
}

func (l *GapLogger) Log(filename string, entry GapEntry) {
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

func (l *GapLogger) Stop(filename string) {
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
