package stego

import "github.com/a-axa/wisanggeni/internal/format"

// BitMask returns the mask of bit j inside its byte. The order is frozen:
// bit j lives in byte j/8 at mask 0x80 >> (j%8), MSB first (§6.1).
func BitMask(j int) byte {
	return format.MSBFirstBase >> (j % format.BitsPerByte)
}

// ChannelRGB maps a channel index to its pixel index and component, where
// component 0, 1 and 2 are R, G and B (§6.1).
func ChannelRGB(c uint32) (pixel uint32, component uint8) {
	return c / 3, uint8(c % 3)
}
