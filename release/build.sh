#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./release/build.sh /path/to/labosurf_pub.key
# The private key is never read by this script.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KEY_FILE="${1:-}"
[[ -f "$KEY_FILE" ]] || { echo "Usage: $0 /path/to/labosurf_pub.key" >&2; exit 2; }
PUBKEY="$(tr -d '[:space:]' < "$KEY_FILE")"
[[ "${#PUBKEY}" -eq 64 ]] || { echo "Clé publique Ed25519 invalide (64 caractères hex attendus)." >&2; exit 1; }

cd "$ROOT/engines/udp"
mkdir -p "$ROOT/dist"
VERSION="$(sed -n 's/^const engineVersion = "\(.*\)"/\1/p' main.go)"
LDFLAGS="-X main.embeddedVerifyKeyHex=${PUBKEY}"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$ROOT/dist/labosurf-linux-amd64" .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "$ROOT/dist/labosurf-linux-arm64" .
cp "$KEY_FILE" "$ROOT/dist/license_pub.key"

echo "Release ${VERSION} construite dans dist/."
sha256sum "$ROOT/dist/"labosurf-linux-* > "$ROOT/dist/SHA256SUMS"
