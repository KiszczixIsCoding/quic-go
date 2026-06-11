package wire

import (
	"fmt"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/quicvarint"
)

// A TulCustomFrame is a PING frame
type TulCustomFrame struct {
	Data []byte
}

func (f *TulCustomFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = append(b, byte(FrameTypeTulCustom))
	b = quicvarint.Append(b, uint64(len(f.Data)))
	b = append(b, f.Data...)
	return b, nil
}

// Length of a written frame
func (f *TulCustomFrame) Length(_ protocol.Version) protocol.ByteCount {
	return 1 + protocol.ByteCount(quicvarint.Len(uint64(len(f.Data)))) + protocol.ByteCount(len(f.Data))
}

func parseTulCustomFrame(b []byte, _ protocol.Version) (*TulCustomFrame, int, error) {
	length, l, err := quicvarint.Parse(b)
	if err != nil {
		return nil, 0, replaceUnexpectedEOF(err)
	}
	b = b[l:]
	if uint64(len(b)) < length {
		return nil, 0, fmt.Errorf("TulCustomFrame: not enough data: have %d, need %d", len(b), length)
	}
	data := make([]byte, length)
	copy(data, b[:length])
	return &TulCustomFrame{
		Data: data,
	}, l + int(length), nil
}

//
//type TulCustomFrame struct {
//	Data []byte
//}
//
////func (f *TulCustomFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
////	return append(b, byte(FrameTypePing), 0xAA, 0xBB, 0xCC, 0xDD), nil
////}
////
////// Length of a written frame
////func (f *TulCustomFrame) Length(_ protocol.Version) protocol.ByteCount {
////	println("TUL FRAME LENGTH")
////	return 1 + 4
////}
//
//func (f *TulCustomFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
//	b = append(b, byte(FrameTypeTulCustom))
//	b = append(b, f.Data...)
//
//	return b, nil
//}
//
//// Length of a written frame
//func (f *TulCustomFrame) Length(_ protocol.Version) protocol.ByteCount {
//	return 1 + protocol.ByteCount(len(f.Data))
//}
