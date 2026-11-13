#!/usr/bin/env python3
"""Reference solver for one STC block (PRD §10, docs/FORMAT.md §10).

A block covers STCWidth = 16 pool channels and carries one message byte through
the syndrome of its LSB vector:

    H * (cover XOR mask) = message        =>   H * mask = message XOR H * cover

This script brute-forces all 2^16 masks and keeps the cheapest one, breaking
ties with the same rule as the Go implementation: prefer no flip at the earliest
column. It is deliberately independent of the Go code — the only shared input is
the frozen parity matrix, which TestParityMatrixGolden pins.

Usage:
    python3 detector/stc_ref.py               print the vectors JSON
    python3 detector/stc_ref.py --write PATH  write the vectors JSON
    python3 detector/stc_ref.py --check PATH  regenerate and compare
"""

import argparse
import json
import random
import sys

# Columns of H as 8-bit syndrome masks, from ChaCha8(SHA-256("wisanggeni/v1/stc")).
PARITY_COLUMNS = [0x84, 0x3F, 0x8F, 0x45, 0x97, 0x5E, 0x3E, 0x0E,
                  0x0E, 0x20, 0x00, 0xA7, 0x4E, 0x7E, 0x25, 0x0A]

WIDTH = 16


def syndrome(mask):
    """XOR of the columns selected by mask."""
    value = 0
    for i in range(WIDTH):
        if mask >> i & 1:
            value ^= PARITY_COLUMNS[i]
    return value


def mask_cost(mask, costs):
    return sum(costs[i] for i in range(WIDTH) if mask >> i & 1)


def decisions(mask):
    """Per-column flip decisions, earliest column first."""
    return tuple(mask >> i & 1 for i in range(WIDTH))


def solve(target, costs):
    """Cheapest mask with H*mask == target; ties prefer zeros at early columns."""
    best_mask = None
    best_key = None
    for mask in range(1 << WIDTH):
        if syndrome(mask) != target:
            continue
        key = (mask_cost(mask, costs), decisions(mask))
        if best_key is None or key < best_key:
            best_key = key
            best_mask = mask
    if best_mask is None:  # unreachable with a full-rank matrix
        raise RuntimeError(f"no mask for syndrome {target:#x}")
    return best_mask, best_key[0]


def build_vectors(seed=20241113, cases=8):
    rng = random.Random(seed)
    out = []
    for index in range(cases):
        cover = [rng.randint(0, 1) for _ in range(WIDTH)]
        costs = [round(rng.uniform(0.001, 5.0), 6) for _ in range(WIDTH)]
        if index == 0:
            costs[3] = 0.0
        message = rng.randint(0, 255)

        cover_mask = sum(bit << i for i, bit in enumerate(cover))
        target = message ^ syndrome(cover_mask)
        mask, cost = solve(target, costs)

        assert syndrome(mask) == target
        out.append({
            "cover_bits": cover,
            "costs": costs,
            "message": message,
            "syndrome_target": target,
            "expected_mask": mask,
            "expected_cost": cost,
        })
    return {"columns": PARITY_COLUMNS, "cases": out}


def serialise(vectors):
    return json.dumps(vectors, indent=2, sort_keys=False) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", metavar="PATH")
    parser.add_argument("--check", metavar="PATH")
    args = parser.parse_args()

    text = serialise(build_vectors())

    if args.write:
        with open(args.write, "w", encoding="utf-8") as handle:
            handle.write(text)
        print(f"wrote {args.write}", file=sys.stderr)
        return

    if args.check:
        with open(args.check, encoding="utf-8") as handle:
            expected = handle.read()
        if expected != text:
            print(f"{args.check} is stale; rerun with --write", file=sys.stderr)
            sys.exit(1)
        print(f"{args.check} matches", file=sys.stderr)
        return

    sys.stdout.write(text)


if __name__ == "__main__":
    main()
