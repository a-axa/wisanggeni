# Test report: recovering a Wisanggeni image without the passphrase

Date: 13 November 2026
Tester: Muse, an AI agent, acting as a red team at the owner's request
Target: `result.png`, a real Wisanggeni v1 output
Carrier: the README cover, Wisanggeni v9, 2096x1184 RGB PNG
Tool version tested: built from this repo at commit `c7300f6`, Go 1.23

This is a test report, not a security audit. It records one real attempt
to recover the contents of a real output file without the passphrase, using
the built tool, public forensic tools, and a code audit. The passphrase is
not included in this report. It was supplied by the owner at the end of the
test, used once through stdin, and discarded.

## Verdict

The payload's presence was confirmed, its contents were not recovered
without the passphrase. With the passphrase, `reveal` succeeded on the
first try. Confidentiality held; not-obviousness did not hold against an
opponent who has the original cover image.

## What was hidden

The owner hid one file, `halo.txt`, 11 bytes, containing `halo -123`
followed by two newlines. This was only learned after the owner supplied
the passphrase. Everything below that, up to the reveal, was found without
it.

## What the forensics confirmed

The uploaded file arrived intact as a PNG, 2096x1184, 3,166,837 bytes,
same dimensions as the cover, no JPEG conversion, no resize. Compared
with the original cover:

- The salt positions `S` are public and need no key. 68 of the 128 salt
  positions differ from the cover, which matches a freshly written random
  16-byte salt (about 64 expected by chance).
- 615 further channels differ, each by exactly plus or minus 1, spread
  across the whole image. Their median embedding cost is 0.0630 against
  0.0722 for all channels, and their mean cost is 0.0644 against 0.0932,
  so the changes sit in textured, low-cost areas, as STC with the HILL
  cost map should.
- The cover's `iTXt` chunk is gone, which matches the chunk-preserving
  writer discarding text chunks, and the IDAT data is re-chunked.
- From the number of changed channels, the encrypted blob was estimated
  at roughly 200 to 300 bytes, consistent with a short compressed text
  file. The estimate was a size estimate only, not a read of the content.

Uploading the PNG through chat did not destroy the data in this case,
because the file stayed a PNG at the same dimensions. Sending it as a
photo through a messenger, which re-encodes to JPEG, would destroy the
LSB payload. The safe way to send a v1 file is as a document.

## Attacks tried

| # | Attack | Result |
|---|---|---|
| 1 | Dictionary, 108 likely passphrases (project words, names, common passwords, owner mottos), real `reveal` each time | All failed, exit code 3 |
| 2 | Expanded dictionary, 2148 candidates (bases times case times suffixes, phrases, common passwords, dates), real `reveal` each time | 700 tried, all failed, then stopped early on the owner's redirect to other tools |
| 3 | `strings` and a magic-byte scan of the raw file (zstd, gzip, zip, tar `ustar`) | Nothing: no appended data, no embedded archive magic |
| 4 | zsteg-style LSB extraction: LSB of R, G, B and RGB together, MSB-first and LSB-first, plus bit plane 1 | Only random short fragments, no readable string of 12 characters or more, no archive magic |
| 5 | steghide | Not applicable: it does not support PNG |
| 6 | The repo's own detector, `detector/steganalysis.py` (pairs-of-values chi-square plus RS analysis) | Cover and stego score identically: chi-square p = 0.0000, RS = 0.0250, both reported clean |
| 7 | Code audit of `internal/crypto/container.go`, `internal/stego/stc.go`, `internal/stego/shuffle.go`, and the reveal path | No flaw found: nonces from `crypto/rand`, header AEAD bound to the salt, body AEAD bound to the header ciphertext, one generic decryption error, no key or seed logging, no hardcoded passphrase |

## Why guessing is slow

One `reveal` attempt costs about 0.6 seconds on the test VM, because
Argon2id runs with `t=3`, 64 MiB, `p=4`. That is about 1.5 tries per
second on that machine. A targeted dictionary of a few thousand entries
takes tens of minutes; an exhaustive search of a strong passphrase is
not feasible at that rate. The STC pool order is keyed by the passphrase
through `pos_seed`, so there is no shortcut that reads the syndrome bits
in the right order without it, and the blob has no magic bytes or
plaintext length to verify a guess against except the AEAD tag, which
only opens with the right key.

## Reveal with the passphrase

With the passphrase supplied by the owner, `reveal` succeeded on the
first attempt:

```
revealed 1 entries (11 bytes) into <output dir>
halo.txt
```

## Limitations

- One sample, one carrier, one small payload.
- The dictionaries were targeted, not exhaustive, and the expanded one
  was stopped at 700 of 2148 entries.
- No GPU or ASIC attempt, no distributed attempt, and no ML detector
  trained on Wisanggeni output.
- The test says nothing about a weak passphrase: a passphrase from the
  tried lists would have fallen on the first matching attempt.

## Conclusion

For this sample, Wisanggeni v1 did what its threat model promises: the
encryption protected the contents, and the steganography did not protect
the existence of the payload from an opponent who has the original cover.
An attacker with only the output image, no passphrase, and no original
could confirm nothing except what generic tools also confirm, which is
nothing at all.
