package wire

import (
	"fmt"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/quicvarint"
)

type SplitDataFrame struct {
	FileOffset      uint64 // Offset in file
	BlockOffset     uint64
	BlockSize       uint64
	ServerBlockSize uint64
}

func parseSplitDataFrame(b []byte, _ protocol.Version) (*SplitDataFrame, int, error) {
	fileOffset, l1, err := quicvarint.Parse(b)
	fmt.Printf("FileOffset data: %v, len=%d, nil? %v\n", fileOffset, l1, err == nil)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l1:]

	blockOffset, l2, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	fmt.Printf("BlockOffset data: %v, len=%d, nil? %v\n", blockOffset, l2, err == nil)
	b = b[l2:]

	blockSize, l3, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	fmt.Printf("BlockSize data: %v, len=%d, nil? %v\n", blockSize, l3, err == nil)
	b = b[l3:]

	serverBlockSize, l4, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	fmt.Printf("ServerBlockSize data: %v, len=%d, nil? %v\n", serverBlockSize, l4, err == nil)

	fmt.Println(fileOffset)
	fmt.Println(blockOffset)
	fmt.Println(blockSize)
	fmt.Println(serverBlockSize)

	return &SplitDataFrame{FileOffset: fileOffset, BlockOffset: blockOffset, BlockSize: blockSize, ServerBlockSize: serverBlockSize}, l1 + l2 + l3 + l4, nil
}

func (f *SplitDataFrame) Append(b []byte, version protocol.Version) ([]byte, error) {
	b = append(b, byte(FrameTypeSplitData))

	b = quicvarint.Append(b, f.FileOffset)
	b = quicvarint.Append(b, f.BlockOffset)
	b = quicvarint.Append(b, f.BlockSize)
	b = quicvarint.Append(b, f.ServerBlockSize)

	return b, nil
}

// Length of a written frame
func (f *SplitDataFrame) Length(version protocol.Version) protocol.ByteCount {
	return 1 + // FrameType
		protocol.ByteCount(quicvarint.Len(f.FileOffset)) +
		protocol.ByteCount(quicvarint.Len(f.BlockOffset)) +
		protocol.ByteCount(quicvarint.Len(f.BlockSize)) +
		protocol.ByteCount(quicvarint.Len(f.ServerBlockSize))
}
