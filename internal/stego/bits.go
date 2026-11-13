package stego

import (
	"image"

	"github.com/a-axa/wisanggeni/internal/format"
)

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

// ChannelCount returns T for a normalised carrier, or 0 when the image has no
// pixels or addresses more channels than a uint32 position can hold. Zero is
// therefore always a reason to reject, never a usable carrier.
func ChannelCount(img *image.NRGBA) uint32 {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	n := int64(b.Dx()) * int64(b.Dy()) * 3
	if n <= 0 || n > int64(format.MaxChannelIndex) {
		return 0
	}
	return uint32(n)
}

// channelOffset returns the index of channel c inside Pix. It assumes the
// channel is in range; callers check that first.
func channelOffset(img *image.NRGBA, c uint32) int {
	b := img.Bounds()
	w := uint32(b.Dx())
	pixel, component := ChannelRGB(c)
	x, y := int(pixel%w), int(pixel/w)
	return img.PixOffset(b.Min.X+x, b.Min.Y+y) + int(component)
}

// LSB returns bit 0 of the channel at index c (§6.1).
func LSB(img *image.NRGBA, c uint32) byte {
	return img.Pix[channelOffset(img, c)] & 1
}

// SetLSB replaces bit 0 of the channel at index c. The value moves by at most
// one, and alpha is never touched. This is the plain write; the embed path uses
// LSB matching instead (§7.6).
func SetLSB(img *image.NRGBA, c uint32, bit byte) {
	off := channelOffset(img, c)
	img.Pix[off] = img.Pix[off]&^1 | bit&1
}

// BytesToBits expands data into one 0/1 byte per bit, MSB first (§6.1).
func BytesToBits(data []byte) []byte {
	bits := make([]byte, 0, len(data)*format.BitsPerByte)
	for _, b := range data {
		for j := 0; j < format.BitsPerByte; j++ {
			bits = append(bits, (b&BitMask(j))/BitMask(j))
		}
	}
	return bits
}

// BitsToBytes packs bits MSB-first, the inverse of BytesToBits. len(bits) is
// expected to be a multiple of eight; a trailing partial byte is dropped, so a
// caller that cares must pass whole bytes.
func BitsToBytes(bits []byte) []byte {
	if len(bits) < format.BitsPerByte {
		return nil
	}
	out := make([]byte, 0, len(bits)/format.BitsPerByte)
	for i := 0; i+format.BitsPerByte <= len(bits); i += format.BitsPerByte {
		var b byte
		for j := 0; j < format.BitsPerByte; j++ {
			b |= (bits[i+j] & 1) << (format.BitsPerByte - 1 - j)
		}
		out = append(out, b)
	}
	return out
}
