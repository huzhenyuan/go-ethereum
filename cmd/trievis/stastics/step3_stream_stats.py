#!/usr/bin/env python3
"""
step3_stream_stats.py
---------------------
Parse the binary dump file produced by `geth db inspect-trie --dump-path`.

The file is NOT JSON. It is a flat sequence of fixed-size binary records
written by trie/inspect.go:

    Record layout  (352 bytes total, little-endian):
    ┌──────────────────────────────────────────────────────┐
    │  32 bytes  owner hash                                │
    │            zero hash  → account trie sentinel record │
    │            non-zero   → storage trie for that owner  │
    ├──────────────────────────────────────────────────────┤
    │  16 levels × 20 bytes each:                          │
    │    4 bytes  short-node count  (uint32 LE)            │
    │    4 bytes  full-node  count  (uint32 LE)            │
    │    4 bytes  value-node count  (uint32 LE)            │
    │    8 bytes  total byte size   (uint64 LE)            │
    └──────────────────────────────────────────────────────┘

For each record we reconstruct per-depth counts of each node type and
aggregate them into counters that are written to CSV for step4_visualize.py.

Usage:
    python step3_stream_stats.py [--stats /path/to/trie-stats.bin]
                                 [--out-dir ./]

Output files (in --out-dir):
    account_depth_short.csv   depth, count   (short/extension nodes)
    account_depth_full.csv    depth, count   (full/branch nodes)
    account_depth_value.csv   depth, count   (value/leaf nodes)
    account_depth_total.csv   depth, count   (all node types combined)
    storage_depth_short.csv   …
    storage_depth_full.csv    …
    storage_depth_value.csv   …
    storage_depth_total.csv   …
    storage_max_depth.csv     max_depth, trie_count  (distribution of per-trie max depths)
    summary.txt               human-readable totals
"""

import argparse
import csv
import os
import struct
import sys
import time
from collections import Counter

# ── Binary record constants (must match trie/inspect.go) ──────────────────
TRIE_STAT_LEVELS   = 16            # trieStatLevels = 16
BYTES_PER_LEVEL    = 4 + 4 + 4 + 8  # short(u32) + full(u32) + value(u32) + size(u64)
RECORD_SIZE        = 32 + TRIE_STAT_LEVELS * BYTES_PER_LEVEL  # = 352

ZERO_HASH = b'\x00' * 32

# struct format for one record: 32s + 16*(I I I Q)
#   I = uint32 LE,  Q = uint64 LE
LEVEL_FMT  = '<' + 'IIQ' * TRIE_STAT_LEVELS   # wrong: size is 8 bytes not 4 before Q
# Correct layout per level: uint32 short, uint32 full, uint32 value, uint64 size
# Pack one full record at a time
RECORD_FMT = '<' + ('III Q' * TRIE_STAT_LEVELS).replace(' ', '')

DEFAULT_STATS   = os.path.expanduser("~/trie-stats.bin")
DEFAULT_OUT_DIR = "."
REPORT_EVERY    = 500_000


def parse_record(raw: bytes):
    """
    Returns (is_account, levels) where levels is a list of 16 dicts:
        {'short': int, 'full': int, 'value': int, 'size': int}
    """
    assert len(raw) == RECORD_SIZE, f"bad record size {len(raw)}"
    owner = raw[:32]
    is_account = (owner == ZERO_HASH)

    levels = []
    off = 32
    for _ in range(TRIE_STAT_LEVELS):
        short = struct.unpack_from('<I', raw, off)[0];     off += 4
        full  = struct.unpack_from('<I', raw, off)[0];     off += 4
        value = struct.unpack_from('<I', raw, off)[0];     off += 4
        size  = struct.unpack_from('<Q', raw, off)[0];     off += 8
        levels.append({'short': short, 'full': full, 'value': value, 'size': size})
    return is_account, levels


