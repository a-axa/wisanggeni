package stego

import "sort"

// PoolPositions returns the first k channels of P', the keyed STC pool order
// (§7.2).
//
// P is every channel of the carrier except the salt positions S, in ascending
// order; P' is P permuted by Shuffle seeded with posSeed. Because STC consumes
// P' strictly in order, embedding m bits only needs the first m positions, so k
// bounds the work and the memory to the payload instead of the carrier.
//
// posSeed is derived from the passphrase (info "wisanggeni/v1/pos"), so P' is
// meaningless to anyone who has not read the salt and derived the key.
//
// The result is nil when the carrier has no pool: too few channels, or k <= 0.
// Positions always exclude S and stay in range; callers can rely on that.
func PoolPositions(channels uint32, posSeed [32]byte, k int) []uint32 {
	salt := SaltPositions(channels)
	pool := int64(channels) - int64(len(salt))
	if pool <= 0 || k <= 0 {
		return nil
	}
	if int64(k) > pool {
		k = int(pool)
	}

	// skipSalt walks S in ascending order, so sort a copy. S itself stays in
	// shuffle order for EmbedSalt.
	sort.Slice(salt, func(i, j int) bool { return salt[i] < salt[j] })

	indices := Shuffle(uint64(pool), posSeed, k)

	// Map the pool indices to absolute channels in place; the caller owns the
	// slice Shuffle returned.
	for i, p := range indices {
		indices[i] = skipSalt(salt, p)
	}
	return indices
}

// skipSalt maps the p-th element of P to its absolute channel index: p plus the
// number of salt positions at or below the result. S is small (SaltChannels),
// so the cascade costs at most len(S) steps.
func skipSalt(salt []uint32, p uint32) uint32 {
	c := p
	for _, s := range salt {
		if s > c {
			break
		}
		c++
	}
	return c
}
