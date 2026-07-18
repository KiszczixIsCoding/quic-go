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





def monitor(handle, session, seed_addrs=None):
    if seed_addrs is None:
        seed_addrs = []
    torrent_info = handle.torrent_file()
    piece_peers = {}    # piece_index -> set of peer IPs
    pending_pieces = set()  # pieces that got piece_finished but not all blocks yet
    start_time = time.time()

    # Dane do wykresu: lista (elapsed, {peer: bytes_in_window})
    throughput_samples = []
    peer_bytes_total = defaultdict(int)   # narastające bajty per peer
    peer_bytes_prev = {}                  # bajty na początku poprzedniego okna
    last_sample_time = start_time
    window_start_elapsed = 0.0
    # Dane do scatter plota: lista (elapsed, piece_idx, peers_set)
    piece_events = []

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
        connected_ips = {p.ip for p in handle.get_peer_info()}
        for addr in seed_addrs:
            if addr not in connected_ips:
                handle.connect_peer(addr)

        # Próbkuj throughput co 1s
        now = time.time()
        elapsed = now - start_time
        window_elapsed = now - last_sample_time
        if window_elapsed >= 1.0:
            window_bytes = {}
            for peer, total in peer_bytes_total.items():
                prev = peer_bytes_prev.get(peer, 0)
                window_bytes[peer] = total - prev
            peer_bytes_prev = dict(peer_bytes_total)
            throughput_samples.append((window_start_elapsed, window_bytes))
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

    for p in handle.get_peer_info():
        print(
            "IP:", p.ip,
            "client:", p.client,
            "down:", p.down_speed,
            "up:", p.up_speed
        )

    print("Connecting to peers...", flush=True)
    handle.resume()  # Start pobierania

    seed_addrs = [
        ("212.51.220.6", 5201),
        ("20.107.170.9", 4443),
    ]

    for addr in seed_addrs:
        print(f"Connecting to seed: {addr[0]}:{addr[1]}", flush=True)
        handle.connect_peer(addr)

    session.apply_settings({"download_rate_limit": 1})  # zamroź pobieranie
    print("Waiting for 2 peers...", flush=True)
    while True:
        peers = handle.get_peer_info()
        connected_ips = {p.ip for p in peers}
        for addr in seed_addrs:
            if addr not in connected_ips:
                handle.connect_peer(addr)
        num = len(peers)
        peers_str = ", ".join(f"{p.ip[0]}:{p.ip[1]}" for p in peers) if peers else "none"
        print(f"\rPeers connected: {num} [{peers_str}]", end="", flush=True)
        if num >= 2:
            break
        time.sleep(0.5)
    session.apply_settings({"download_rate_limit": 0})  # odblokuj
    print(f"\nGot {handle.status().num_peers} peers, starting download.", flush=True)

    monitor(handle, session, seed_addrs)