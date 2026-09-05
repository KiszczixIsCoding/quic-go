import os
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib.ticker import MultipleLocator, MaxNLocator

CHARTS_DIR = os.path.join(os.path.dirname(__file__), "charts")

# Etykiety i kolory spójne z wykresami QUIC (azure/tul)
PEER_LABELS = {
    "20.107.170.9": "azure",
    "212.51.220.6": "tul",
}
PEER_COLORS = {
    "azure": "#0078D4",
    "tul": "#9E1B32",
}
FALLBACK_COLORS = ['tab:blue', 'tab:orange', 'tab:green', 'tab:red']


def _peer_label(peer):
    ip = peer.split(":")[0]
    return PEER_LABELS.get(ip, peer)


def _peer_color_map(all_peers):
    peer_list = sorted(all_peers)
    color = {}
    for i, peer in enumerate(peer_list):
        label = _peer_label(peer)
        color[peer] = PEER_COLORS.get(label, FALLBACK_COLORS[i % len(FALLBACK_COLORS)])
    return peer_list, color


def _style(ax, xlabel, ylabel, title):
    ax.set_xlabel(xlabel)
    ax.set_ylabel(ylabel)
    ax.set_title(title)
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0)
    _, hi = ax.get_xlim()
    if hi < 12:
        ax.set_xlim(right=12)
    ax.xaxis.set_major_locator(MultipleLocator(2))
    ax.grid(True, linestyle="--", alpha=0.5)
    handles, labels = ax.get_legend_handles_labels()
    if labels:
        ax.legend()


def _save(fig, out_dir, name):
    path = os.path.join(out_dir, name)
    fig.tight_layout()
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"Zapisano: {path}")
    return path


def _t0(samples):
    """Pierwszy moment z danymi (nawiązanie połączenia) — jak rel_time w QUIC."""
    for t, pb in samples:
        if any(v > 0 for v in pb.values()):
            return t
    return 0.0


def plot_throughput(throughput_samples, out_dir=CHARTS_DIR):
    """Przepustowość per połączenie + łącznie (MB/s). -> out_dir/throughput.png"""
    os.makedirs(out_dir, exist_ok=True)
    all_peers = {peer for _, pb in throughput_samples for peer in pb}
    peer_list, peer_color = _peer_color_map(all_peers)

    fig, ax = plt.subplots(figsize=(10, 5))
    t0 = _t0(throughput_samples)
    for peer in peer_list:
        times = [t - t0 for t, pb in throughput_samples if peer in pb]
        speeds = [pb[peer] for t, pb in throughput_samples if peer in pb]
        ax.plot(times, speeds, label=_peer_label(peer), color=peer_color[peer])
    total_times = [t - t0 for t, pb in throughput_samples]
    total_speeds = [sum(pb.values()) for t, pb in throughput_samples]
    ax.plot(total_times, total_speeds, label="łącznie", linestyle="--", color="black")
    _style(ax, "Czas (s)", "Przepustowość (MB/s)", "Przepustowość dla każdego peer'a")
    return _save(fig, out_dir, "throughput.png")


def plot_rtt(rtt_samples, out_dir=CHARTS_DIR):
    """Wygładzone RTT per połączenie (ms), bez łącznie. -> out_dir/rtt.png"""
    os.makedirs(out_dir, exist_ok=True)
    all_peers = {peer for _, pb in rtt_samples for peer in pb}
    peer_list, peer_color = _peer_color_map(all_peers)

    fig, ax = plt.subplots(figsize=(10, 5))
    t0 = _t0(rtt_samples)
    for peer in peer_list:
        times = [t - t0 for t, pb in rtt_samples if peer in pb]
        rtts = [pb[peer] for t, pb in rtt_samples if peer in pb]
        ax.plot(times, rtts, label=_peer_label(peer), color=peer_color[peer])
    _style(ax, "Czas (s)", "Wygładzone RTT (ms)", "Wygładzone RTT dla każdego peer'a")
    return _save(fig, out_dir, "rtt.png")


def plot_progress(progress_samples, out_dir=CHARTS_DIR):
    """Kumulacyjnie pobrane per połączenie + łącznie (MB). -> out_dir/progress.png"""
    os.makedirs(out_dir, exist_ok=True)
    all_peers = {peer for _, pb in progress_samples for peer in pb}
    peer_list, peer_color = _peer_color_map(all_peers)

    fig, ax = plt.subplots(figsize=(10, 5))
    t0 = _t0(progress_samples)
    for peer in peer_list:
        times = [t - t0 for t, pb in progress_samples if peer in pb]
        mbs = [pb[peer] for t, pb in progress_samples if peer in pb]
        ax.plot(times, mbs, label=_peer_label(peer), color=peer_color[peer])
    total_times = [t - t0 for t, pb in progress_samples]
    total_mbs = [sum(pb.values()) for t, pb in progress_samples]
    ax.plot(total_times, total_mbs, label="łącznie", linestyle="--", color="black")
    _style(ax, "Czas (s)", "Pobrane (MB)", "Kumulacyjnie pobrane dla każdego peer'a")
    return _save(fig, out_dir, "progress.png")


def plot_pieces_per_peer(piece_events, out_dir=CHARTS_DIR):
    """Scatter: oś X = czas, oś Y = numer piece, kolor = peer.
    Zapisuje do out_dir/pieces_per_peer.png.
    piece_events: lista (elapsed, piece_idx, peers_set)
    """
    os.makedirs(out_dir, exist_ok=True)
    all_peers = {peer for _, _, ps in piece_events for peer in ps}
    peer_list, peer_color = _peer_color_map(all_peers)

    fig, ax = plt.subplots(figsize=(10, 5))
    t0 = piece_events[0][0] if piece_events else 0.0
    for peer in peer_list:
        times = [e - t0 for e, _, ps in piece_events if peer in ps]
        pieces = [idx for _, idx, ps in piece_events if peer in ps]
        ax.scatter(times, pieces, label=_peer_label(peer), color=peer_color[peer], s=5, alpha=0.5)
    _style(ax, "Czas (s)", "Numer piece", "Piece'y per peer")
    ax.yaxis.set_major_locator(MaxNLocator(integer=True))
    return _save(fig, out_dir, "pieces_per_peer.png")


def plot_all(throughput_samples, piece_events, out_dir=CHARTS_DIR,
             progress_samples=None, rtt_samples=None):
    """Generuje wszystkie wykresy. progress/rtt opcjonalne (kompat. wstecz)."""
    plot_throughput(throughput_samples, out_dir)
    if progress_samples:
        plot_progress(progress_samples, out_dir)
    if rtt_samples:
        plot_rtt(rtt_samples, out_dir)
    plot_pieces_per_peer(piece_events, out_dir)
