// Package format pins every number that defines container format v1 (§6, §6.1).
//
// Nothing here may change after v1.0.0. A reader has no plaintext header, so a
// drifted constant silently makes every existing file undecryptable. The tests
// in this package freeze these values against the vectors in docs/FORMAT.md.
package format

// Version and flags live inside the encrypted header, never in the blob.
const (
	Version = 1

	FlagZstd uint8 = 1 << 0
)

// Blob layout (§6). Offsets are absolute within the blob.
const (
	SaltOffset = 0
	SaltSize   = 16

	HeaderNonceOffset = SaltOffset + SaltSize // 16
	NonceSize         = 24

	HeaderCipherOffset = HeaderNonceOffset + NonceSize // 40
	TagSize            = 16
	HeaderPlainSize    = 16
	HeaderCipherSize   = HeaderPlainSize + TagSize // 32

	BodyNonceOffset = HeaderCipherOffset + HeaderCipherSize // 72

	BodyCipherOffset = BodyNonceOffset + NonceSize // 96

	// BlobOverhead is everything that is not body_plain: the 96-byte prefix
	// plus the body tag. Blob size is BlobOverhead + body_plain.
	BlobOverhead = BodyCipherOffset + TagSize // 112
)

// header_plain layout (§6).
const (
	HeaderVersionOffset  = 0
	HeaderFlagsOffset    = 1
	HeaderReservedOffset = 2
	HeaderReservedSize   = 6
	HeaderBodyLenOffset  = HeaderReservedOffset + HeaderReservedSize // 8
	HeaderBodyLenSize    = 8
)

// Bit order (§6.1): bit j maps to byte j/8 with mask 0x80 >> (j%8), MSB first.
const (
	BitsPerByte  = 8
	MSBFirstBase = 0x80
)

// Key derivation (§6.1). Argon2id takes memory in KiB, not bytes.
const (
	Argon2Time      = 3
	Argon2MemoryKiB = 64 * 1024
	Argon2Threads   = 4
	Argon2KeyLen    = 32
	HKDFHashLen     = 32
)

// HKDF and seed labels (§6.1). The salt for HKDF is nil, never the Argon2 salt.
const (
	LabelSaltPos = "wisanggeni/v1/salt-pos"
	LabelSTC     = "wisanggeni/v1/stc"

	InfoEncKey  = "wisanggeni/v1/enc"
	InfoPosSeed = "wisanggeni/v1/pos"
)

// Carrier limits.
const (
	// MaxPixels is the default guard applied before decoding (100 MP).
	MaxPixels = 100_000_000

	// MaxChannelIndex is the largest channel index the format addresses; it
	// follows from storing positions as uint32 (§11.17).
	MaxChannelIndex = 1<<32 - 1
)

// PNG chunks kept and dropped by the writer (§7.7). Kept chunks preserve their
// original order; dropped chunks carry metadata the format refuses to leak.
var (
	PreservedChunks = []string{"gAMA", "cHRM", "sRGB", "iCCP", "pHYs"}
	DroppedChunks   = []string{"eXIf", "tEXt", "zTXt", "iTXt", "tIME"}
)
