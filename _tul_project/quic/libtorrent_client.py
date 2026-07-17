import libtorrent as lt
import time
import sys
import os
from datetime import datetime


def create_client():

    ses = lt.session()

    ses.apply_settings({
    "enable_lsd": False,
    "enable_dht": False,
    "enable_upnp": False,
    "enable_natpmp": False,
    "listen_interfaces": "0.0.0.0:52864",
    "alert_queue_size": 100000,
    "download_rate_limit": 10,  # limit 100 KB/s żeby wymusić użycie obu peerów
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


def monitor(handle, session):
    torrent_info = handle.torrent_file()
    piece_peers = {}    # piece_index -> set of peer IPs
    pending_pieces = set()  # pieces that got piece_finished but not all blocks yet

    while not handle.status().is_seeding:
        alerts = session.pop_alerts()
        for alert in alerts:
            name = type(alert).__name__
            if name == 'block_finished_alert':
                peer_ip = f"{alert.ip[0]}:{alert.ip[1]}"
                if alert.piece_index not in piece_peers:
                    piece_peers[alert.piece_index] = set()
                piece_peers[alert.piece_index].add(peer_ip)
                # Sprawdź czy mamy oczekujący piece do zalogowania
                if alert.piece_index in pending_pieces:
                    pending_pieces.discard(alert.piece_index)
            elif name == 'piece_finished_alert':
                pending_pieces.add(alert.piece_index)
                # Sprawdź czy mamy już peerów
                peers_set = piece_peers.get(alert.piece_index)
                if peers_set:
                    peer = ", ".join(peers_set)
                else:
                    # Fallback — brakiemy block_finished
                    peers = handle.get_peer_info()
                    peer = peers[0].ip if peers else "unknown"
                piece_idx = alert.piece_index
                piece_size = get_piece_size(torrent_info, piece_idx)
                ts = datetime.now().strftime("%H:%M:%S.%f")[:-3]
                print(f"\n[{ts}] [Piece {piece_idx:3d}] size={format_size(piece_size)} peer={peer}")

        status = handle.status()
        print(
            f"\r"
            f"Progress: {status.progress * 100:.2f}% "
            f"Down: {status.download_rate / 1024:.1f} kB/s "
            f"Peers: {status.num_peers}",
            end="",
            flush=True
        )

        time.sleep(1)

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


if __name__ == "__main__":
    torrent = sys.argv[1]
    destination = os.path.abspath("./downloads")

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
    handle.connect_peer(("127.0.0.1", 52865))
    handle.connect_peer(("127.0.0.1", 52863))

    handle.resume()  # Start pobierania
    monitor(handle, session)