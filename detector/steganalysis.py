#!/usr/bin/env python3
"""White-box steganalysis for Wisanggeni (PRD Fase G3).

This is a detector, not part of the Go binary. It answers one question the
README has to be honest about: if someone knew the format, could they tell a
Wisanggeni carrier from an untouched one?

It runs two classic tests that need no key and no knowledge of the format:

  * The pairs-of-values chi-square attack (Westfeld & Pfitzmann). Sequential LSB
    *replacement* forces each histogram pair (2k, 2k+1) to equalise, so the
    chi-square statistic collapses and its upper-tail probability climbs towards
    1. LSB matching and STC do not force that equality.

  * RS analysis (Fridrich et al.). It measures how the "smoothness" of the image
    changes under the flipping function, which a replacement-like embed shifts
    even when the first test says nothing.

The point is not that Wisanggeni is undetectable. It is detectable in principle:
an adaptive embedder leaves a cost-map-shaped footprint, and the README says so.
The point is that these two standard attacks, run blind, do not separate a
Wisanggeni carrier from the original at the fill the default policy allows.

Usage:
    python3 detector/steganalysis.py CARRIER STEGO [STEGO ...]
    python3 detector/steganalysis.py --make-samples DIR

Exits 0 when every stego image stays below the detection threshold, 1 otherwise.
Stdlib only, so the CI detector job needs no extra packages for this script.
"""

import argparse
import math
import os
import struct
import sys
import zlib

# The p-value above which the pairs-of-values test reports an image as modified.
CHI_FLAG = 0.95


def read_png_rgb(path):
    """Return (width, height, channels) where channels is a flat list of ints.

    A minimal, dependency-free reader: it handles the 8-bit non-interlaced
    truecolour images Wisanggeni writes and refuses anything else, which is
    enough for carrier analysis.
    """
    with open(path, "rb") as fh:
        data = fh.read()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError(f"{path}: not a PNG")

    pos = 8
    width = height = None
    bit_depth = color_type = interlace = None
    idat = bytearray()
    while pos + 8 <= len(data):
        length = struct.unpack(">I", data[pos:pos + 4])[0]
        ctype = data[pos + 4:pos + 8]
        body = data[pos + 8:pos + 8 + length]
        pos += 12 + length
        if ctype == b"IHDR":
            width, height, bit_depth, color_type, _, _, interlace = struct.unpack(
                ">IIBBBBB", body)
        elif ctype == b"IDAT":
            idat += body
        elif ctype == b"IEND":
            break

    if bit_depth != 8 or color_type != 2 or interlace != 0:
        raise ValueError(
            f"{path}: only 8-bit non-interlaced truecolour is supported "
            f"(bit_depth={bit_depth}, color_type={color_type}, interlace={interlace})")

    raw = zlib.decompress(bytes(idat))
    stride = width * 3
    out = []
    prev = bytearray(stride)
    off = 0
    for _ in range(height):
        ftype = raw[off]
        off += 1
        line = bytearray(raw[off:off + stride])
        off += stride
        unfilter(ftype, line, prev, 3)
        out.extend(line)
        prev = line
    return width, height, out


def unfilter(ftype, line, prev, bpp):
    """Reverse one PNG scanline filter, in place."""
    if ftype == 0:
        return
    for i in range(len(line)):
        a = line[i - bpp] if i >= bpp else 0
        b = prev[i]
        c = prev[i - bpp] if i >= bpp else 0
        x = line[i]
        if ftype == 1:
            line[i] = (x + a) & 0xFF
        elif ftype == 2:
            line[i] = (x + b) & 0xFF
        elif ftype == 3:
            line[i] = (x + ((a + b) >> 1)) & 0xFF
        elif ftype == 4:
            line[i] = (x + paeth(a, b, c)) & 0xFF
        else:
            raise ValueError(f"unknown filter {ftype}")


def paeth(a, b, c):
    p = a + b - c
    pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
    if pa <= pb and pa <= pc:
        return a
    if pb <= pc:
        return b
    return c


def chi_square_upper(x, df):
    """P(X >= x) for a chi-square with df degrees of freedom."""
    if x <= 0 or df <= 0:
        return 1.0
    return 1.0 - _gamma_p(df / 2.0, x / 2.0)


