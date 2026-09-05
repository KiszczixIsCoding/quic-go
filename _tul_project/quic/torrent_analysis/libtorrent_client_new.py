import libtorrent as lt
import time
import sys
import os
from datetime import datetime
from collections import defaultdict
from charts import plot_all, CHARTS_DIR




def create_client():
    ses = lt.session()

    ses.apply_settings({
    "enable_lsd": False,
    "enable_dht": False,
    "enable_upnp": False,
    "enable_natpmp": False,
    "listen_interfaces": "0.0.0.0:52864",
    "alert_queue_size": 100000,
    # "download_rate_limit": 10,  # limit 100 KB/s żeby wymusić użycie obu peerów
    "enable_outgoing_utp": True,
    "enable_incoming_utp": True,
})
    # porty nasłuchujące dla peerów
    # ses.listen_on(52862, 52864)

    print(ses.listen_port())
    # opcjonalnie włącz DHT
    # ses.start_dht()

    print("Available categories:", [x for x in dir(lt.alert.category_t) if not x.startswith('_')], flush=True)
    ses.set_alert_mask(lt.alert.category_t.all_categories)
    return ses


def add_torrent(session, torrent_file, download_path):
    params = {
        "save_path": download_path,
        "ti": lt.torrent_info(torrent_file)
    }

    handle = session.add_torrent(params)
    return handle


def get_piece_size(torrent_info, piece_index):
    if piece_index == torrent_info.num_pieces() - 1:
        return torrent_info.total_size() - (torrent_info.num_pieces() - 1) * torrent_info.piece_length()
    return torrent_info.piece_length()


def format_size(bytes_val):
    if bytes_val >= 1024 * 1024:
        return f"{bytes_val / (1024 * 1024):.2f} MB"
    elif bytes_val >= 1024:
        return f"{bytes_val / 1024:.2f} KB"
    return f"{bytes_val} B"


def compute_stats(values):
    if not values:
        return 0.0, 0.0, 0.0, 0.0
    vmin = min(values)
    vmax = max(values)
    mean = sum(values) / len(values)
    var = sum((v - mean) ** 2 for v in values) / len(values)
    return vmin, vmax, mean, var


def write_metric_summary(path, label, section, values, duration=None):
    with open(path, "w") as f:
        f.write("=== %s ===\n\n" % label)
        if values:
            vmin, vmax, mean, var = compute_stats(values)
            f.write("--- %s ---\n" % section)
            f.write("Count: %d\n" % len(values))
            f.write("Min: %.6f\n" % vmin)
            f.write("Max: %.6f\n" % vmax)
            f.write("Mean: %.6f\n" % mean)
            f.write("Variance: %.6f\n" % var)
            f.write("StdDev: %.6f\n" % (var ** 0.5))
        if duration is not None:
            f.write("\n=== Transfer Duration ===\n")
            f.write("Total: %.6f s\n" % duration)


