#!/usr/bin/env python3
"""
step2_parse_result.py
---------------------
Parse and pretty-print the summary data in trie-result.json.

trie-result.json is small (~58 KB) so we load it fully into memory.
The script prints a human-readable table and saves a CSV summary.

Usage:
    python step2_parse_result.py [--result /path/to/trie-result.json]
                                 [--out    ./result_summary.csv]
"""

import argparse
import json
import os
import sys
import csv

DEFAULT_RESULT = os.path.expanduser("~/trie-result.json")
DEFAULT_OUT    = "result_summary.csv"


def flatten(obj, prefix="", sep="."):
    """Recursively flatten a nested dict/list into key-value pairs."""
    rows = {}
    if isinstance(obj, dict):
        for k, v in obj.items():
            rows.update(flatten(v, f"{prefix}{sep}{k}" if prefix else k, sep))
    elif isinstance(obj, list):
        for i, v in enumerate(obj):
            rows.update(flatten(v, f"{prefix}[{i}]", sep))
    else:
        rows[prefix] = obj
    return rows


def print_section(title, data):
    print(f"\n{'─'*60}")
    print(f"  {title}")
    print(f"{'─'*60}")
    if isinstance(data, dict):
        flat = flatten(data)
        col_w = max(len(k) for k in flat) + 2
        for k, v in flat.items():
            # Format large integers with commas
            if isinstance(v, int) and v > 9999:
                v = f"{v:,}"
            elif isinstance(v, float):
                v = f"{v:.4f}"
            print(f"  {k:<{col_w}} {v}")
    else:
        print(f"  {data}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--result", default=DEFAULT_RESULT)
    parser.add_argument("--out",    default=DEFAULT_OUT)
    args = parser.parse_args()

    if not os.path.exists(args.result):
        print(f"ERROR: {args.result} not found", file=sys.stderr)
        sys.exit(1)

    with open(args.result) as f:
        data = json.load(f)

    print(f"\n{'='*60}")
    print(f"  trie-result.json  Summary")
    print(f"{'='*60}")

    # ── Try to detect common geth inspect-trie output shapes ──────────────
    # Shape A: top-level keys are trie names
    # Shape B: { "account": {...}, "storage": {...} }
    # Shape C: flat dict of stat name -> value

    if isinstance(data, dict):
        for section_key, section_val in data.items():
            print_section(section_key, section_val)
    elif isinstance(data, list):
        for i, item in enumerate(data):
            print_section(f"[{i}]", item)
    else:
        print(data)

    # ── Save flattened CSV ─────────────────────────────────────────────────
    flat = flatten(data)
    with open(args.out, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["key", "value"])
        for k, v in flat.items():
            writer.writerow([k, v])
    print(f"\nFlattened summary saved → {args.out}")


if __name__ == "__main__":
    main()

