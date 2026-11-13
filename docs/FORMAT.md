# Wisanggeni — Container Format v1

This document is the contract between writer and reader. Every number here has
a named constant in `internal/format` or `internal/stego`, and every item has a
test vector that CI runs. There are no magic numbers outside those constants.

**Status: FROZEN except §10 (STC).** After the `v1.0.0` tag, changing any item
here means format v2. The blob has no plaintext header, so a reader has no way
to "guess" the version: a shifted constant makes every existing file
undecryptable.

Notation: `T = W × H × 3` (the number of image channels).

---

## 1. Bit order and endianness

- The bit stream is read from offset 0. Bit `j` lives in byte `j / 8` with mask
  `0x80 >> (j % 8)` (MSB-first).
- Frozen example: byte `0xA5` → bits `1,0,1,0,0,1,0,1`.
- All multi-byte integers are little-endian.
- The LSB of a channel is read as `value & 1`.

Constants: `format.BitsPerByte = 8`, `format.MSBFirstBase = 0x80`,
`stego.BitMask(j)`.
Vector: `internal/stego/vectors_test.go` → `TestBitOrder`.

## 2. Channel mapping

- Channel `c` ↦ pixel `c / 3`, component `c % 3` (0=R, 1=G, 2=B).
- Positions are stored as `uint32`, so `T ≤ 2³²−1`.

Constants: `stego.ChannelRGB`, `format.MaxChannelIndex`.
Vector: `TestChannelRGB` (including `191 → pixel 63, component 2`).

## 3. Blob layout (§6)

| offset | size | contents |
|---|---|---|
| 0 | 16 | `salt` (random) |
| 16 | 24 | `header_nonce` (random) |
| 40 | 32 | `header_ct` = XChaCha20-Poly1305(enc_key, header_nonce, header_plain) |
| 72 | 24 | `body_nonce` (random) |
| 96 | N+16 | `body_ct` = XChaCha20-Poly1305(enc_key, body_nonce, body_plain) |

- Fixed overhead `96 + 16 = 112` bytes → `blob size = 112 + body_len`.
- Header AEAD AAD = `salt`. Body AEAD AAD = `header_ct`.
- `header_plain` (16 bytes): `version` (=1) at offset 0, `flags` at offset 1
  (bit0 = zstd used), `reserved` 6 zero bytes at offset 2, `body_len` uint64
  little-endian at offset 8.

Constants: `format.SaltSize`, `NonceSize`, `TagSize`, `HeaderPlainSize`,
`BlobOverhead`, every `format.*Offset`, `format.Version`, `format.FlagZstd`.
Vector: `internal/format/format_test.go` → `TestBlobLayout`,
`TestHeaderPlainLayout`.

## 4. Key derivation

- Passphrase normalisation: **NFKC**, then its UTF-8 bytes are fed to Argon2id.
- Argon2id: `t=3`, `m=65536 KiB`, `p=4`, `keyLen=32` → `K`.
- HKDF (SHA-256) with **salt `nil`** (equivalent to 32 zero bytes per RFC 5869;
  the Argon2 salt is not reused):
  - `PRK = HKDF-Extract(salt=nil, IKM=K)`
  - `enc_key = HKDF-Expand(PRK, info="wisanggeni/v1/enc", L=32)`
  - `pos_seed = HKDF-Expand(PRK, info="wisanggeni/v1/pos", L=32)`
  - Two separate Expand calls, **not** one 64-byte Expand split in half.

Constants: `format.Argon2*`, `format.HKDFHashLen`, `format.InfoEncKey`,
`format.InfoPosSeed`.
Vector: `internal/format/hkdf_test.go`, `internal/format/nfkc_test.go`.

### 4.1 HKDF vectors

`K = 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f`
(HKDF salt `nil`)

| output | value (hex, 32 bytes) |
|---|---|
| `enc_key` | `4f9e59b17124a88318dcb4f93f891901ae7b5b8758410e331ed314530609914a` |
| `pos_seed` | `e4efae5a801d64e3cdc9fe0bd2b1c9041e3841a4e96f790ad38a6d9ebc2f4c19` |

### 4.2 NFKC vectors

| input | result | UTF-8 bytes (hex) |
|---|---|---|
| `Caf\u00e9` (NFC) | `Café` | `436166c3a9` |
| `Cafe\u0301` (NFD) | `Café` | `436166c3a9` |
| `ｗｉｓａｎｇｇｅｎｉ` | `wisanggeni` | `776973616e6767656e69` |

## 5. Randomness, bounded sampling, permutation

- PRNG: ChaCha8 from `math/rand/v2` (the C2SP `chacha8rand` specification); only
  raw `Uint64()` is used. `rand.Rand.IntN`/`Shuffle` are not used.
