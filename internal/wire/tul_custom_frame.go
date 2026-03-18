package wire

import (
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/quicvarint"
)

// A TulCustomFrame is a PING frame
type TulCustomFrame struct {
	Data []byte
}

func (f *TulCustomFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	b = append(b, byte(FrameTypeTulCustom))
	b = append(b, f.Data...)

	return b, nil
}

// Length of a written frame
func (f *TulCustomFrame) Length(_ protocol.Version) protocol.ByteCount {
	return 1 + protocol.ByteCount(len(f.Data))
}

func parseTulCustomFrame(b []byte, _ protocol.Version) (*TulCustomFrame, int, error) {
	_, l, _ := quicvarint.Parse(b)

	return &TulCustomFrame{
		Data: b,
	}, l, nil
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
