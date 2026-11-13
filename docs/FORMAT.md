# Wisanggeni — Container Format v1

This document is the contract between writer and reader. Every number here has
a named constant in `internal/format` or `internal/stego`, and every item has a
test vector that CI runs. There are no magic numbers outside those constants.

**Status: FROZEN.** After the `v1.0.0` tag, changing any item here means format
v2. The blob has no plaintext header, so a reader has no way to "guess" the
version: a shifted constant makes every existing file undecryptable.

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
  STC over `P'`. At the frozen code rate of 1/2, each payload byte occupies 16
  consecutive `P'` channels (§10).
- At the stego level the blob is split exactly like this: the first
  `format.SaltSize` (16) bytes go to `S` with plain LSB matching, and the
  remaining bytes go through STC over `P'`. The reader recovers the salt first
  (it needs no key), derives `pos_seed` from it, and only then can build `P'`.
  `stego.EmbedBlob` / `ExtractBlob` / `VerifyBlob` implement this split
  (§7.8 steps 10-14, §7.9 steps 3-8); `VerifyBlob` is the writer's self-check
  and reports `ErrVerify` on any mismatch.

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
| `CapacityRaw(w,h)` | `floor((T − 128) / 16)` | maximum blob size |
| `Capacity(w,h)` | `max(0, CapacityRaw − 112)` | maximum `body_plain` size |
| `SuggestedCapacity(w,h)` | `max(0, floor(T/80) − 112)` | `body_plain` at 10% fill |
| `StrongCapacity(w,h)` | `max(0, floor(T/160) − 112)` | `body_plain` at 5% fill |
| `FillRatio(blob)` | `8 × blob / T` | computed from the blob, not `body_plain` |

The `CapacityRaw` formula follows from the STC code rate of 1/2 (§10): the pool
is `T − 128` channels, and one payload byte needs 16 pool channels, so a blob at
raw capacity consumes just under half of `T` (`FillRatio(CapacityRaw) ≈ 0.49999`).

Fill policy (§5): warning at `> 0.05`, `--force` required at `> 0.10`, reject
at `> 0.50`. `Pack` uses `Capacity()` directly as `maxBytes`.

Constants: `stego.FillWarn`, `FillForce`, `FillReject`.
Vector: `internal/stego/capacity_test.go` → `TestCapacityTable`,
`TestFillRatio`.

### 7.1 Verified table

| Carrier | T | CapacityRaw | Capacity | Suggested (10%) | Strong (5%) |
|---|---|---|---|---|---|
| 1920×1080 | 6220800 | 388792 | 388680 | 77648 | 38768 |
| 1080×1350 | 4374000 | 273367 | 273255 | 54563 | 27225 |
| 3840×2160 | 24883200 | 1555192 | 1555080 | 310928 | 155408 |
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
  (exit 5) unless `--force`. The bounds are closed on the lower edge.

The score reuses the cost-map engine in a single pass: `CoverScore` counts the
channels with `S > 16` while it builds `rho`, then selects the median in place
with a three-way quickselect, so a large carrier is not copied and no second
buffer is allocated. A nil or empty carrier scores `reject` with zero fractions.
The verdict labels are `suitable`, `warning`, `reject` (all lowercase).

Constants: `stego.TexturedSThreshold = 16`, `TexturedFractionGood = 0.20`,
`TexturedFractionWarn = 0.05`.
Vector: `TestCoverScoreVectorPattern` (the §8.1 8×8 pattern).

### 9.1 Verified values

| Carrier | textured_fraction | median_rho | verdict |
|---|---|---|---|
| §8.1 8×8 pattern | 1 | `0.0042531553983439184` | suitable |
| flat 16×16, value 128 | 0 | `0.1` | reject |

On an 8×8 tile the 5×5 smoothing spreads the boundary residual over the flat
half, so every channel counts as textured; on a full-size carrier the flat half
would stay below the threshold.

Vector: `TestCoverScoreVectorPattern`, `TestCoverScoreFlatIsUnsuitable`,
`TestCoverScoreNoiseIsSuitable`, `TestCoverScoreEmptyIsUnsuitable`,
`TestClassifyCoverBoundaries`.

## 10. STC (syndrome-trellis code)

Status: **FROZEN**.

### 10.1 Parameters

- Binary code, `h = STCHeight = 8` parity rows (256 syndromes), `w = STCWidth =
  16` columns per block → **code rate 1/2 bit per channel**. One payload byte
  (8 message bits) occupies 16 pool channels.
- Blocks buffer `STCBlockChannels = 65536` pool channels when walking `P'`; the
  code itself is block-local at `STCWidth` and the last block may be shorter.
