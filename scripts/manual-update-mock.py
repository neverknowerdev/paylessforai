#!/usr/bin/env python3
"""Minimal local GitHub Releases API and asset server for updater drills."""

import argparse
import hashlib
import json
import pathlib
import signal
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def build_manifest(args, artifact_path, port):
    data = artifact_path.read_bytes()
    manifest = {
        "schema": 1,
        "channel": args.channel,
        "version": args.version,
        "commit": args.commit,
        "published_at": "2026-09-01T00:00:00Z",
        "min_supervisor_protocol": 1,
        "schema_compatibility": {"min": 1, "max": 999999},
        "artifacts": [{
            "os": args.os,
            "arch": args.arch,
            "url": f"http://127.0.0.1:{port}/assets/{artifact_path.name}",
            "size": len(data),
            "sha256": hashlib.sha256(data).hexdigest(),
            "name": artifact_path.name,
        }],
    }
    return json.dumps(manifest, separators=(",", ":")).encode("utf-8"), data


def sign_manifest(contents, key_path):
    with tempfile.TemporaryDirectory(prefix="paylessforai-update-mock-") as temp:
        input_path = pathlib.Path(temp) / "manifest.json"
        output_path = pathlib.Path(temp) / "manifest.sig"
        input_path.write_bytes(contents)
        subprocess.run([
            "openssl", "pkeyutl", "-sign", "-rawin",
            "-inkey", str(key_path), "-in", str(input_path),
            "-out", str(output_path),
        ], check=True, capture_output=True)
        return output_path.read_bytes()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--artifact", required=True, type=pathlib.Path)
    parser.add_argument("--private-key", required=True, type=pathlib.Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--channel", choices=("main", "releases"), required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--commit", default="mock-commit")
    parser.add_argument("--os", default="darwin")
    parser.add_argument("--arch", default="arm64")
    args = parser.parse_args()

    artifact_path = args.artifact.resolve()
    manifest, artifact = build_manifest(args, artifact_path, args.port)
    signature = sign_manifest(manifest, args.private_key.resolve())
    prerelease = args.channel == "main"
    assets = [
        {"name": "update-manifest.json", "browser_download_url": f"http://127.0.0.1:{args.port}/assets/update-manifest.json"},
        {"name": "update-manifest.json.sig", "browser_download_url": f"http://127.0.0.1:{args.port}/assets/update-manifest.json.sig"},
        {"name": artifact_path.name, "browser_download_url": f"http://127.0.0.1:{args.port}/assets/{artifact_path.name}"},
    ]
    releases = [{"tag_name": args.tag, "prerelease": prerelease, "draft": False, "published_at": "2026-09-01T00:00:00Z", "assets": assets}]

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, format_string, *values):
            return

        def do_GET(self):
            if self.path == "/releases":
                body = json.dumps(releases, separators=(",", ":")).encode("utf-8")
            elif self.path == "/assets/update-manifest.json":
                body = manifest
            elif self.path == "/assets/update-manifest.json.sig":
                body = signature
            elif self.path == f"/assets/{artifact_path.name}":
                body = artifact
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"READY {args.port}", flush=True)
    signal.signal(signal.SIGTERM, lambda *_: threading.Thread(target=server.shutdown, daemon=True).start())
    signal.signal(signal.SIGINT, lambda *_: threading.Thread(target=server.shutdown, daemon=True).start())
    server.serve_forever()


if __name__ == "__main__":
    main()
