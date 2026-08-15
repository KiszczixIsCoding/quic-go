import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import os

CSV_PATH = os.path.join(os.path.dirname(__file__), "received_packets.csv")
OUT_PATH = os.path.join(os.path.dirname(__file__), "received_packets.png")


def main():
    df = pd.read_csv(CSV_PATH)
    df = df.dropna(subset=["timestamp", "conn_id", "data_size"])
    df = df[df["timestamp"].astype(str).str.strip() != ""]
    df["timestamp"] = pd.to_datetime(df["timestamp"], format="ISO8601", utc=True)
    df = df.sort_values("timestamp")

    # Czas od początku transmisji (w sekundach)
    t0 = df["timestamp"].min()
    df["elapsed"] = (df["timestamp"] - t0).dt.total_seconds()

    # Narastające bajty per conn_id
    conns = df["conn_id"].unique()
    colors = ["tab:blue", "tab:orange", "tab:green", "tab:red"]

    fig, axes = plt.subplots(2, 1, figsize=(13, 9))

    # Wykres 1: narastające bajty per połączenie + łącznie
    ax1 = axes[0]
    total_cumsum = None
    for i, conn in enumerate(sorted(conns)):
        sub = df[df["conn_id"] == conn].copy()
        sub["cumulative"] = sub["data_size"].cumsum()
        ax1.plot(sub["elapsed"], sub["cumulative"], label=conn, color=colors[i % len(colors)], drawstyle='steps-post')
        if total_cumsum is None:
            total_cumsum = sub.set_index("elapsed")["data_size"]
        else:
            total_cumsum = total_cumsum.add(
                sub.set_index("elapsed")["data_size"], fill_value=0
            )

    # Łączne narastające bajty
    df_all = df.sort_values("elapsed")
    df_all["cumulative_total"] = df_all["data_size"].cumsum()
    ax1.plot(df_all["elapsed"], df_all["cumulative_total"],
             label="total", color="black", linestyle="--", linewidth=1.5, drawstyle='steps-post')

    ax1.set_xlabel("Czas od startu (s)")
    ax1.set_ylabel("Bajty (narastająco)")
    ax1.set_title("Narastające bajty per połączenie")
    ax1.legend()
    ax1.grid(True)

    # Wykres 2: bajty per pakiet w czasie (scatter)
    ax2 = axes[1]
    for i, conn in enumerate(sorted(conns)):
        sub = df[df["conn_id"] == conn]
        ax2.scatter(sub["elapsed"], sub["data_size"],
                    label=conn, color=colors[i % len(colors)], s=6, alpha=0.7)
    ax2.set_xlabel("Czas od startu (s)")
    ax2.set_ylabel("Bajty (per pakiet)")
    ax2.set_title("Rozmiar pakietów w czasie per połączenie")
    ax2.legend()
    ax2.grid(True)

    fig.tight_layout()
    fig.savefig(OUT_PATH, dpi=150)
    print(f"Zapisano: {OUT_PATH}")


if __name__ == "__main__":
    main()
