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
    <run_dir>/packet_log2.csv              timestamp,conn_id,offset,data_size,throughput
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


CONN_COLORS = {
    "conn1": "#0078D4",
    "conn2": "#9E1B32",
}

CONN_LABELS = {
    "conn1": "azure",
    "conn2": "tul",
}


def conn_color(cid):
    return CONN_COLORS.get(cid)


def conn_label(cid):
    return CONN_LABELS.get(cid, cid)


def _style(ax, xlabel, ylabel, title):
    ax.set_xlabel(xlabel)
    ax.set_ylabel(ylabel)
    ax.set_title(title)
    ax.set_xlim(left=0)
    ax.grid(True, linestyle="--", alpha=0.5)
    ax.legend()


def plot_rtt(run_dir, out_dir):
    df = load(os.path.join(run_dir, "stats_rtt", "rtt.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["rtt_ms"], label=conn_label(cid), color=conn_color(cid))
    _style(ax, "Czas (s)", "Wygładzone RTT (ms)", "Wygładzone RTT dla każdego połączenia")
    save(fig, out_dir, "rtt.png")


def plot_throughput(run_dir, out_dir):
    df = load(os.path.join(run_dir, "stats_throughput", "throughput.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["throughput_mbs"], label=conn_label(cid), color=conn_color(cid))
    pivot = df.pivot_table(index="t", columns="conn_id", values="throughput_mbs", aggfunc="mean")
    ax.plot(pivot.index, pivot.sum(axis=1), label="łącznie", linestyle="--", color="black")
    _style(ax, "Czas (s)", "Przepustowość (MB/s)", "Przepustowość dla każdego połączenia")
    save(fig, out_dir, "throughput.png")


def plot_inflight(run_dir, out_dir):
    df = load(os.path.join(run_dir, "stats_inflight", "inflight.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["bytes_in_flight"], label=conn_label(cid), color=conn_color(cid))
    pivot = df.pivot_table(index="t", columns="conn_id", values="bytes_in_flight", aggfunc="mean")
    ax.plot(pivot.index, pivot.sum(axis=1), label="łącznie", linestyle="--", color="black")
    _style(ax, "Czas (s)", "Dane inflight", "Dane inflight dla każdego połączenia")
    save(fig, out_dir, "inflight.png")


def plot_gaps(run_dir, out_dir):
    df = load(os.path.join(run_dir, "gaps.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    ax.plot(df["t"], df["gaps_between_offset"], label="luki między offsetami", color="black")
    _style(ax, "Czas (s)", "Liczba", "Luki między offsetami w czasie")
    save(fig, out_dir, "gaps.png")


def plot_progress(run_dir, out_dir):
    df = load(os.path.join(run_dir, "packet_log2.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    df = df.sort_values("t")
    mb = 1024 * 1024
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["data_size"].cumsum() / mb, label=conn_label(cid), color=conn_color(cid))
    ax.plot(df["t"], df["data_size"].cumsum() / mb, label="łącznie", linestyle="--", color="black")
    _style(ax, "Czas (s)", "Pobrane (MB)", "Kumulacyjnie pobrane dla każdego połączenia")
    save(fig, out_dir, "progress.png")


def plot_latency(run_dir, out_dir):
    df = load(os.path.join(run_dir, "latency.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.plot(g["t"], g["latency_ms"], label=conn_label(cid), color=conn_color(cid))
    _style(ax, "Czas (s)", "Opóźnienie odczytu (ms)", "Opóźnienie odczytu dla każdego połączenia")
    save(fig, out_dir, "latency.png")


def plot_packet_scatter(run_dir, out_dir):
    df = load(os.path.join(run_dir, "packet_log2.csv"))
    if df is None:
        return
    df["t"] = rel_time(df["timestamp"])
    df = df.sort_values("t")
    kb = 1024
    fig, ax = plt.subplots(figsize=(10, 5))
    for cid, g in df.groupby("conn_id"):
        ax.scatter(g["t"], g["data_size"] / kb, label=conn_label(cid), color=conn_color(cid), s=5, alpha=0.5)
    _style(ax, "Czas (s)", "Rozmiar pakietu (KB)", "Otrzymane pakiety w czasie")
    save(fig, out_dir, "packet_scatter.png")


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
    plot_packet_scatter(run_dir, out_dir)

    print("Done. Charts in", out_dir)


if __name__ == "__main__":
    main()