- `bounded(rng, n)`: Lemire + rejection —
  `hi, lo = bits.Mul64(rng.Uint64(), n)`; when `lo < n`, reject while
  `lo < (2⁶⁴ − n) % n`; the result is `hi`.
- `Shuffle(n, seed)` is a lazy Fisher-Yates that yields the first `k` elements.
  Two variants:
  - **dense** when `k > n/10` (memory ≈ 4n bytes),
  - **map** otherwise (memory ≈ 30–50 bytes × k).
  Both must produce an identical order for the same `(n, seed)`.
- The `n/10` threshold is `stego.DenseDivisor`; the value is a heuristic and
  may change before release, as long as the resulting order does not.
- B3 memory decision for a 12 MP carrier (T = 36,000,000), measured with
  `go test -bench BenchmarkPoolPositions -benchmem`:
  - typical payload (10% fill, k ≈ 2.49 M bits): **86 MB** (map variant),
  - worst case (k just past the dense threshold, k ≈ 3.6 M): **158 MB**
    (dense variant).

  Both are below the 300 MB budget, so the keyed Feistel fallback is not used
  and the format keeps Fisher-Yates. The `Shuffle` result is mapped to channels
  in place, which removed one 14 MB copy; a swap to Feistel would change every
  permutation and invalidate the salt vectors, so it stays unimplemented.

## 6. Salt positions and STC pool (§7.2)

- `S` = the first 128 elements of `Shuffle(n = T, seed = SHA-256("wisanggeni/v1/salt-pos"))`.
  Salt positions are **public**; salt secrecy is not required.
- `P` = every channel in `[0, T)` except `S`, in ascending order.
- `P'` = `P` shuffled with `Shuffle(n = T − 128, seed = pos_seed)`.
- `S` and `P` are guaranteed disjoint. The salt is written into `S` with LSB
  matching.
- The payload (the blob after the salt: 80-byte prefix + body) is written with
  STC over `P'`.

Constants: `stego.SaltChannels = 128`, `format.LabelSaltPos`.
Vector: `TestSaltPositionsGolden`.

### 6.1 Salt position vector, `T = 192` (8×8 carrier)

`S` uses the dense variant because `128 > 192/10`.

- First 8 elements: `148, 65, 169, 39, 146, 140, 26, 187`
- SHA-256 over the 128 `S` values encoded as little-endian `uint32`:
  `9373c9f0ea969158b29c86fd10a166056a7dfb993491519922fac43aa149ac06`

## 7. Capacity (§6.1)

| function | formula | meaning |
|---|---|---|
| `CapacityRaw(w,h)` | `floor(T/8)` | maximum blob size |
| `Capacity(w,h)` | `max(0, floor(T/8) − 112)` | maximum `body_plain` size |
| `SuggestedCapacity(w,h)` | `max(0, floor(T/80) − 112)` | `body_plain` at 10% fill |
| `StrongCapacity(w,h)` | `max(0, floor(T/160) − 112)` | `body_plain` at 5% fill |
| `FillRatio(blob)` | `8 × blob / T` | computed from the blob, not `body_plain` |

Fill policy (§5): warning at `> 0.05`, `--force` required at `> 0.10`, reject
at `> 0.50`. `Pack` uses `Capacity()` directly as `maxBytes`.

Constants: `stego.FillWarn`, `FillForce`, `FillReject`.
Vector: `internal/stego/capacity_test.go` → `TestCapacityTable`,
`TestFillRatio`.

### 7.1 Verified table

| Carrier | T | CapacityRaw | Capacity | Suggested (10%) | Strong (5%) |
|---|---|---|---|---|---|
| 1920×1080 | 6220800 | 777600 | 777488 | 77648 | 38768 |
| 1080×1350 | 4374000 | 546750 | 546638 | 54563 | 27225 |
| 3840×2160 | 24883200 | 3110400 | 3110288 | 310928 | 155408 |
| 1×1 | 3 | 0 | 0 | 0 | 0 |

## 8. Cost map (§7.3)

Per channel plane `X` (values 0–255 as `float64`):

1. High-pass residual `R = X * K` with
   `K = [−1,2,−1; 2,−4,2; −1,2,−1]`.
2. `A = |R|`.
3. `A1` = 3×3 box mean of `A`, then `S` = 5×5 box mean of `A1`.
4. `rho = 1 / (S + 10)`.
5. When the original channel value is `0` or `255`, `rho` is multiplied by `4`.

Two decisions are frozen here, because §7.3 says "mirror padding at the edges"
and "the divisor is the number of valid pixels":

- Indices outside `[0, n)` are mapped by `mirrorIndex`: `−1 → 1`, `n → n−2`
  (without repeating the edge pixel), and `n = 1` maps every index to 0.