- Why rate 1/2: at rate 1 (`w = h`) `Ĥ` is square, so `Ĥ·e = s` has a unique
  solution and there is no optimisation room at all — the encoder could never
  beat uniform LSB matching. `w = 2h` is the smallest width that gives real
  freedom (256 candidates per block) while keeping the capacity formula simple.
  The cost is that capacity is halved relative to rate 1: see §7.

### 10.2 Parity-check matrix

Public and identical for every block. Derived from `ChaCha8` seeded with
`SHA-256("wisanggeni/v1/stc")` (`format.LabelSTC`); column `i` is byte `i` of the
raw `Uint64()` stream. Frozen columns:

```
0x84 0x3F 0x8F 0x45 0x97 0x5E 0x3E 0x0E 0x0E 0x20 0x00 0xA7 0x4E 0x7E 0x25 0x0A
```

Vector: `TestParityMatrixGolden`. The same 16 bytes are stored in
`internal/stego/testdata/stc_vectors.json` (`columns`) and in
`detector/stc_ref.py` (`PARITY_COLUMNS`); `TestSTCVectors` and
`stc_ref.py --check` fail on any drift.

### 10.3 Embedding

For each block `b` covering 16 pool channels, with `cover` the block's 16 LSBs as
a 16-bit mask:

1. `target = data[b] XOR syndrome(cover)`.
2. Solve `Ĥ·m = target` for the cheapest flip mask `m` (see 10.4).
3. For every bit set in `m`, move the channel by ±1 with LSB matching (§7.6),
   so `|y_i − x_i| ∈ {0,1}` and the stego LSB is `cover_i XOR m_i`.
4. The decoder's view is `Ĥ·LSB(stego) = data[b]` by construction.

The objective is `sum rho_i` over the flipped columns. Failure (syndrome
mismatch) returns `ErrSTC`; it is never a fallback to random LSB.

### 10.4 Solver (frozen ordering)

Columns are scanned left to right. A column outside the span of the columns
already chosen is added to the basis and its span recorded in `solve[s]` (one
basis combination per reachable syndrome). A dependent column becomes a kernel
generator: column `c` XOR the basis combination that reproduces it. Every
solution of `Ĥ·m = target` is then `solve[target] XOR any subset of the 8 kernel
generators`; all **256** candidates are enumerated and the cheapest kept. This
is a global optimum per block.

Tie-break `maskLess`: among equal costs, the mask that leaves the **earliest**
column untouched wins (lexicographic order of per-column decisions, earliest
first). The Go solver and `decisions()` in `stc_ref.py` implement the same rule.

### 10.5 Extraction

The decoder computes the syndrome `Ĥ·LSB(y)` per block over the `P'` order. It
needs neither the cost map nor the cover, and the state is carried across the
`reveal` steps (prefix, then body). The bit split per block is a pure function
of `(m, B)` and is tested.

### 10.6 Vectors

`internal/stego/testdata/stc_vectors.json` holds 8 cases produced independently
by `detector/stc_ref.py`, which brute-forces all 2¹⁶ masks from the definition
rather than reusing the basis construction. In Go:
`TestSTCVectors` (golden), `TestSTCSolveMaskBruteForce` (exhaustive scan over
all masks), `TestSTCRoundTrip` / `TestSTCRoundTripManySizes`,
`TestSTCBeatsUniformLSBMatching`, `TestSTCChangesStayWithinOneAndFlipLSB`,
`TestSTCEdgeValues`.

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

## 13. Archive layer (`body_plain`)

§3 defines `body_plain` only as opaque bytes. This section records what the
writer puts there; it defines no binary constants of its own and changes none of
the frozen values above.

`body_plain` is a tar stream, optionally zstd-compressed. `header_plain.flags`
bit 0 (`format.FlagZstd`) says whether it is compressed; no other framing byte is
added, so the decoder hands `body_ct`'s plaintext straight to zstd when the flag
is set and to the tar reader otherwise.

Deterministic output is required, because two runs over the same inputs must
produce byte-identical carriers:

- Entry names are the input basename plus, for directories, the path relative to
  it, slash separated and never absolute.
- Entries are sorted by name bytes.
- Metadata is stripped: `uid`/`gid` are 0, `uname`/`gname` are empty, `mtime`,
  `atime` and `ctime` are zeroed, and `mode` keeps only the permission bits.
- Names are not Unicode-normalised; NFD names from macOS stay NFD.
- zstd runs with encoder concurrency 1. The default follows the CPU count, which
  would make identical inputs compress to different bytes on different machines.

Compression is applied only when it makes the payload strictly smaller; the
`zstd` output is compared against the raw tar and the smaller one wins, with the
flag set accordingly. `Pack` caps the result at the caller's byte budget
(`Capacity(w,h)`), so an oversized payload fails rather than being truncated.
