"""Aggregate RTT, throughput, and transfer time across the latest N runs.

Usage:
    python aggregate_runs.py [N] [K]

N = how many of the most recent runs/run_* directories to aggregate (default: all).
K = how many of the WORST runs to discard from the average (default: 4).
    "Worst" = longest transfer time.

For each run the script reads:
    <run>/stats_rtt/rtt.csv               timestamp,conn_id,rtt_ns,rtt_ms
    <run>/stats_throughput/throughput.csv  timestamp,conn_id,throughput_mbs,total_bytes
    <run>/packet_log2.csv                  timestamp,conn_id,offset,data_size,throughput

Transfer time = time range of the received data (last - first timestamp),
taken from packet_log2.csv (fallback: throughput.csv, then rtt.csv).

Per-run metrics:
    rtt_<cid>_mean_ms   - mean RTT per connection
    rtt_total_mean_ms   - mean of the per-connection mean RTTs
    tp_<cid>_mean_mbs   - mean throughput per connection (MiB/s)
    tp_total_mean_mbs   - combined throughput = total bytes / transfer time (MiB/s)
    transfer_time_s     - transfer time (last - first received timestamp)

All values are rounded to 2 decimal places.

Outputs (written to the base dir, i.e. the quic/ folder):
    aggregated_all.csv          - one row per run: RTT, throughput, transfer time, excluded flag
    aggregated_average.csv      - per metric: min, max, mean, std over the kept (N-K) runs + which runs were excluded
    aggregated_average_latex.txt - three LaTeX tables (RTT, throughput, transfer time; Min/Max/Średnia/Odchylenie std)
"""

import glob
import os
import sys

import pandas as pd


def find_runs(base_dir):
    runs = glob.glob(os.path.join(base_dir, "runs", "run_*"))
    runs.sort(key=os.path.getmtime)
    return runs


def load(path):
    if not os.path.exists(path):
        return None
    return pd.read_csv(path)


def compute_transfer_time(run_dir):
    """Transfer time (seconds) = time range of the received data."""
    for fname in ["packet_log2.csv", "stats_throughput/throughput.csv", "stats_rtt/rtt.csv"]:
        df = load(os.path.join(run_dir, fname))
        if df is not None and not df.empty and "timestamp" in df.columns:
            ts = pd.to_datetime(df["timestamp"], utc=True, errors="coerce").dropna()
            if len(ts) >= 2:
                return (ts.max() - ts.min()).total_seconds()
    return None