- After that mapping every window position refers to a real pixel, so the
  divisor is the full window size: 9 for 3×3, 25 for 5×5.

Arithmetic must be `float64` and the operation order must match the list above.
The cost map is computed from the original cover before embedding; the STC
decoder does not need costs.

Constants: `stego.CostKernel`, `CostEpsilon = 10`, `CostEdgeMultiplier = 4`.
Vector: `TestCostMapGolden`, `TestCostMapFlatAndEdges`,
`TestCostMapTexturedIsCheaper`.

### 8.1 8×8 vector pattern

```
for x < 4:  P(x, y, c) = 128
for x >= 4: P(x, y, c) = (x*37 + y*17 + c*53 + x*y*97) mod 256
```

The left half is flat (expensive to embed), the right half is textured (cheap).

- First 8 `rho` values (channels 0..7):
  `0.023248605083694977`, `0.025999537785994915`, `0.034059945504087197`,
  `0.013199577613516367`, `0.015002000266702229`, `0.01971263360785001`,
  `0.0066646919431279615`, `0.0078561452513966484`
- SHA-256 over all `rho` (192 values, little-endian `math.Float64bits`):
  `9e1741de9274574aa7f575e40cb6b97cd550f61f0b38a4ea534c5b38e327a26a`
- Flat 8×8 image of 128: `rho = 0.1` on every channel (S = 0).
- 1×1 image with value 0: `rho = 0.4` (S = 0, multiplied by 4).

## 9. Cover scoring (§7.5)

- `textured_fraction` = number of channels with `S > 16`, divided by `T`.
- `median_rho` = median `rho` across all channels.
- Classification: `≥ 0.20` suitable; `0.05 ≤ x < 0.20` warning; `< 0.05` reject
  (exit 5) unless `--force`.

Constants: `stego.TexturedSThreshold = 16`, `TexturedFractionGood = 0.20`,
`TexturedFractionWarn = 0.05`.

## 10. STC — NOT FROZEN YET

What is already settled:

- Binary, `h = 8` (256 Viterbi states), 65536-channel blocks, the last block
  may be shorter. `stego.STCHeight`, `stego.STCBlockChannels`.
- Public parity-check matrix from seed `SHA-256("wisanggeni/v1/stc")`
  (`format.LabelSTC`), identical for every block.
- The encoder minimises `sum rho_i * |y_i − x_i|` with `|y_i − x_i| ∈ {0,1}`
  (±1 direction from LSB matching, §7.6). Failure is an error, not a fallback
  to random LSB.
- The decoder computes the syndrome `H * LSB(y)` over the `P'` order, stateful
  across the `reveal` steps (prefix then body).
- The bit split per block is a pure function of `(m, B)` and is tested.

**Still undecided: the code rate.** This blocks the STC vector and task B4.

- A rate of 1 bit/channel (`w = h`) is the only value consistent with the
  capacity table in §6.1 and DoD B6 (round-trip up to 90% of capacity). But at
  rate 1 the matrix is square, so `H·e = s` has a **unique solution** and
  Viterbi has no room to optimise at all. DoD B4 ("STC total distortion smaller
  than uniform LSB matching") can never be met.
- A rate of `h/w < 1` (submatrix `Ĥ` of size `h × w`, `w > h`) gives real
  optimisation room, but the maximum capacity drops to `T × h/w / 8` bytes.
  For `w = 2h`, `Capacity` becomes `floor(T/16) − 112`, and the §6.1 table,
  `SuggestedCapacity`, `StrongCapacity`, and DoD B6 all have to change.

This decision must be made before B4. Once made, this section becomes frozen,
the Python reference (`detector/stc_ref.py`) is written, and its vectors are
stored in `testdata/`.

## 11. PNG chunks (§7.7)

- Preserved, original order: `gAMA`, `cHRM`, `sRGB`, `iCCP`, `pHYs`.
- Dropped: `eXIf`, `tEXt`, `zTXt`, `iTXt`, `tIME`.
- Output: `IHDR` (8-bit RGB, opaque, same dimensions), the preserved colour
  chunks, `IDAT` from the Go encoder, `IEND`. CRCs are recomputed.

Constants: `format.PreservedChunks`, `format.DroppedChunks`.
Vector: `TestPNGChunkPolicy`.

## 12. Carrier limits

- `format.MaxPixels = 100_000_000` (100 MP) is checked via `image.DecodeConfig`
  **before** decoding, so a decompression bomb allocates nothing.
- `T ≤ format.MaxChannelIndex = 2³²−1`, a consequence of `uint32` positions.
- Carriers with alpha `< 255` are rejected (v1.1 backlog).
