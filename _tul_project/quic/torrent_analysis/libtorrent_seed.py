import libtorrent as lt
import time
import sys
import os


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
        # "upload_rate_limit": 2 * 1024 * 1024,  # 2 MB/s
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


def monitor(handle):
    while True:
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
        time.sleep(1)


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
            print(f"  Error: {status.error.message}", flush=True)
            break

    print(f"Seeding... (Ctrl+C to stop)")
    print(f"Info hash: {status.info_hash} seed: {status.is_seeding} peers: {status.num_peers}", flush=True)
    try:
        monitor(handle)
    except KeyboardInterrupt:
        print("\nStopped.")
