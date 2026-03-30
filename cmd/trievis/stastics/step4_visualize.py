#!/usr/bin/env python3
"""
step4_visualize.py
------------------
Read the CSV files produced by step3_stream_stats.py and generate charts.

Usage:
    pip install matplotlib pandas numpy   # one-time
    python step4_visualize.py [--in-dir ./] [--out-dir ./]

Output:
    trie_depth_total.png          – total nodes per depth (account & storage)
    trie_depth_by_type.png        – short/full/value breakdown per depth
    trie_storage_max_depth.png    – distribution of per-trie max depths
    trie_depth_stats.txt          – min/max/mean/median/P90/P95/P99
"""

import argparse
import os
import sys

try:
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    import matplotlib.ticker as mticker
    import numpy as np
    import pandas as pd
except ImportError:
    print("ERROR: missing packages.  Run:  pip install matplotlib pandas numpy",
          file=sys.stderr)
    sys.exit(1)

DEFAULT_IN_DIR  = "."
DEFAULT_OUT_DIR = "."

NODE_TYPES = ('short', 'full', 'value')
NODE_COLORS = {'short': '#4C72B0', 'full': '#DD8452', 'value': '#55A868'}
TYPE_LABELS = {'short': 'Short (extension)', 'full': 'Full (branch)', 'value': 'Value (leaf)'}


def load_depth_csv(path):
    df = pd.read_csv(path)
    df.columns = df.columns.str.strip()
    df = df.sort_values('depth').reset_index(drop=True)
    df['count'] = df['count'].astype(int)
    return df


def enrich(df):
    """Add pct and cumulative_pct columns."""
    df = df.copy()
    total = df['count'].sum()
    df['pct'] = df['count'] / total * 100 if total else 0
    df['cumulative_pct'] = df['pct'].cumsum()
    return df


def depth_stats(df):
    depths = np.repeat(df['depth'].values, df['count'].values)
    if len(depths) == 0:
        return {}
    return {
        'total':  int(df['count'].sum()),
        'min':    int(depths.min()),
        'max':    int(depths.max()),
        'mean':   float(depths.mean()),
        'median': float(np.median(depths)),
        'p90':    float(np.percentile(depths, 90)),
        'p95':    float(np.percentile(depths, 95)),
        'p99':    float(np.percentile(depths, 99)),
    }


def fmt_axis(ax):
    ax.yaxis.set_major_formatter(
        mticker.FuncFormatter(lambda x, _: f'{x:,.0f}'))


