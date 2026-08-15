package wire

import (
	"fmt"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/quicvarint"
)

// A GapFillFrame is a gap-fill request frame
type GapFillFrame struct {
	Data []byte
}

func (f *GapFillFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = append(b, byte(FrameTypeGapFill))
	b = quicvarint.Append(b, uint64(len(f.Data)))
	b = append(b, f.Data...)
	return b, nil
}

// Length of a written frame
func (f *GapFillFrame) Length(_ protocol.Version) protocol.ByteCount {
	return 1 + protocol.ByteCount(quicvarint.Len(uint64(len(f.Data)))) + protocol.ByteCount(len(f.Data))
}

func parseGapFillFrame(b []byte, _ protocol.Version) (*GapFillFrame, int, error) {
	length, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l:]
	if uint64(len(b)) < length {
		return nil, 0, fmt.Errorf("GapFillFrame: not enough data: have %d, need %d", len(b), length)
	}
	data := make([]byte, length)
	copy(data, b[:length])
	return &GapFillFrame{
		Data: data,
	}, l + int(length), nil
}
