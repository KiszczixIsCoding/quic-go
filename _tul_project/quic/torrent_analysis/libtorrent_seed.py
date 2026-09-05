import libtorrent as lt
import time
import sys
import os
from datetime import datetime


def create_session(listen_interface):
    ses = lt.session()
    ses.apply_settings({
        "enable_lsd": False,
        "enable_dht": False,
        "enable_upnp": False,
        "enable_natpmp": False,
        "listen_interfaces": listen_interface,
        "enable_outgoing_utp": True,
        "enable_incoming_utp": True,
        "connections_limit": 100,
        "upload_rate_limit": 4 * 1024 * 1024,  # 2 MB/s
    })
    print("Listening on port:", ses.listen_port())
    return ses


def add_torrent(session, torrent_file, download_path):
    params = {
        "save_path": download_path,
        "ti": lt.torrent_info(torrent_file)
    }
    handle = session.add_torrent(params)
    return handle


def monitor(handle, run_dir):
    inflight_csv = open(os.path.join(run_dir, "stats_inflight", "inflight.csv"), "w")
    inflight_csv.write("timestamp,peer,upload_in_flight\n")
    last_sample = time.time()
    while True:
        now = time.time()
        if now - last_sample >= 0.1:
            last_sample = now
            ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")
            for p in handle.get_peer_info():
                peer_ip = f"{p.ip[0]}:{p.ip[1]}"
                inflight = getattr(p, "upload_in_flight", 0)
                inflight_csv.write(f"{ts},{peer_ip},{inflight}\n")
            inflight_csv.flush()
        status = handle.status()
        print(
            f"\r"
            f"Progress: {status.progress * 100:.2f}% "
            f"Up: {status.upload_rate / 1024:.1f} kB/s "
            f"Down: {status.download_rate / 1024:.1f} kB/s "
            f"Peers: {status.num_peers}",
            end="",
            flush=True
        )
        time.sleep(0.05)


if __name__ == "__main__":
    torrent = sys.argv[1]
    source = os.path.abspath(sys.argv[2])
    listen_interface = sys.argv[3] if len(sys.argv) > 3 else "0.0.0.0:6881"

    ti = lt.torrent_info(torrent)
    print(f"Torrent name: {ti.name()}")
    print(f"Num files: {ti.num_files()}")
    files = ti.files()
    for i in range(files.num_files()):
        print(f"  [{i}] {files.file_path(i, ti.name())} ({files.file_size(i)} bytes)")
    print(f"\nUsing save_path: {source}")
    print(f"Directory exists: {os.path.isdir(source)}")
    if os.path.isdir(source):
        for root, dirs, files_list in os.walk(source):
            for fn in files_list:
                print(f"  Found: {os.path.join(root, fn)}")
    elif os.path.isfile(source):
        print(f"  Is file: {source}")

    session = create_session(listen_interface)
    handle = add_torrent(session, torrent, source)
    print("Verifying storage...", flush=True)
    handle.resume()
    status = handle.status()
    while not status.is_seeding:
        status = handle.status()
        print(f"  Progress: {status.progress*100:.1f}% state: {status.state}", flush=True)
        time.sleep(1)
        if status.error:
            print(f"  Error: {status.error}", flush=True)
            break

    print(f"Seeding... (Ctrl+C to stop)")
    print(f"Info hash: {status.info_hash} seed: {status.is_seeding} peers: {status.num_peers}", flush=True)
    base_dir = os.path.dirname(os.path.abspath(__file__))
    run_dir = os.path.join(base_dir, "runs", "run_" + datetime.now().strftime("%Y%m%d_%H%M%S"))
    os.makedirs(os.path.join(run_dir, "stats_inflight"), exist_ok=True)
    print(f"Inflight log: {os.path.join(run_dir, 'stats_inflight', 'inflight.csv')}", flush=True)
    try:
        monitor(handle, run_dir)
    except KeyboardInterrupt:
        print("\nStopped.")