def plot_total_depth(ax, df, title, color):
    df = enrich(df)
    ax2 = ax.twinx()
    ax.bar(df['depth'], df['count'], color=color, alpha=0.75, label='nodes')
    ax2.plot(df['depth'], df['cumulative_pct'], 'r-o',
             markersize=3, linewidth=1.2, label='cumulative %')
    ax.set_title(title, fontweight='bold')
    ax.set_xlabel('Depth')
    ax.set_ylabel('Node Count')
    ax2.set_ylabel('Cumulative %')
    ax2.set_ylim(0, 105)
    fmt_axis(ax)
    h1, l1 = ax.get_legend_handles_labels()
    h2, l2 = ax2.get_legend_handles_labels()
    ax.legend(h1 + h2, l1 + l2, loc='upper left', fontsize=8)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--in-dir',  default=DEFAULT_IN_DIR)
    parser.add_argument('--out-dir', default=DEFAULT_OUT_DIR)
    args = parser.parse_args()

    os.makedirs(args.out_dir, exist_ok=True)

    def inpath(name):
        return os.path.join(args.in_dir, name)

    def outpath(name):
        return os.path.join(args.out_dir, name)

    # ── Check required files ─────────────────────────────────────────────
    required = [f'{p}_depth_total.csv' for p in ('account', 'storage')]
    missing = [f for f in required if not os.path.exists(inpath(f))]
    if missing:
        print('ERROR: missing CSV files:', missing, file=sys.stderr)
        print('       Run step3_stream_stats.py first.', file=sys.stderr)
        sys.exit(1)

    # ── Load data ────────────────────────────────────────────────────────
    acc_total = load_depth_csv(inpath('account_depth_total.csv'))
    sto_total = load_depth_csv(inpath('storage_depth_total.csv'))

    acc_by_type, sto_by_type = {}, {}
    for t in NODE_TYPES:
        p = inpath(f'account_depth_{t}.csv')
        if os.path.exists(p):
            acc_by_type[t] = load_depth_csv(p)
        p = inpath(f'storage_depth_{t}.csv')
        if os.path.exists(p):
            sto_by_type[t] = load_depth_csv(p)

    max_depth_file = inpath('storage_max_depth.csv')
    sto_max_depth = load_depth_csv(max_depth_file) if os.path.exists(max_depth_file) else None

    # ── Figure 1: Total node count per depth ────────────────────────────
    fig1, axes = plt.subplots(1, 2, figsize=(18, 6))
    fig1.suptitle('Trie Node Depth Distribution (all node types)', fontsize=14, fontweight='bold')
    plot_total_depth(axes[0], acc_total, 'Account Trie', '#4C72B0')
    plot_total_depth(axes[1], sto_total, 'Storage Tries (aggregate)', '#DD8452')
    plt.tight_layout()
    p = outpath('trie_depth_total.png')
    fig1.savefig(p, dpi=150)
    print(f'Saved {p}')

    # ── Figure 2: Stacked bar by node type ──────────────────────────────
    fig2, axes2 = plt.subplots(1, 2, figsize=(18, 6))
    fig2.suptitle('Trie Node Depth Distribution by Node Type', fontsize=14, fontweight='bold')

    def plot_stacked(ax, by_type, title):
        if not by_type:
            ax.set_visible(False)
            return
        all_depths = sorted(set().union(*[set(df['depth']) for df in by_type.values()]))
        bottoms = np.zeros(len(all_depths))
        depth_idx = {d: i for i, d in enumerate(all_depths)}
        for t in NODE_TYPES:
            if t not in by_type:
                continue
            df = by_type[t]
            vals = np.zeros(len(all_depths))
            for _, row in df.iterrows():
                vals[depth_idx[row['depth']]] = row['count']
            ax.bar(all_depths, vals, bottom=bottoms,
                   color=NODE_COLORS[t], alpha=0.85, label=TYPE_LABELS[t])
            bottoms += vals
        ax.set_title(title, fontweight='bold')
        ax.set_xlabel('Depth')
        ax.set_ylabel('Node Count')
        fmt_axis(ax)
        ax.legend(fontsize=9)

    plot_stacked(axes2[0], acc_by_type, 'Account Trie – by Node Type')
    plot_stacked(axes2[1], sto_by_type, 'Storage Tries – by Node Type')
    plt.tight_layout()
    p = outpath('trie_depth_by_type.png')
    fig2.savefig(p, dpi=150)
    print(f'Saved {p}')

    # ── Figure 3: Storage trie max-depth histogram ───────────────────────
    if sto_max_depth is not None and not sto_max_depth.empty:
        fig3, ax3 = plt.subplots(figsize=(12, 5))
        sto_max_depth_e = enrich(sto_max_depth)
        ax3b = ax3.twinx()
        ax3.bar(sto_max_depth_e['depth'], sto_max_depth_e['count'],
                color='#C44E52', alpha=0.75, label='trie count')
        ax3b.plot(sto_max_depth_e['depth'], sto_max_depth_e['cumulative_pct'],
                  'b-o', markersize=3, linewidth=1.2, label='cumulative %')
        ax3.set_title('Storage Trie Max-Depth Distribution\n'
                      '(how deep each individual storage trie goes)',
                      fontweight='bold')
        ax3.set_xlabel('Max Depth of Storage Trie')
        ax3.set_ylabel('Number of Storage Tries')
        ax3b.set_ylabel('Cumulative %')
        ax3b.set_ylim(0, 105)
        fmt_axis(ax3)
        h1, l1 = ax3.get_legend_handles_labels()
        h2, l2 = ax3b.get_legend_handles_labels()
        ax3.legend(h1 + h2, l1 + l2, loc='upper left', fontsize=8)
        plt.tight_layout()
        p = outpath('trie_storage_max_depth.png')
        fig3.savefig(p, dpi=150)
        print(f'Saved {p}')

    # ── Depth stats text ─────────────────────────────────────────────────
    stats_path = outpath('trie_depth_stats.txt')
    with open(stats_path, 'w') as f:
        for label, df in [('Account Trie', acc_total),
                          ('Storage Tries (aggregate)', sto_total)]:
            s = depth_stats(df)
            if not s:
                continue
            f.write(f'=== {label} ===\n')
            f.write(f'  Total nodes : {s["total"]:>15,}\n')
            f.write(f'  Min depth   : {s["min"]:>15}\n')
            f.write(f'  Max depth   : {s["max"]:>15}\n')
            f.write(f'  Mean depth  : {s["mean"]:>15.2f}\n')
            f.write(f'  Median depth: {s["median"]:>15.1f}\n')
            f.write(f'  P90 depth   : {s["p90"]:>15.1f}\n')
            f.write(f'  P95 depth   : {s["p95"]:>15.1f}\n')
            f.write(f'  P99 depth   : {s["p99"]:>15.1f}\n\n')

    with open(stats_path) as f:
        print(f.read())

    print(f'Stats saved → {stats_path}')
    print('Done.')


if __name__ == '__main__':
    main()