def write_depth_csv(path, counter):
    rows = sorted(counter.items())
    with open(path, 'w', newline='') as f:
        w = csv.writer(f)
        w.writerow(['depth', 'count'])
        w.writerows(rows)
    print(f"  Saved {path}  ({len(rows)} depth levels)")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--stats',   default=DEFAULT_STATS,
                        help='Path to the binary dump file (trie-stats.bin / trie-dump.bin)')
    parser.add_argument('--out-dir', default=DEFAULT_OUT_DIR)
    args = parser.parse_args()

    if not os.path.exists(args.stats):
        print(f"ERROR: {args.stats} not found", file=sys.stderr)
        sys.exit(1)

    file_size = os.path.getsize(args.stats)
    if file_size % RECORD_SIZE != 0:
        print(f"WARNING: file size {file_size} is not a multiple of {RECORD_SIZE}. "
              f"File may be truncated or use a different record size.", file=sys.stderr)
    total_records = file_size // RECORD_SIZE
    print(f"File size : {file_size / 1024 / 1024:.1f} MB")
    print(f"Record sz : {RECORD_SIZE} bytes")
    print(f"Expected  : {total_records:,} records")
    print()

    os.makedirs(args.out_dir, exist_ok=True)

    # Per-depth counters indexed as [depth] = count
    acc = {'short': Counter(), 'full': Counter(), 'value': Counter(), 'size': Counter()}
    sto = {'short': Counter(), 'full': Counter(), 'value': Counter(), 'size': Counter()}
    storage_max_depth: Counter = Counter()   # max_depth -> number of storage tries

    total = 0
    account_record = None
    t0 = time.time()

    with open(args.stats, 'rb') as f:
        while True:
            raw = f.read(RECORD_SIZE)
            if not raw:
                break
            if len(raw) < RECORD_SIZE:
                print(f"WARNING: trailing {len(raw)} bytes ignored (incomplete record)")
                break

            is_account, levels = parse_record(raw)
            target = acc if is_account else sto

            max_depth = 0
            for depth, lv in enumerate(levels):
                if lv['short'] or lv['full'] or lv['value']:
                    max_depth = depth
                target['short'][depth] += lv['short']
                target['full'][depth]  += lv['full']
                target['value'][depth] += lv['value']
                target['size'][depth]  += lv['size']

            if is_account:
                account_record = levels
            else:
                storage_max_depth[max_depth] += 1

            total += 1
            if total % REPORT_EVERY == 0:
                elapsed = time.time() - t0
                rate = total / elapsed
                pct = total / total_records * 100 if total_records else 0
                print(f"  {total:>10,} / {total_records:,} ({pct:.1f}%)  "
                      f"{elapsed:.0f}s  {rate/1000:.1f}k rec/s")

    elapsed = time.time() - t0
    print(f"\nDone. {total:,} records in {elapsed:.1f}s ({total/elapsed/1000:.1f}k rec/s)")

    storage_tries = total - (1 if account_record is not None else 0)
    print(f"  Account trie : 1 record")
    print(f"  Storage tries: {storage_tries:,} records")

    # ── Write depth CSVs ────────────────────────────────────────────────────
    print()
    for prefix, counters in [('account', acc), ('storage', sto)]:
        for kind in ('short', 'full', 'value'):
            write_depth_csv(
                os.path.join(args.out_dir, f'{prefix}_depth_{kind}.csv'),
                counters[kind])
        # total = short + full + value per depth
        total_counter: Counter = Counter()
        for kind in ('short', 'full', 'value'):
            for depth, cnt in counters[kind].items():
                total_counter[depth] += cnt
        write_depth_csv(
            os.path.join(args.out_dir, f'{prefix}_depth_total.csv'),
            total_counter)

    write_depth_csv(
        os.path.join(args.out_dir, 'storage_max_depth.csv'),
        storage_max_depth)

    # ── Write text summary ──────────────────────────────────────────────────
    def total_nodes(counters):
        return sum(counters['short'].values()) + \
               sum(counters['full'].values()) + \
               sum(counters['value'].values())

    def total_bytes(counters):
        return sum(counters['size'].values())

    summary_path = os.path.join(args.out_dir, 'summary.txt')
    with open(summary_path, 'w') as f:
        for label, counters in [('Account Trie', acc), ('Storage Tries (aggregate)', sto)]:
            tn = total_nodes(counters)
            tb = total_bytes(counters)
            f.write(f"=== {label} ===\n")
            f.write(f"  Total nodes : {tn:>15,}\n")
            f.write(f"  Total size  : {tb:>15,}  ({tb/1024/1024:.1f} MB)\n")
            f.write(f"  Short nodes : {sum(counters['short'].values()):>15,}\n")
            f.write(f"  Full  nodes : {sum(counters['full'].values()):>15,}\n")
            f.write(f"  Value nodes : {sum(counters['value'].values()):>15,}\n\n")
        f.write(f"Storage tries count: {storage_tries:,}\n")
    print(f"  Saved {summary_path}")

    with open(summary_path) as f:
        print(f.read())

    print(f"Next step:  python step4_visualize.py --in-dir {args.out_dir}")


if __name__ == '__main__':
    main()

