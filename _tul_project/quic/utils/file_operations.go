package utils

import (
	"io"
	"log"
	"os"
	"sync"
)

var fileCache = struct {
	sync.Once
	file *os.File
	err  error
}{}

func ReadChunk(path string, offset int64, length int) ([]byte, error) {
	fileCache.Once.Do(func() {
		fileCache.file, fileCache.err = os.Open(path)
	})
	if fileCache.err != nil {
		return nil, fileCache.err
	}

	buf := make([]byte, length)
	n, err := fileCache.file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}

	return buf[:n], nil
}

func GetFileSize(path string) uint64 {
	info, err := os.Stat(path)
	if err != nil {
		panic(err)
	}

	size := info.Size()
	return uint64(size)
}

func WriteChunk(path string, offset int64, data []byte) int {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		log.Println("FileOperations - seekError:", err)
	}

	n, err := file.Write(data)
	if err != nil {
		log.Println("FileOperations - writeError:", err)
	}

	return n
}
