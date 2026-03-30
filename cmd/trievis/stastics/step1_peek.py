#!/usr/bin/env python3
"""
step1_peek.py
-------------
Quick-peek at both JSON files so you know their structure before running
the heavier analysis scripts.

Usage:
    python step1_peek.py [--result /path/to/trie-result.json] \
                         [--stats  /path/to/trie-stats.bin]
"""

import argparse
import gzip
import json
import os
import sys

DEFAULT_RESULT = os.path.expanduser("~/trie-result.json")
DEFAULT_STATS  = os.path.expanduser("~/trie-stats.bin")

PEEK_BYTES = 4096  # how many bytes to show from trie-stats.bin (after decompression)


def human_size(path):
    size = os.path.getsize(path)
    for unit in ("B", "KB", "MB", "GB"):
        if size < 1024:
            return f"{size:.1f} {unit}"
        size /= 1024
    return f"{size:.1f} TB"


def is_gzip(path):
    with open(path, "rb") as f:
        return f.read(2) == b'\x1f\x8b'


def open_file(path):
    if is_gzip(path):
        return gzip.open(path, "rb")
    return open(path, "rb")


def peek_result(path):
    print(f"\n{'='*60}")
    gzip_tag = " [gzip]" if is_gzip(path) else ""
    print(f"  trie-result.json{gzip_tag}  ({human_size(path)})")
    print(f"{'='*60}")
    with open_file(path) as f:
        data = json.loads(f.read())
    print(json.dumps(data, indent=2))


def peek_stats(path):
    print(f"\n{'='*60}")
    gzip_tag = " [gzip]" if is_gzip(path) else ""
    print(f"  trie-stats.bin{gzip_tag}  ({human_size(path)})  — first {PEEK_BYTES} decompressed bytes")
    print(f"{'='*60}")
    with open_file(path) as f:
        head = f.read(PEEK_BYTES).decode("utf-8", errors="replace")
    print(head)

    # Try to find the first complete JSON object / array element
    print(f"\n{'='*60}")
    print("  Attempting to decode first complete record …")
    print(f"{'='*60}")
    import re
    # Heuristic: find the first '{...}' block
    m = re.search(r'\{[^{}]+\}', head)
    if m:
        try:
            rec = json.loads(m.group(0))
            print("First record keys:", list(rec.keys()))
            print(json.dumps(rec, indent=2))
        except json.JSONDecodeError as e:
            print(f"Could not parse first record: {e}")
    else:
        print("No complete object found in the first peek window.")


def main():
    parser = argparse.ArgumentParser(description="Peek at trie JSON files")
    parser.add_argument("--result", default=DEFAULT_RESULT)
    parser.add_argument("--stats",  default=DEFAULT_STATS)
    args = parser.parse_args()

    for path, label in [(args.result, "trie-result.json"),
                        (args.stats,  "trie-stats.bin")]:
        if not os.path.exists(path):
            print(f"ERROR: {label} not found at {path}", file=sys.stderr)
            print(f"       Pass --result / --stats to specify the correct path.",
                  file=sys.stderr)
            sys.exit(1)

    peek_result(args.result)
    peek_stats(args.stats)


if __name__ == "__main__":
    main()

