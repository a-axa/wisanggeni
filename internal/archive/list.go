package archive

// ListOptions controls List.
type ListOptions struct {
	// Compressed says the payload is zstd, i.e. header_plain.flags bit 0.
	Compressed bool

	// MaxBytes caps the decompressed size. Zero means DefaultMaxBytes. A
	// negative value is also treated as DefaultMaxBytes: an unbounded cap lets a
	// zstd bomb grow until the process is killed, which contradicts the bounded
	// decode in docs/FORMAT.md §13.1. A caller that needs a larger ceiling sets
	// one explicitly.
	MaxBytes int64
}

// capBytes resolves an option value to the limit actually applied.
func capBytes(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return DefaultMaxBytes
	}
	return maxBytes
}

// List decodes a payload and returns its entries without touching the
// filesystem, so `inspect` can name what a blob holds without extracting it.
//
// Every check Unpack performs on the archive structure is applied here too, so
// an archive that would be refused on the way to disk is refused here as well.
func List(data []byte, opts ListOptions) ([]Entry, error) {
	maxBytes := capBytes(opts.MaxBytes)

	payload, err := decodePayload(data, opts.Compressed, maxBytes)
	if err != nil {
		return nil, err
	}
	items, err := planArchive(payload, maxBytes)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, len(items))
	for i, it := range items {
		entries[i] = it.Entry
	}
	return entries, nil
}