def monitor(handle, session, seed_addrs=None):
    if seed_addrs is None:
        seed_addrs = []
    torrent_info = handle.torrent_file()
    piece_peers = {}    # piece_index -> set of peer IPs
    pending_pieces = set()  # pieces that got piece_finished but not all blocks yet
    start_time = time.time()
    first_block_time = None  # czas pierwszego otrzymanego bloku (pierwszy bajt)

    # Mapowanie adresów seedów na etykiety conn (jak w QUIC: conn1, conn2, ...)
    addr_to_conn = {f"{ip}:{port}": "conn%d" % (i + 1) for i, (ip, port) in enumerate(seed_addrs)}

    # Katalog runa z metrykami w tym samym układzie co klient QUIC
    base_dir = os.path.dirname(__file__)
    run_dir = os.path.join(base_dir, "runs", "run_" + datetime.now().strftime("%Y%m%d_%H%M%S"))
    os.makedirs(os.path.join(run_dir, "stats_rtt"), exist_ok=True)
    os.makedirs(os.path.join(run_dir, "stats_throughput"), exist_ok=True)
    rtt_csv = open(os.path.join(run_dir, "stats_rtt", "rtt.csv"), "w")
    tp_csv = open(os.path.join(run_dir, "stats_throughput", "throughput.csv"), "w")
    rtt_csv.write("timestamp,conn_id,rtt_ns,rtt_ms\n")
    tp_csv.write("timestamp,conn_id,throughput_mbs,total_bytes\n")

    # Dane do wykresu: lista (elapsed, {peer: bytes_in_window})
    throughput_samples = []
    peer_bytes_total = defaultdict(int)   # narastające bajty per peer
    peer_bytes_prev = {}                  # bajty na początku poprzedniego okna
    last_sample_time = start_time
    window_start_elapsed = 0.0
    # Dane do scatter plota: lista (elapsed, piece_idx, peers_set)
    piece_events = []
    last_rtt_sample = start_time
    # Wartości do summary (jak w QUIC): per conn i combined
    rtt_values = defaultdict(list)
    tp_values = defaultdict(list)

    while not handle.status().is_seeding:
        alerts = session.pop_alerts()
        for alert in alerts:
            name = type(alert).__name__
            # Log peer connection/disconnect alerts
            if name in ('peer_connected_alert', 'peer_disconnected_alert', 'tcp_error_alert', 'peer_error_alert', 'session_error_alert'):
                err = getattr(alert, 'error', None)
                err_msg = str(err) if err else "none"
                msg_fn = getattr(alert, 'message', None)
                msg = msg_fn() if callable(msg_fn) else str(msg_fn)
                ip = f"{alert.ip[0]}:{alert.ip[1]}" if hasattr(alert, 'ip') else "N/A"
                print(f"\n[ALERT] {name}: ip={ip} error={err_msg} msg={msg}", flush=True)
            if name == 'block_finished_alert':
                if first_block_time is None:
                    first_block_time = time.time()
                peer_ip = f"{alert.ip[0]}:{alert.ip[1]}"
                if alert.piece_index not in piece_peers:
                    piece_peers[alert.piece_index] = set()
                piece_peers[alert.piece_index].add(peer_ip)
                peer_bytes_total[peer_ip] += 16 * 1024  # blok = 16 KB
                # Sprawdź czy mamy oczekujący piece do zalogowania
                if alert.piece_index in pending_pieces:
                    pending_pieces.discard(alert.piece_index)
            elif name == 'piece_finished_alert':
                pending_pieces.add(alert.piece_index)
                # Sprawdź czy mamy już peerów
                peers_set = piece_peers.get(alert.piece_index)
                if not peers_set:
                    # Fallback — brakiemy block_finished
                    peers = handle.get_peer_info()
                    peers_set = {f"{peers[0].ip[0]}:{peers[0].ip[1]}"} if peers else {"unknown"}
                # Znormalizuj do stringów
                peers_set = {f"{p[0]}:{p[1]}" if isinstance(p, tuple) else p for p in peers_set}
                peer = ", ".join(peers_set)
                piece_idx = alert.piece_index
                piece_size = get_piece_size(torrent_info, piece_idx)
                ts = datetime.now().strftime("%H:%M:%S.%f")
                elapsed = time.time() - start_time
                piece_events.append((elapsed, piece_idx, peers_set))
                print(f"\n[{ts}] [+{elapsed:.3f}s] [Piece {piece_idx:3d}] size={format_size(piece_size)} peer={peer}")

        status = handle.status()
        peer_infos = handle.get_peer_info()
        connected_ips = {p.ip for p in peer_infos}
        for addr in seed_addrs:
            if addr not in connected_ips:
                handle.connect_peer(addr)

        now = time.time()
        elapsed = now - start_time

        # Próbkuj RTT co 100 ms (jak w QUIC)
        if now - last_rtt_sample >= 0.1:
            last_rtt_sample = now
            ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")
            for p in peer_infos:
                peer_ip = f"{p.ip[0]}:{p.ip[1]}"
                rtt_ms = getattr(p, "rtt", 0)
                if rtt_ms and rtt_ms > 0:
                    conn = addr_to_conn.get(peer_ip, peer_ip)
                    rtt_values[conn].append(rtt_ms)
                    rtt_csv.write(f"{ts},{conn},{int(rtt_ms * 1e6)},{rtt_ms:.6f}\n")
            rtt_csv.flush()

        # Próbkuj throughput co 1s
        window_elapsed = now - last_sample_time
        if window_elapsed >= 1.0:
            window_bytes = {}
            for peer, total in peer_bytes_total.items():
                prev = peer_bytes_prev.get(peer, 0)
                window_bytes[peer] = total - prev
            peer_bytes_prev = dict(peer_bytes_total)
            throughput_samples.append((window_start_elapsed, window_bytes))
            ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")
            for peer, wb in window_bytes.items():
                tp_mbs = wb / window_elapsed / 1024 / 1024
                conn = addr_to_conn.get(peer, peer)
                tp_values[conn].append(tp_mbs)
                tp_csv.write(f"{ts},{conn},{tp_mbs:.6f},{peer_bytes_total[peer]}\n")
            tp_csv.flush()
            last_sample_time = now
            window_start_elapsed = elapsed

        print(
            f"\r"
            f"Progress: {status.progress * 100:.2f}% "
            f"Down: {status.download_rate / 1024:.1f} kB/s "
            f"Peers: {status.num_peers}",
            end="",
            flush=True
        )

        time.sleep(0.05)

    # Ostatnie alerty
    alerts = session.pop_alerts()
    for alert in alerts:
        if type(alert).__name__ == 'block_finished_alert':
            peer_ip = f"{alert.ip[0]}:{alert.ip[1]}"
            if alert.piece_index not in piece_peers:
                piece_peers[alert.piece_index] = set()
            piece_peers[alert.piece_index].add(peer_ip)
    for alert in alerts:
        if type(alert).__name__ == 'piece_finished_alert':
            peers_set = piece_peers.get(alert.piece_index)
            peer = ", ".join(peers_set) if peers_set else "unknown"
            piece_idx = alert.piece_index
            piece_size = get_piece_size(torrent_info, piece_idx)
            ts = datetime.now().strftime("%H:%M:%S.%f")[:-3]
            print(f"\n[{ts}] [Piece {piece_idx:3d}] size={format_size(piece_size)} peer={peer}")

    print("\nDownload finished")
    rtt_csv.close()
    tp_csv.close()
    if first_block_time is not None:
        transfer_time = time.time() - first_block_time
    else:
        transfer_time = time.time() - start_time
    with open(os.path.join(run_dir, "transfer_time.txt"), "w") as f:
        f.write("transfer_time_s=%.2f\n" % transfer_time)

    # Summary (jak w QUIC): combined + per conn
    combined_rtt = [v for vals in rtt_values.values() for v in vals]
    write_metric_summary(
        os.path.join(run_dir, "stats_rtt", "rtt_summary.txt"),
        "Combined (all connections)", "Smoothed RTT (ms)", combined_rtt, transfer_time)
    for conn, vals in rtt_values.items():
        write_metric_summary(
            os.path.join(run_dir, "stats_rtt", "rtt_summary_%s.txt" % conn),
            conn, "Smoothed RTT (ms)", vals)

    combined_tp = [v for vals in tp_values.values() for v in vals]
    write_metric_summary(
        os.path.join(run_dir, "stats_throughput", "throughput_summary.txt"),
        "Combined (all connections)", "Throughput (MB/s)", combined_tp, transfer_time)
    for conn, vals in tp_values.items():
        write_metric_summary(
            os.path.join(run_dir, "stats_throughput", "throughput_summary_%s.txt" % conn),
            conn, "Throughput (MB/s)", vals)

    print("Metryki (rtt, throughput, czas) zapisane do:", run_dir)
    if throughput_samples or piece_events:
        plot_all(throughput_samples, piece_events, CHARTS_DIR)


if __name__ == "__main__":
    torrent = sys.argv[1]
    destination = os.path.join(os.path.dirname(__file__), "downloads")

    os.makedirs(destination, exist_ok=True)

    session = create_client()

    handle = add_torrent(
        session,
        torrent,
        destination
    )

    handle.set_sequential_download(True)

    seed_addrs = [
        ("212.51.220.6", 5201),
        ("20.107.170.9", 4443),
    ]

    # seed_addrs = [
    #     ("127.0.0.1", 4443),
    #     ("127.0.0.1", 4444)
    # ]

    for addr in seed_addrs:
        print("Connecting:", addr)
        handle.connect_peer(addr)

    monitor(handle, session, seed_addrs)