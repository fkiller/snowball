#!/usr/bin/env bash
set -euo pipefail
VERSION=0.4.0-alpha.1
case "$(uname -m)" in aarch64|arm64) ARCH=arm64 ;; x86_64|amd64) ARCH=amd64 ;; *) printf 'Unsupported architecture.\n' >&2; exit 1 ;; esac
command -v curl >/dev/null || { printf 'Install curl first.\n' >&2; exit 1; }
command -v docker >/dev/null || { printf 'Install Docker Engine first.\n' >&2; exit 1; }
docker info >/dev/null
DIR=$(mktemp -d)
trap 'rm -rf "$DIR"' EXIT
ASSET="snowball-voice-gate-$VERSION-linux-$ARCH-image.tar.gz"
URL="https://github.com/fkiller/snowball/releases/download/v$VERSION"
curl --fail --location --retry 3 "$URL/$ASSET" -o "$DIR/$ASSET"
curl --fail --location --retry 3 "$URL/$ASSET.sha256" -o "$DIR/$ASSET.sha256"
(cd "$DIR" && sha256sum --check "$ASSET.sha256")
docker load --input "$DIR/$ASSET"
printf 'Downloaded and checksum-verified Snowball-Voice-Gate %s (%s). Follow docs/PLATFORMS.md to install.\n' "$VERSION" "$ARCH"