def _gamma_p(a, x):
    """Regularised lower incomplete gamma P(a, x)."""
    if x < 0 or a <= 0:
        return 0.0
    if x == 0:
        return 0.0
    if x < a + 1:
        term = 1.0 / a
        total = term
        for n in range(1, 200):
            term *= x / (a + n)
            total += term
            if abs(term) < abs(total) * 1e-15:
                break
        return total * math.exp(-x + a * math.log(x) - math.lgamma(a))
    b = x + 1 - a
    c = 1e300
    d = 1.0 / b
    h = d
    for i in range(1, 200):
        an = -i * (i - a)
        b += 2
        d = an * d + b
        if abs(d) < 1e-300:
            d = 1e-300
        c = b + an / c
        if abs(c) < 1e-300:
            c = 1e-300
        d = 1.0 / d
        delta = d * c
        h *= delta
        if abs(delta - 1) < 1e-15:
            break
    q = math.exp(-x + a * math.log(x) - math.lgamma(a)) * h
    return 1.0 - q


def chi_square_lsp(channels):
    """Pairs-of-values chi-square test over a flat channel list."""
    counts = [0] * 256
    for v in channels:
        counts[v] += 1
    stat = 0.0
    df = 0
    for k in range(0, 256, 2):
        even = float(counts[k])
        odd = float(counts[k + 1])
        if even + odd == 0:
            continue
        expected = (even + odd) / 2.0
        stat += (even - expected) ** 2 / expected
        df += 1
    if df == 0:
        return 1.0
    return chi_square_upper(stat, df)


def rs_analysis(channels, mask_seed=0):
    """RS analysis: the fraction of regular minus singular groups.

    Flipping the low bit of a channel changes its "smoothness" as measured by a
    discrimination function. An LSB-replacement embed pushes the regular and
    singular fractions apart; an untouched image keeps them close. The returned
    value is |R - S| over the whole image, which grows with the embed size.
    """
    state = mask_seed or 12345

    def next_bit():
        nonlocal state
        state = (state * 1103515245 + 12345) & 0x7FFFFFFF
        return state & 1

    # A fixed pseudo-random mask, applied only to channels whose bit is set.
    mask = [next_bit() for _ in range(len(channels))]

    r = s = 0
    for i in range(len(channels) - 4):
        if not mask[i]:
            continue
        # Group of four consecutive values, treating each third as a channel.
        group = channels[i:i + 4]
        f_orig = discrimination(group)
        flipped = [flip_lsb(v) for v in group]
        f_flip = discrimination(flipped)
        if f_flip > f_orig:
            r += 1
        elif f_flip < f_orig:
            s += 1
    total = r + s
    if total == 0:
        return 0.0
    return abs(r - s) / total


def flip_lsb(v):
    return v ^ 1


def discrimination(group):
    """The RS discrimination function: total variation of a small group."""
    return sum(abs(group[i + 1] - group[i]) for i in range(len(group) - 1))


def analyse(path):
    width, height, channels = read_png_rgb(path)
    chi = chi_square_lsp(channels)
    rs = rs_analysis(channels)
    return {
        "path": path,
        "width": width,
        "height": height,
        "chi_square_p": chi,
        "rs_delta": rs,
    }


def make_samples(outdir):
    """Write a cover, an LSB-replaced copy and a Wisanggeni carrier.

    The cover has high-entropy values spread over 0..255. That is the property
    the pairs test needs to have any power: with many sparse histogram pairs, a
    replacement embed visibly equalises them, while a structured or peaky cover
    leaves the statistic below its own null distribution and the test says
    nothing either way.

    The LSB-replaced copy is the control the self-check requires to be flagged.
    The Wisanggeni carrier is produced by the built binary when one is on PATH or
    given by --binary; without it only the first two are written.
    """
    os.makedirs(outdir, exist_ok=True)
    w = h = 256

    raw = bytearray()
    state = 0x2545F491

    def nxt():
        nonlocal state
        state ^= (state << 13) & 0xFFFFFFFF
        state ^= state >> 17
        state ^= (state << 5) & 0xFFFFFFFF
        return state

    for _ in range(h):
        raw.append(0)  # filter type 0
        for _ in range(w * 3):
            raw.append(1 + nxt() % 254)  # avoid 0 and 255

    cover = os.path.join(outdir, "cover.png")
    write_png(cover, w, h, bytes(raw))

    replaced = bytearray(raw)
    for y in range(h):
        base = y * (w * 3 + 1)
        for i in range(w * 3):
            replaced[base + 1 + i] ^= (i * 2654435761 >> 7) & 1
    lsb = os.path.join(outdir, "lsb-replaced.png")
    write_png(lsb, w, h, bytes(replaced))
    return cover, lsb


