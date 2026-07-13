package stats

import (
	"encoding/csv"
	"fmt"
	"os"
	"sync"
	"time"
)

type SplitDataFrameEntry struct {
	Timestamp        time.Time
	Direction        string
	FileOffset       uint64
	BlockOffset      uint64
	BlockSize        uint64
	ServerBlockSize  uint64
	ServerFileOffset uint64
}

type fileHandle struct {
	ch   chan SplitDataFrameEntry
	done chan struct{}
}

type Logger struct {
	mu    sync.Mutex
	files map[string]*fileHandle
}

var (
	instance *Logger
	once     sync.Once
)

func GetInstance() *Logger {
	once.Do(func() {
		instance = &Logger{
			files: make(map[string]*fileHandle),
		}
	})
	return instance
}

func (l *Logger) Start(filename string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.files[filename]; exists {
		return nil
	}

	f, err := os.Create(filename)
	if err != nil {
		return err
	}

	writer := csv.NewWriter(f)
	writer.Write([]string{"timestamp", "direction", "file_offset", "block_offset", "block_size", "server_block_size", "server_file_offset"})

	ch := make(chan SplitDataFrameEntry, 4096)
	done := make(chan struct{})

	go func() {
		for entry := range ch {
			ts := entry.Timestamp.UTC().Format(time.RFC3339Nano)
			record := []string{
				ts,
				entry.Direction,
				fmt.Sprintf("%d", entry.FileOffset),
				fmt.Sprintf("%d", entry.BlockOffset),
				fmt.Sprintf("%d", entry.BlockSize),
				fmt.Sprintf("%d", entry.ServerBlockSize),
				fmt.Sprintf("%d", entry.ServerFileOffset),
			}
			if err := writer.Write(record); err != nil {
				return
			}
		}
		writer.Flush()
		f.Close()
		close(done)
	}()

	l.files[filename] = &fileHandle{ch: ch, done: done}
	return nil
}

func (l *Logger) Log(filename string, entry SplitDataFrameEntry) {
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

func (l *Logger) Stop(filename string) {
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
