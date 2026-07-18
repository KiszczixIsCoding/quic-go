import os
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

CHARTS_DIR = os.path.join(os.path.dirname(__file__), "charts")

COLORS = ['tab:blue', 'tab:orange', 'tab:green', 'tab:red']


def _peer_color_map(all_peers):
    peer_list = sorted(all_peers)
    return peer_list, {peer: COLORS[i % len(COLORS)] for i, peer in enumerate(peer_list)}


def plot_throughput_per_peer(throughput_samples, out_dir=CHARTS_DIR):
    """Throughput per peer (MB/s) w czasie. Zapisuje do out_dir/throughput_per_peer.png."""
    os.makedirs(out_dir, exist_ok=True)
    all_peers = {peer for _, pb in throughput_samples for peer in pb}
    peer_list, peer_color = _peer_color_map(all_peers)

    fig, ax = plt.subplots(figsize=(12, 5))
    for peer in peer_list:
        times = [t for t, pb in throughput_samples if peer in pb]
        speeds = [pb[peer] / 1024 / 1024 for t, pb in throughput_samples if peer in pb]
        ax.plot(times, speeds, label=peer, color=peer_color[peer])
    ax.set_xlabel("Czas od startu (s)")
    ax.set_ylabel("Throughput (MB/s)")
    ax.set_title("Throughput per peer")
    ax.legend()
    ax.grid(True)
    fig.tight_layout()
    path = os.path.join(out_dir, "throughput_per_peer.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"Zapisano: {path}")
    return path


def plot_throughput_total(throughput_samples, out_dir=CHARTS_DIR):
    """Łączny throughput (MB/s) w czasie. Zapisuje do out_dir/throughput_total.png."""
    os.makedirs(out_dir, exist_ok=True)

    fig, ax = plt.subplots(figsize=(12, 5))
    times = [t for t, pb in throughput_samples]
    speeds = [sum(pb.values()) / 1024 / 1024 for _, pb in throughput_samples]
    ax.plot(times, speeds, color='black', label='Total')
    ax.set_xlabel("Czas od startu (s)")
    ax.set_ylabel("Throughput (MB/s)")
    ax.set_title("Łączny throughput")
    ax.legend()
    ax.grid(True)
    fig.tight_layout()
    path = os.path.join(out_dir, "throughput_total.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"Zapisano: {path}")
    return path


def plot_pieces_per_peer(piece_events, out_dir=CHARTS_DIR):
    """Scatter: oś X = czas, oś Y = numer piece, kolor = peer.
    Zapisuje do out_dir/pieces_per_peer.png.
    piece_events: lista (elapsed, piece_idx, peers_set)
    """
    os.makedirs(out_dir, exist_ok=True)
    all_peers = {peer for _, _, ps in piece_events for peer in ps}
    peer_list, peer_color = _peer_color_map(all_peers)

    fig, ax = plt.subplots(figsize=(12, 6))
    for peer in peer_list:
        times = [e for e, _, ps in piece_events if peer in ps]
        pieces = [idx for _, idx, ps in piece_events if peer in ps]
        ax.scatter(times, pieces, label=peer, color=peer_color[peer], s=8, alpha=0.7)
    ax.set_xlabel("Czas od startu (s)")
    ax.set_ylabel("Numer piece")
    ax.set_title("Pieces per peer")
    ax.legend()
    ax.grid(True)
    fig.tight_layout()
    path = os.path.join(out_dir, "pieces_per_peer.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"Zapisano: {path}")
    return path


def plot_all(throughput_samples, piece_events, out_dir=CHARTS_DIR):
    """Generuje wszystkie trzy wykresy."""
    plot_throughput_per_peer(throughput_samples, out_dir)
    plot_throughput_total(throughput_samples, out_dir)
    plot_pieces_per_peer(piece_events, out_dir)