def main():
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    n = int(sys.argv[1]) if len(sys.argv) > 1 else None
    k = int(sys.argv[2]) if len(sys.argv) > 2 else 4

    runs = find_runs(base_dir)
    if n is not None:
        runs = runs[-n:]

    if not runs:
        print("No runs found under", os.path.join(base_dir, "runs"))
        return

    print("Aggregating %d run(s); will discard %d worst (by transfer time)." % (len(runs), k))

    rows = []
    for run_dir in runs:
        row = {"run": os.path.basename(run_dir)}

        rtt = load(os.path.join(run_dir, "stats_rtt", "rtt.csv"))
        rtt_means = {}
        if rtt is not None and not rtt.empty:
            for cid, g in rtt.groupby("conn_id"):
                m = float(g["rtt_ms"].mean())
                row["rtt_%s_mean_ms" % cid] = round(m, 2)
                rtt_means[cid] = m
        if len(rtt_means) >= 2:
            row["rtt_total_mean_ms"] = round(sum(rtt_means.values()) / len(rtt_means), 2)

        tp = load(os.path.join(run_dir, "stats_throughput", "throughput.csv"))
        total_bytes = 0.0
        have_bytes = False
        if tp is not None and not tp.empty:
            for cid, g in tp.groupby("conn_id"):
                row["tp_%s_mean_mbs" % cid] = round(float(g["throughput_mbs"].mean()), 2)
                if "total_bytes" in g.columns:
                    total_bytes += float(g["total_bytes"].max())
                    have_bytes = True

        tt = compute_transfer_time(run_dir)
        if have_bytes and tt is not None and tt > 0:
            row["tp_total_mean_mbs"] = round(total_bytes / tt / 1024 / 1024, 2)
        row["transfer_time_s"] = round(tt, 2) if tt is not None else None

        rows.append(row)

    df = pd.DataFrame(rows)

    # Rank runs by transfer time; the K longest are the "worst" and get excluded.
    valid = df.dropna(subset=["transfer_time_s"])
    if len(valid) > k:
        worst = valid.nlargest(k, "transfer_time_s")
        excluded_runs = set(worst["run"])
    else:
        print("Warning: only %d valid run(s); cannot exclude %d. Excluding none." % (len(valid), k))
        excluded_runs = set()

    df["excluded"] = df["run"].isin(excluded_runs)

    # File 1: all runs (with excluded flag)
    all_path = os.path.join(base_dir, "aggregated_all.csv")
    df.to_csv(all_path, index=False)

    # File 2: average over the kept runs + which runs were excluded
    kept = df[~df["excluded"]].dropna(subset=["transfer_time_s"])
    metrics = [c for c in df.columns if c not in ("run", "excluded")]

    avg_rows = []
    for col in metrics:
        vals = kept[col].dropna()
        if len(vals) > 0:
            avg_rows.append({
                "metric": col,
                "min": round(float(vals.min()), 2),
                "max": round(float(vals.max()), 2),
                "mean": round(float(vals.mean()), 2),
                "std": round(float(vals.std()), 2) if len(vals) > 1 else 0.0,
                "n": len(vals),
            })
    avg_rows.append({"metric": "n_runs_used", "min": "", "max": "", "mean": len(kept), "std": "", "n": ""})
    avg_rows.append({"metric": "n_runs_excluded", "min": "", "max": "", "mean": len(excluded_runs), "std": "", "n": ""})
    avg_rows.append({"metric": "excluded_runs", "min": "", "max": "", "mean": ";".join(sorted(excluded_runs)), "std": "", "n": ""})

    avg_df = pd.DataFrame(avg_rows)
    avg_path = os.path.join(base_dir, "aggregated_average.csv")
    avg_df.to_csv(avg_path, index=False)

    # File 3: LaTeX tables for RTT, throughput, and transfer time
    def stat_row(col):
        if col not in kept.columns:
            return None
        vals = kept[col].dropna()
        if len(vals) == 0:
            return None
        std = float(vals.std()) if len(vals) > 1 else 0.0
        return (float(vals.min()), float(vals.max()), float(vals.mean()), std)

    def build_table(caption, label, unit, rows):
        body = []
        for (row_label, col) in rows:
            s = stat_row(col)
            if s is not None:
                body.append((row_label, s))
        if not body:
            return []
        lines = [
            "\\begin{table}[H]",
            "\\centering",
            "\\caption{%s}" % caption,
            "\\label{%s}" % label,
            "\\begin{tabular}{lrrrr}",
            "    \\hline",
            "     & Min [%s] & Max [%s] & Średnia [%s] & Odchylenie std. [%s] \\\\ \\hline" % (unit, unit, unit, unit),
        ]
        for i, (row_label, (mn, mx, mean, std)) in enumerate(body):
            end = "\\\\ \\hline" if i == len(body) - 1 else "\\\\"
            lines.append("    %s & %.2f & %.2f & %.2f & %.2f %s" % (row_label, mn, mx, mean, std, end))
        lines.append("\\end{tabular}")
        lines.append("\\end{table}")
        return lines

    rtt_rows = [
        ("RTT conn. 1", "rtt_conn1_mean_ms"),
        ("RTT conn. 2", "rtt_conn2_mean_ms"),
        ("RTT total", "rtt_total_mean_ms"),
    ]
    tp_rows = [
        ("Przepustowość conn. 1", "tp_conn1_mean_mbs"),
        ("Przepustowość conn. 2", "tp_conn2_mean_mbs"),
        ("Przepustowość total", "tp_total_mean_mbs"),
    ]
    time_rows = [
        ("Czas transmisji", "transfer_time_s"),
    ]

    latex_lines = []
    latex_lines += build_table("Średnie wartości RTT dla poszczególnych połączeń", "tab:rtt-results", "ms", rtt_rows)
    latex_lines.append("")
    latex_lines += build_table("Średnie wartości przepustowości dla poszczególnych połączeń", "tab:throughput-results", "MB/s", tp_rows)
    latex_lines.append("")
    latex_lines += build_table("Czas transmisji", "tab:time-results", "s", time_rows)
    latex_path = os.path.join(base_dir, "aggregated_average_latex.txt")
    with open(latex_path, "w", encoding="utf-8") as f:
        f.write("\n".join(latex_lines) + "\n")

    print("\nPer-run results:")
    print(df.to_string(index=False))
    print("\nSaved all runs to:", all_path)

    print("\nAverage over %d kept run(s):" % len(kept))
    print(avg_df.to_string(index=False))
    print("\nSaved average to:", avg_path)
    print("Saved LaTeX table to:", latex_path)

    if excluded_runs:
        print("\nExcluded (worst) runs:")
        for r in sorted(excluded_runs):
            tt = df.loc[df["run"] == r, "transfer_time_s"].iloc[0]
            print("  -", r, "(transfer_time=%ss)" % tt)


if __name__ == "__main__":
    main()
