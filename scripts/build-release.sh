#!/usr/bin/env bash
set -euo pipefail

version="${1:?version is required}"
channel="${2:?channel is required}"
tag="${3:?tag is required}"
out="${4:-dist}"
mkdir -p "$out/assets"
out_abs="$(cd "$out" && pwd)"
commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
official="${OFFICIAL_BUILD:-false}"
public_key="${UPDATE_PUBLIC_KEY:-}"
if [[ "$official" == true && -z "$public_key" ]]; then echo "UPDATE_PUBLIC_KEY is required for an official build" >&2; exit 1; fi
ldflags="-s -w -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Version=${version} -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Channel=${channel} -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Commit=${commit} -X github.com/neverknowerdev/paylessforai/internal/buildinfo.BuiltAt=${built_at} -X github.com/neverknowerdev/paylessforai/internal/buildinfo.Official=${official} -X github.com/neverknowerdev/paylessforai/internal/buildinfo.UpdatePublicKey=${public_key}"

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os="${target%/*}"; arch="${target#*/}"; suffix=""; [[ "$os" == windows ]] && suffix=".exe"
  name="paylessforai_${version}_${os}_${arch}"
  work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$work/paylessforai-app$suffix" ./cmd/paylessforai-app
  if [[ "$os" == windows ]]; then (cd "$work" && zip -q -X "$out_abs/assets/$name.zip" "paylessforai-app$suffix"); else (cd "$work" && COPYFILE_DISABLE=1 tar -czf "$out_abs/assets/$name.tar.gz" "paylessforai-app$suffix"); fi
  rm -rf "$work"; trap - EXIT
done

python3 - "$out" "$version" "$channel" "$tag" "$commit" "$built_at" <<'PY'
import hashlib, json, os, pathlib, sys
out, version, channel, tag, commit, built_at = sys.argv[1:]
base = pathlib.Path(out) / "assets"
artifacts = []
for path in sorted(base.iterdir()):
    parts = path.name.rsplit('.', 2)[0].split('_')
    os_name, arch = parts[-2], parts[-1]
    artifacts.append({"os": os_name, "arch": arch,
        "url": f"https://github.com/neverknowerdev/paylessforai/releases/download/{tag}/{path.name}",
        "size": path.stat().st_size, "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
        "name": path.name})
manifest = {"schema": 1, "channel": channel, "version": version, "commit": commit,
    "published_at": built_at, "min_supervisor_protocol": 1,
    "schema_compatibility": {"min": 1, "max": 999999}, "artifacts": artifacts}
pathlib.Path(out, "update-manifest.json").write_text(json.dumps(manifest, separators=(',', ':')) + "\n")
with open(pathlib.Path(out, "checksums.txt"), 'w') as f:
    for p in sorted(base.iterdir()): f.write(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n")
PY

if [[ -n "${UPDATE_SIGNING_PRIVATE_KEY:-}" ]]; then
  printf '%s' "$UPDATE_SIGNING_PRIVATE_KEY" > "$out/signing-key.pem"
  openssl pkeyutl -sign -rawin -inkey "$out/signing-key.pem" -in "$out/update-manifest.json" -out "$out/update-manifest.json.sig"
  rm -f "$out/signing-key.pem"
else
  if [[ "${ALLOW_UNSIGNED:-false}" != true ]]; then echo "UPDATE_SIGNING_PRIVATE_KEY is required for a publish build" >&2; exit 1; fi
  : > "$out/update-manifest.json.sig"
fi
