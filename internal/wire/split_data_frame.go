package wire

import (
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/quicvarint"
)

type SplitDataFrame struct {
	FileOffset      uint64
	BlockOffset     uint64
	BlockSize       uint64
	ServerBlockSize uint64
}

func parseSplitDataFrame(b []byte, _ protocol.Version) (*SplitDataFrame, int, error) {
	fileOffset, l1, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l1:]

	blockOffset, l2, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l2:]

	blockSize, l3, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l3:]

	serverBlockSize, l4, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}

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

func (f *SplitDataFrame) Length(version protocol.Version) protocol.ByteCount {
	return 1 + // FrameType
		protocol.ByteCount(quicvarint.Len(f.FileOffset)) +
		protocol.ByteCount(quicvarint.Len(f.BlockOffset)) +
		protocol.ByteCount(quicvarint.Len(f.BlockSize)) +
		protocol.ByteCount(quicvarint.Len(f.ServerBlockSize))
}
