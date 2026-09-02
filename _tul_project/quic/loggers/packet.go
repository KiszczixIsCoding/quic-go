package loggers

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type PacketEntry struct {
	Timestamp  time.Time
	ConnID     string
	Offset     uint64
	DataSize   int
	Throughput float64
}

type PacketLogger struct {
	ch   chan PacketEntry
	done chan struct{}
	once sync.Once
}

var packetLogger = &PacketLogger{}

func Packet() *PacketLogger { return packetLogger }

func (l *PacketLogger) Start(filename string) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Cannot create log file: %v", err)
	}
	f.WriteString("timestamp,conn_id,offset,data_size,throughput\n")

	l.ch = make(chan PacketEntry, 1000)
	l.done = make(chan struct{})

	go func() {
		for entry := range l.ch {
			ts := entry.Timestamp.Format("2006-01-02 15:04:05.000000")
			line := fmt.Sprintf("%s,%s,%d,%d,%.2f\n",
				ts, entry.ConnID, entry.Offset, entry.DataSize, entry.Throughput)
			if _, err := f.WriteString(line); err != nil {
				log.Printf("Log write error: %v", err)
			}
		}
		f.Close()
		close(l.done)
	}()
}

func (l *PacketLogger) Log(e PacketEntry) {
	select {
	case l.ch <- e:
	default:
	}
}

func (l *PacketLogger) Stop() {
	l.once.Do(func() {
		close(l.ch)
		<-l.done
	})
}
