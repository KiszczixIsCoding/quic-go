package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	filename := "movie.mp4"

	file, err := os.Open(filename)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	const chunkSize = 64 * 1024 // 64 KB
	buf := make([]byte, chunkSize)

	chunkNumber := 0
	for {
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			panic(err)
		}
		if n == 0 { // koniec pliku
			break
		}

		chunkNumber++
		fmt.Printf("Chunk #%d, bytes read: %d\n", chunkNumber, n)
	}

	fmt.Println("Wszystkie paczki wczytane.")
}
