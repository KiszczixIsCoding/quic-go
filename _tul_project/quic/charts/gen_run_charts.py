"""Generate charts from a run's CSV logs.

Usage:
    python gen_run_charts.py [run_dir]

If run_dir is omitted, the latest runs/run_* directory is used.
Charts are saved to <run_dir>/charts/*.png

Expected CSV layout (produced by the Go client):
    <run_dir>/stats_rtt/rtt.csv            timestamp,conn_id,rtt_ns,rtt_ms
    <run_dir>/stats_throughput/throughput.csv  timestamp,conn_id,throughput_mbs,total_bytes
    <run_dir>/stats_inflight/inflight.csv  timestamp,conn_id,bytes_in_flight
    <run_dir>/gaps.csv                     timestamp,current_offset,conn1_offset,conn2_offset,gaps,gaps_before_min,gaps_between_offset
    <run_dir>/latency.csv                  timestamp,conn_id,offset,data_size,latency_ns,latency_ms,throughput_mbps
"""

import glob
import os
import sys

import matplotlib.pyplot as plt
import pandas as pd


def find_latest_run(base_dir):
    runs = glob.glob(os.path.join(base_dir, "runs", "run_*"))
    if not runs:
        raise FileNotFoundError("No runs/run_* directory found under " + base_dir)
    return max(runs, key=os.path.getmtime)


def load(path):
    if not os.path.exists(path):
        return None
    return pd.read_csv(path)


def rel_time(series):
    """Convert a timestamp column to seconds since the first sample (UTC-normalized)."""
    ts = pd.to_datetime(series, utc=True, errors="coerce")
    t0 = ts.dropna().min()
    return (ts - t0).dt.total_seconds()


def save(fig, out_dir, name):
    path = os.path.join(out_dir, name)
    fig.tight_layout()
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print("saved", path)


def _style(ax, xlabel, ylabel, title):
    ax.set_xlabel(xlabel)
    ax.set_ylabel(ylabel)
    ax.set_title(title)
    ax.grid(True, linestyle="--", alpha=0.5)
    ax.legend()


def plot_rtt(run_dir, out_dir):
    df = load(os.path.join(run_dir, "stats_rtt", "rtt.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["rtt_ms"], label=cid)
    _style(ax, "Time (s)", "Smoothed RTT (ms)", "Smoothed RTT per connection")
    save(fig, out_dir, "rtt.png")


def plot_throughput(run_dir, out_dir):
    df = load(os.path.join(run_dir, "stats_throughput", "throughput.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["throughput_mbs"], label=cid)
    pivot = df.pivot_table(index="t", columns="conn_id", values="throughput_mbs", aggfunc="mean")
    ax.plot(pivot.index, pivot.sum(axis=1), label="total", linestyle="--")
    _style(ax, "Time (s)", "Throughput (MB/s)", "Throughput per connection + total")
    save(fig, out_dir, "throughput.png")


def plot_inflight(run_dir, out_dir):
    df = load(os.path.join(run_dir, "stats_inflight", "inflight.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["bytes_in_flight"], label=cid)
    pivot = df.pivot_table(index="t", columns="conn_id", values="bytes_in_flight", aggfunc="mean")
    ax.plot(pivot.index, pivot.sum(axis=1), label="total", linestyle="--")
    _style(ax, "Time (s)", "Bytes in flight", "Bytes in flight per connection + total")
    save(fig, out_dir, "inflight.png")


def plot_gaps(run_dir, out_dir):
    df = load(os.path.join(run_dir, "gaps.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    ax.plot(df["t"], df["gaps_between_offset"], label="gaps_between_offset")
    _style(ax, "Time (s)", "Count", "Gaps between offsets over time")
    save(fig, out_dir, "gaps.png")


def plot_progress(run_dir, out_dir):
    df = load(os.path.join(run_dir, "gaps.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    ax.plot(df["t"], df["current_offset"], label="current (max)")
    ax.plot(df["t"], df["conn1_offset"], label="conn1")
    ax.plot(df["t"], df["conn2_offset"], label="conn2")
    _style(ax, "Time (s)", "Offset (bytes)", "Transfer progress")
    save(fig, out_dir, "progress.png")


def plot_latency(run_dir, out_dir):
    df = load(os.path.join(run_dir, "latency.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["latency_ms"], label=cid)
    _style(ax, "Time (s)", "Read latency (ms)", "Read latency per connection")
    save(fig, out_dir, "latency.png")


def main():
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    run_dir = sys.argv[1] if len(sys.argv) > 1 else find_latest_run(base_dir)
    print("Run dir:", run_dir)

    out_dir = os.path.join(run_dir, "charts")
    os.makedirs(out_dir, exist_ok=True)

    plot_rtt(run_dir, out_dir)
    plot_throughput(run_dir, out_dir)
    plot_inflight(run_dir, out_dir)
    plot_gaps(run_dir, out_dir)
    plot_progress(run_dir, out_dir)
    plot_latency(run_dir, out_dir)

    print("Done. Charts in", out_dir)


if __name__ == "__main__":
    main()
