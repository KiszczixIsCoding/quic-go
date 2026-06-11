package utils

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"

	_ "github.com/quic-go/quic-go"
)

type CsvLogger struct {
	file   *os.File
	writer *csv.Writer
}

func NewCsvLogger(filename string) (*CsvLogger, error) {
	file, err := os.Create(filename)
	if err != nil {
		return nil, err
	}

	writer := csv.NewWriter(file)

	// Nagłówek CSV
	header := []string{
		"RTT conn1",
		"RTT conn2",
		"FileOffset",
	}

	if err := writer.Write(header); err != nil {
		return nil, err
	}

	writer.Flush()

	return &CsvLogger{
		file:   file,
		writer: writer,
	}, nil
}

func (l *CsvLogger) WriteRow(rtt1, rtt2 time.Duration, offset int64) error {
	record := []string{
		strconv.FormatFloat(rtt1.Seconds()*1000, 'f', 2, 64), // ms
		strconv.FormatFloat(rtt2.Seconds()*1000, 'f', 2, 64), // ms
		strconv.FormatInt(offset, 10),
	}

	if err := l.writer.Write(record); err != nil {
		return err
	}

	l.writer.Flush()
	return l.writer.Error()
}

func (l *CsvLogger) Close() error {
	l.writer.Flush()
	return l.file.Close()
}

func save_csv_data() {
	logger, err := NewCsvLogger("../stats/stats.csv")
	if err != nil {
		panic(err)
	}
	defer logger.Close()
	var fileOffset int64 = 0

	for i := 0; i < 10; i++ {
		err := logger.WriteRow(time.Duration(i), time.Duration(i), fileOffset)
		if err != nil {
			fmt.Println("CSV write error:", err)
		}

		fileOffset += 4096

		time.Sleep(1 * time.Second)
	}

	fmt.Println("CSV zapisany.")
}