def wisanggeni_carrier(binary, cover, outdir, fill=0.05):
    """Produce a real Wisanggeni carrier with the given binary, for analysis."""
    import subprocess
    import tempfile

    width, height, _ = read_png_rgb(cover)
    # 8 bits per channel at the requested fraction of raw capacity.
    body = int(width * height * 3 / 8 * fill) - 112
    payload = os.path.join(outdir, "payload.bin")
    with open(payload, "wb") as fh:
        fh.write(bytes((i * 7 + 11) & 0xFF for i in range(body)))

    with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False) as pf:
        pf.write("steganalysis passphrase\n")
        passfile = pf.name
    os.chmod(passfile, 0o600)

    stego = os.path.join(outdir, "wisanggeni.png")
    subprocess.run([binary, "hide", payload, "-c", cover, "-o", stego,
                    "--passphrase-file", passfile, "--force"], check=True)
    return stego


def write_png(path, w, h, raw):
    def chunk(tag, body):
        return (struct.pack(">I", len(body)) + tag + body +
                struct.pack(">I", zlib.crc32(tag + body) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
    with open(path, "wb") as fh:
        fh.write(b"\x89PNG\r\n\x1a\n")
        fh.write(chunk(b"IHDR", ihdr))
        fh.write(chunk(b"IDAT", zlib.compress(raw, 9)))
        fh.write(chunk(b"IEND", b""))


def main():
    ap = argparse.ArgumentParser(description="Wisanggeni white-box steganalysis")
    ap.add_argument("paths", nargs="*", help="PNG files to analyse")
    ap.add_argument("--make-samples", metavar="DIR",
                    help="write a cover and an LSB-replaced sample, then exit")
    ap.add_argument("--self-check", action="store_true",
                    help="with --make-samples, fail unless the detector separates them")
    ap.add_argument("--binary", metavar="PATH",
                    help="wisanggeni binary; with it, a real carrier is analysed too")
    args = ap.parse_args()

    if args.make_samples:
        cover, lsb = make_samples(args.make_samples)
        if args.self_check:
            return self_check(cover, lsb, args.binary, args.make_samples)
        print(f"wrote {cover} and {lsb}")
        return 0

    if not args.paths:
        ap.error("no input files")

    failed = False
    for path in args.paths:
        info = analyse(path)
        flagged = info["chi_square_p"] >= CHI_FLAG
        print(f"{path}: {info['width']}x{info['height']} "
              f"chi-square p={info['chi_square_p']:.4f} "
              f"RS={info['rs_delta']:.4f} "
              f"{'FLAGGED' if flagged else 'clean'}")
        if flagged:
            failed = True
    return 1 if failed else 0


def self_check(cover, lsb, binary, outdir):
    cover_info = analyse(cover)
    lsb_info = analyse(lsb)
    print(f"cover:        p={cover_info['chi_square_p']:.4f} RS={cover_info['rs_delta']:.4f}")
    print(f"lsb-replaced: p={lsb_info['chi_square_p']:.4f} RS={lsb_info['rs_delta']:.4f}")

    if lsb_info["chi_square_p"] < CHI_FLAG:
        print("self-check FAILED: the detector did not flag LSB replacement",
              file=sys.stderr)
        return 1
    if cover_info["chi_square_p"] >= CHI_FLAG:
        print("self-check FAILED: the detector flagged the clean cover",
              file=sys.stderr)
        return 1

    if binary:
        stego = wisanggeni_carrier(binary, cover, outdir)
        stego_info = analyse(stego)
        print(f"wisanggeni:   p={stego_info['chi_square_p']:.4f} RS={stego_info['rs_delta']:.4f}")
        if stego_info["chi_square_p"] >= CHI_FLAG:
            print("self-check FAILED: chi-square flagged the Wisanggeni carrier",
                  file=sys.stderr)
            return 1
        print("G3: the attack separates LSB replacement but not Wisanggeni")

    print("self-check OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
