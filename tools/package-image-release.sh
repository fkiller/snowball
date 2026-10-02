#!/usr/bin/env bash
set -euo pipefail
VERSION=$(node -p "require('./package.json').version")
ARCH=${1:?provide arm64 or amd64}
case "$ARCH" in arm64|amd64) ;; *) exit 2 ;; esac
IMAGE="snowball-voice:$VERSION"
OUT="$PWD/artifacts/releases"
SOURCE="$PWD/artifacts/corresponding-source-$ARCH"
mkdir -p "$OUT" "$SOURCE"
# No entrypoint, production mounts, host listeners, profiles, or credentials.
docker run --rm --user 0 --entrypoint python3 \
  -v "$PWD/tools/collect-image-sources.py:/collector.py:ro" \
  -v "$SOURCE:/sources" "$IMAGE" /collector.py
git archive --format=tar.gz --output="$SOURCE/snowball-project-source.tar.gz" HEAD
(cd gateway && go mod vendor && tar -czf "$SOURCE/go-dependency-source.tar.gz" vendor && rm -rf vendor)
cp Dockerfile "$SOURCE/"
cp THIRD_PARTY_NOTICES.md "$SOURCE/"
cat > "$SOURCE/REBUILD.md" <<'EOF'
# Corresponding source

This directory accompanies the matching Snowball-Voice-Gate image. It retains
the exact Debian .dsc/orig/debian source packages and build scripts, Node and
noVNC source, Go dependency source, project source/Dockerfile, and installed
copyright/license notices. Debian sources are authenticated by APT. Sources
are unmodified upstream sources except the project changes recorded in Git.

Rebuild Snowball with its Dockerfile and locked dependencies. Individual Debian
packages can be unpacked with dpkg-source -x PACKAGE.dsc and built following
debian/rules (install the descriptor's Build-Depends). Node/noVNC retain their
upstream build instructions. LGPL libraries remain replaceable inside a rebuilt
container; no signed firmware or locked device is distributed with the Gateway.
Keep all source parts and SHA256SUMS for as long as the corresponding image is
distributed. To restore split archives: cat NAME.tar.gz.part-* > NAME.tar.gz.
EOF
BASE="snowball-voice-gate-$VERSION-linux-$ARCH"
docker image inspect "$IMAGE" --format '{{.Id}}' > "$OUT/$BASE-image-id.txt"
docker save "$IMAGE" | gzip -1 > "$OUT/$BASE-image.tar.gz"
# Prove the downloadable image can be loaded, preserving the saved image ID.
docker load -i "$OUT/$BASE-image.tar.gz"
test "$(docker image inspect "$IMAGE" --format '{{.Id}}')" = "$(cat "$OUT/$BASE-image-id.txt")"
tar -czf "$OUT/$BASE-corresponding-source.tar.gz" -C "$SOURCE" .
if [ "$(stat -c %s "$OUT/$BASE-corresponding-source.tar.gz")" -gt 1800000000 ]; then
  split -b 1800000000 -d "$OUT/$BASE-corresponding-source.tar.gz" "$OUT/$BASE-corresponding-source.tar.gz.part-"
  rm "$OUT/$BASE-corresponding-source.tar.gz"
fi
for file in "$OUT/$BASE"*; do
  case "$file" in *.sha256) continue ;; esac
  (cd "$OUT" && sha256sum "$(basename "$file")") > "$file.sha256"
done
