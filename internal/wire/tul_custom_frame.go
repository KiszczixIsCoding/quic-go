package wire

import (
	"github.com/quic-go/quic-go/internal/protocol"
)

// A TulCustomFrame is a TUL_CUSTOM frame
type TulCustomFrame struct{}

func (f *TulCustomFrame) Append(b []byte, _ protocol.Version) ([]byte, error) {
	return append(b, byte(FrameTypeTulCustom)), nil
}

// Length of a written frame
func (f *TulCustomFrame) Length(_ protocol.Version) protocol.ByteCount {
	return 1
}
