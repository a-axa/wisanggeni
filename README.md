![Wisanggeni](docs/img/readme_cover.png)

# Wisanggeni

Hide files and folders inside a PNG image.

Wisanggeni packs your input into a tar archive, compresses it with zstd,
encrypts it with XChaCha20-Poly1305 under a key derived from your passphrase, and
embeds the result into the pixels of a PNG you supply. Every channel of the
output changes by at most ±1, the dimensions never change, and the output has no
magic bytes and no plaintext length. With the wrong passphrase there is nothing
in the image to tell apart from noise.

It is a single Go binary with a command line and a terminal UI, no cgo, and no
runtime dependencies.

> **This is not undetectable.** The confidentiality comes from the encryption,
> not from the steganography. Read [Threat model](#threat-model) before you rely
> on it for anything that matters. It has not had an independent security audit.

---

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [The terminal UI](#the-terminal-ui)
- [Commands](#commands)
- [Choosing a carrier](#choosing-a-carrier)
- [Passphrase modes](#passphrase-modes)
- [Threat model](#threat-model)
- [What is not protected](#what-is-not-protected)
- [Measured properties](#measured-properties)
- [Exit codes](#exit-codes)
- [How it works](#how-it-works)
- [Building and testing](#building-and-testing)

## Install

With a Go toolchain:

```sh
go install github.com/a-axa/wisanggeni@latest
```

Or build from a checkout:

```sh
make build      # writes bin/wisanggeni
```

Prebuilt binaries for Linux, macOS and Windows are attached to each release.

## Quick start

```sh
# 1. Check that the carrier is noisy enough to be a good cover.
wisanggeni score photo.png

# 2. Hide a document. The output is a new PNG; photo.png is never modified.
wisanggeni hide notes.pdf -c photo.png -o secret.png

# 3. Prove the payload came back byte for byte.
wisanggeni reveal secret.png -o ./out

# 4. Look inside without extracting anything.
wisanggeni inspect secret.png
```

A folder works the same way:

```sh
wisanggeni hide ~/Documents/contracts -c photo.png -o secret.png
```

Run `wisanggeni` with no arguments to open the terminal UI.

## The terminal UI

The TUI covers the same operations as the CLI, through the same engine, so a bug
only has to be fixed once and the two can never disagree about what a carrier
holds.

- **Hide** walks you through four screens: choose what to hide, choose the
  carrier, read the score and capacity, then type the passphrase twice. The
  carrier is scored and the payload measured before you are asked for a secret,
  so an impossible job fails early.
- **Reveal** asks for the image and the passphrase, then unpacks.
- **Score** and **Capacity** report on one image.
- **Inspect** names the payload. With no passphrase it stops at the encrypted
  header; with one it lists the archive contents without writing anything.

The picker takes `Enter` to open a folder or move on and `Space` to choose a
file. Below 80×24 the TUI asks you to use the CLI rather than rendering a broken
layout, and it is tested at that size.

## Commands

### `hide`

```
wisanggeni hide <input...> -c <carrier.png> -o <output.png> [flags]
```

| Flag | Meaning |
|---|---|
| `-c, --carrier` | Carrier PNG. Required. |
| `-o, --output` | Output PNG. Required, and must not be the carrier. |
| `--force` | Allow overwriting the output, a fill above 10%, and a smooth cover. |
| `--no-compress` | Skip zstd. |
| `--no-verify` | Skip the post-write verification. Not recommended. |
| `-q, --quiet` | Suppress the success line. |
| `--passphrase-file`, `--passphrase-stdin` | See [Passphrase modes](#passphrase-modes). |

The carrier is never touched. The output is written to a temporary file, verified
by decoding it again and comparing the embedded blob, and only then renamed into
place.

### `reveal`

```
wisanggeni reveal <image.png> -o <dir> [flags]
```

| Flag | Meaning |
|---|---|
| `-o, --output` | Destination directory, created if missing. Defaults to `.`. |
| `--force` | Allow replacing files that already exist. |
| `--max-output` | Ceiling on the decompressed size, in bytes. Default 1 GiB. |

The archive is treated as untrusted. Absolute paths, `..`, symlinks, hardlinks,
device nodes and FIFOs are refused, a pre-existing symlink in a parent directory
is not followed, and file modes are masked to `0o777` so setuid bits inside a
payload cannot survive.

### `capacity`, `score`, `inspect`

```sh
wisanggeni capacity photo.png   # four capacity figures and the cover verdict
wisanggeni score photo.png      # just the cover verdict
wisanggeni inspect secret.png   # header, and entries when a passphrase is given
```

`capacity` prints the numbers the other commands use:

```
file          photo.png
dimensions    256x256
channels      196608
capacity raw  12280 bytes
usable        12168 bytes
suggested     2345 bytes (10% fill)
strong        1116 bytes (5% fill)
cover         suitable (textured_fraction 1.000, median_rho 0.00278783)
alpha         no
chunks        none
```

## Choosing a carrier

The carrier decides almost everything about how detectable the result is.

- **Use a photo you took yourself.** An opponent who has the original can diff
  the pixels and prove something changed, no matter what the embedder does.
- **Pick something high-resolution and heavily textured** — foliage, gravel,
  crowds, fabric. Avoid screenshots, flat illustrations, and anything with large
  single-colour areas. Run `wisanggeni score` first: it refuses a carrier whose
  textured fraction is below 0.05, warns between 0.05 and 0.20, and accepts at
  0.20 or above.
- **Stay small.** `hide` warns above a 5% fill, refuses above 10% unless
  `--force` is given, and refuses above 50% outright. `capacity` reports the
  suggested (10%) and strong (5%) sizes for a given image.
- **Delete the original after hiding**, if you do not need it. Leaving it next to
  the output invites a comparison.
- **Send the result as a file, not as a photo.** WhatsApp, Instagram, X and
  Telegram's photo mode all re-compress images, which destroys the payload.
  Telegram's file mode and email attachments are fine.
- **Do not edit, resize, crop, or re-save the output.** Any lossy step, and most
  lossless ones, will destroy the payload.

## Passphrase modes

A passphrase never travels through a command-line argument or an environment
variable: arguments are visible in `ps` and shell history, and environment
variables leak into child processes.

| Mode | Use it when |
|---|---|
| Interactive prompt (default) | You are at a terminal. `hide` asks twice, so a typo cannot produce an unopenable file. |
| `--passphrase-file PATH` | A script needs a secret. Keep the file at mode `0600`; a looser mode produces a warning. |
| `--passphrase-stdin` | Piping from a password manager, e.g. `pass show site \| wisanggeni hide ... --passphrase-stdin`. Reads one line, no confirmation. |

Use a strong one. The key derivation is Argon2id with 64 MiB of memory and three
passes, which is slow enough to make guessing expensive but cannot rescue a weak
passphrase. Aim for at least five random words, for example from a diceware list.
The passphrase is normalised with NFKC first, so the same characters typed on a
different keyboard or input method derive the same key.

## Threat model

### What is protected

| Asset | Protection |
|---|---|
| **File contents** | XChaCha20-Poly1305 with a key from Argon2id, as long as the passphrase is strong. |
| **Existence of the data, fill ≤ 5%** | Adaptive cost map plus STC encoding into a naturally noisy cover. The blind attacks in [Measured properties](#measured-properties) see nothing; a detector trained on Wisanggeni itself was not measured. |
| **Existence of the data, fill 5–10%** | Allowed with a warning. The same two blind attacks still see nothing up to 40%, but assume this is the range where a targeted model starts to matter. |
| **Existence of the data, fill > 10%** | Requires `--force`. Assume a purpose-trained detector will find it. |

### What is not protected

- **Modern machine-learning steganalysis** (SRM plus an ensemble, or SRNet) at
  high fills, and in particular an opponent who trains a detector on Wisanggeni
  output specifically. The adaptive cost map leaves a statistical footprint that
  a targeted model can learn.
- **An opponent holding the original image.** Comparing pixels proves the image
  was modified, and localises roughly where.
- **Recompression** by any platform or messenger that re-encodes the image.
- **A weak passphrase**, a keylogger, or coercion.
- **Implementation bugs.** There has been no independent security audit. Do not
  use this to protect anything where a mistake costs a life.

The design rule behind all of this: confidentiality comes from the encryption,
and the steganography only adds a layer of not-obviousness. Nothing here claims
to be undetectable when the opponent knows the algorithm and the fill is high.

## Measured properties

These numbers come from code that runs in CI, not from estimates.

| Property | Value | Where |
|---|---|---|
| Per-channel change | at most ±1 | `internal/stego/steganalysis_test.go` |
| PSNR at 10% fill | **62.7 dB** | same |
| PSNR at full capacity | **55.8 dB** | same |
| Pairs-of-values chi-square, clean cover | p = 0.0000 (clean) | `detector/steganalysis.py` |
| Same attack, LSB replacement | p = 1.0000 (flagged) | `detector/steganalysis.py` |

On a cover whose values are spread widely enough for the pairs-of-values attack to
have any power, the attack flags sequential LSB replacement outright and does not
flag a Wisanggeni carrier at any fill from 2% to 40%:

| Fill | chi-square p | RS (reg − sing) |
|---|---|---|
| clean cover | 0.0000 | 0.0238 |
| LSB replacement | 1.0000 (flagged) | 0.0006 |
| Wisanggeni 2% | 0.0000 | 0.0232 |
| Wisanggeni 5% | 0.0000 | 0.0218 |
| Wisanggeni 10% | 0.0000 | 0.0246 |
| Wisanggeni 20% | 0.0000 | 0.0191 |
| Wisanggeni 40% | 0.0000 | 0.0201 |

The chi-square result is a negative result, and it only means something because
the same code flags LSB replacement on the same cover. It has a limit worth
knowing: the attack needs a cover whose histogram pairs are sparsely populated.
On a structured or peaky cover the statistic sits below its own null distribution
even for the untouched image, so a high p-value there is evidence of nothing. The
samples use a high-entropy cover for that reason.

These two classic blind attacks are what the detector job checks. PRD item 22 is
explicit that a detector trained on Wisanggeni output would do better, and that
figure is not measured here. Treat the table as "two standard attacks see
nothing", not as "nothing can see it".

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Usage or input error, including an existing output without `--force`. |
| 2 | The payload does not fit the carrier's capacity. |
| 3 | Wrong passphrase, no data, or corrupt data — deliberately the same code and message for all three. |
| 4 | Post-write verification failed. |
| 5 | The carrier or the fill ratio is not suitable, and `--force` was not given. |

The three failures in code 3 share one error message on purpose. Distinguishing
them would tell an attacker whether they guessed the passphrase, found a
stego image, or found a broken one.

## How it works

```
input files → tar → zstd (if smaller) → XChaCha20-Poly1305 → STC embed → PNG
                 passphrase → Argon2id → HKDF ─┘                      │
                                                           verify ←──┘
```

1. **Pack.** Inputs become a deterministic tar, then zstd if that is smaller. The
   original file names are inside the encrypted archive.
2. **Derive.** The passphrase is normalised with NFKC, run through Argon2id
   (`t=3`, 64 MiB, `p=4`), and split with HKDF-SHA256 into an encryption key and
   a position seed.
3. **Encrypt.** The archive is sealed with XChaCha20-Poly1305. A 16-byte random
   salt and the nonces are the only plaintext structure; the header — version,
   flags, body length — is itself encrypted.
4. **Embed.** A cost map, computed from the original cover, estimates how
   expensive each channel is to change. The payload is written with syndrome
   trellis coding at one bit per channel, minimising total cost, so changes land
   in textured areas. A keyed permutation decides the channel order, so an
   opponent who knows the cost function still does not know which channels carry
   which bit.
5. **Verify.** The output file is decoded again and the blob compared, so a
   broken PNG writer or a bad embed aborts before the file is renamed into place.

`docs/FORMAT.md` is the frozen writer/reader contract: bit order, blob layout,
key derivation vectors, salt positions, capacities, cost map and the STC
parameters, each with the test vector that pins it. After the `v1.0.0` tag,
changing any of it means a new format version, because the blob has no plaintext
header a reader could use to detect the difference.

## Building and testing

```sh
make build      # bin/wisanggeni
make test       # go test -race ./...
make lint       # golangci-lint
make all        # lint, test, build
```

The end-to-end tests build the binary and drive it as a subprocess, so they cover
the real exit codes and `--passphrase-stdin`. The archive readers and the
container reader have fuzz targets:

```sh
go test ./internal/archive -run XXX -fuzz FuzzUnpack -fuzzminimizetime 3s
go test ./internal/stego   -run XXX -fuzz FuzzExtractSTC
go test ./internal/crypto  -run XXX -fuzz FuzzOpen
```

`detector/` holds the steganalysis tooling. It never ships inside the binary and
is stdlib-only:

```sh
python3 detector/steganalysis.py --make-samples /tmp/g3 --self-check \
    --binary ./bin/wisanggeni
python3 detector/stc_ref.py --check internal/stego/testdata/stc_vectors.json
```

## License

See [LICENSE](LICENSE).
