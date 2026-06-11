package utils

import (
	"io"
	"log"
	"os"
)

func ReadChunk(path string, offset int64, length int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, length)

	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}

	return buf[:n], nil
}

func GetFileSize(path string) uint64 {
	info, err := os.Stat("../movie.mp4")
	if err != nil {
		panic(err)
	}

	size := info.Size()
	return uint64(size)
}

func WriteChunk(path string, offset int64, data []byte) int {
	file, err := os.OpenFile(path, os.O_CREATE, 0644)
	defer file.Close()
	if err != nil {
		panic(err)
	}
	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		log.Println("FileOperations - seekError:", err)
	}

	n, err := file.Write(data)
	if err != nil {
		log.Println("accept error:", err)
	}

	return n
}
