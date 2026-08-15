import pandas as pd
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import os

DIR = os.path.dirname(__file__)
CSV1 = os.path.join(DIR, "received_ranges_conn1.csv")
CSV2 = os.path.join(DIR, "received_ranges_conn2.csv")
OUT_PATH = os.path.join(DIR, "received_ranges.png")


def load(path):
    df = pd.read_csv(path)
    df = df.dropna(subset=["timestamp", "start", "end"])
    df["timestamp"] = pd.to_datetime(df["timestamp"], format="ISO8601", utc=True)
    df = df.sort_values("timestamp")
    return df


def main():
    df1 = load(CSV1)
    df2 = load(CSV2)

    df_all = pd.concat([df1, df2]).sort_values("timestamp")
    t0 = df_all["timestamp"].min()

    for df in [df1, df2, df_all]:
        df["elapsed"] = (df["timestamp"] - t0).dt.total_seconds()

    conns = {
        "conn1": df1,
        "conn2": df2,
    }
    colors = {"conn1": "tab:blue", "conn2": "tab:orange"}

    fig, axes = plt.subplots(3, 1, figsize=(14, 12))

    # Wykres 1: Gantt — zakresy jako poziome belki
    ax1 = axes[0]
    yticks = []
    ylabels = []
    for i, (conn, df) in enumerate(conns.items()):
        for _, row in df.iterrows():
            ax1.barh(
                y=i,
                width=row["end"] - row["start"],
                left=row["start"],
                height=0.4,
                color=colors[conn],
                alpha=0.6,
                label=conn if _ == df.index[0] else "",
            )
        yticks.append(i)
        ylabels.append(conn)
    ax1.set_yticks(yticks)
    ax1.set_yticklabels(ylabels)
    ax1.set_xlabel("Offset w pliku (bajty)")
    ax1.set_title("Pokrycie pliku per połączenie (zakresy)")
    ax1.grid(True, axis="x")
    # Deduplikuj legendę
    handles, labels = ax1.get_legend_handles_labels()
    by_label = dict(zip(labels, handles))
    ax1.legend(by_label.values(), by_label.keys())

    # Wykres 2: narastające bajty (end - start) w czasie per połączenie
    ax2 = axes[1]
    for conn, df in conns.items():
        df = df.copy()
        df["chunk_size"] = df["end"] - df["start"]
        df["cumulative"] = df["chunk_size"].cumsum()
        ax2.plot(df["elapsed"], df["cumulative"],
                 label=conn, color=colors[conn], drawstyle="steps-post")
    # Łącznie
    df_all2 = df_all.copy()
    df_all2["chunk_size"] = df_all2["end"] - df_all2["start"]
    df_all2["cumulative"] = df_all2["chunk_size"].cumsum()
    ax2.plot(df_all2["elapsed"], df_all2["cumulative"],
             label="total", color="black", linestyle="--", drawstyle="steps-post")
    ax2.set_xlabel("Czas od startu (s)")
    ax2.set_ylabel("Bajty (narastająco)")
    ax2.set_title("Narastające bajty per połączenie")
    ax2.legend()
    ax2.grid(True)

    # Wykres 3: rozmiar zakresu w czasie per połączenie
    ax3 = axes[2]
    all_sizes = []
    for conn, df in conns.items():
        df = df.copy()
        df["chunk_size"] = df["end"] - df["start"]
        ax3.scatter(df["elapsed"], df["chunk_size"],
                    label=conn, color=colors[conn], s=8, alpha=0.7)
        all_sizes.extend(df["chunk_size"].tolist())

    ax3.axhline(68000, color="red", linestyle="-", linewidth=2.5, label="max = 68000 B", zorder=5)
    ax3.axhline(34000, color="black", linestyle="-", linewidth=2.5, label="max/2 = 34000 B", zorder=5)
    ax3.set_ylim(0, 68000 * 1.1)
    ax3.set_xlabel("Czas od startu (s)")
    ax3.set_ylabel("Rozmiar zakresu (bajty)")
    ax3.set_title("Rozmiar przesłanych zakresów w czasie")
    ax3.legend()
    ax3.grid(True)

    fig.tight_layout()
    fig.savefig(OUT_PATH, dpi=150)
    print(f"Zapisano: {OUT_PATH}")


if __name__ == "__main__":
    main()
